package cmd

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"golang.org/x/crypto/ssh"

	"github.com/bresilla/drop/src/pkg/arch"
	"github.com/bresilla/drop/src/pkg/book"
	"github.com/bresilla/drop/src/pkg/history"
	"github.com/bresilla/drop/src/pkg/node"
	"github.com/bresilla/drop/src/pkg/ns"
	"github.com/bresilla/drop/src/pkg/proto"
	"github.com/bresilla/drop/src/pkg/user"
)

// This machine speaking first, answered by a machine that holds the same namespace.
//
// Both ends run in this process, so they cannot have a data directory each. They are told to call
// the namespace two different names instead, which gives them a history each: what a name is worked
// out from never travels in a catch-up, so the exchange is the one the two machines would have.

// asSomebody gives this machine a user key to sign changes with, and homes of its own.
func asSomebody(t *testing.T) string {
	t.Helper()

	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	at := filepath.Join(t.TempDir(), "user")
	_, secret, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generating a user key: %v", err)
	}
	block, err := ssh.MarshalPrivateKey(secret, "a test")
	if err != nil {
		t.Fatalf("writing a user key: %v", err)
	}
	if err := os.WriteFile(at, pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DROP_USER_KEY", at)

	key, err := user.Public()
	if err != nil {
		t.Fatalf("reading the user key back: %v", err)
	}
	return user.Text(key)
}

// bookWith is an address book with one paired machine, belonging to the person this machine signs
// as, so that a change made here is a change that person made.
func bookWith(t *testing.T, key string) (*book.Book, book.Entry) {
	t.Helper()

	b, err := book.Load()
	if err != nil {
		t.Fatalf("book.Load(): %v", err)
	}
	b.Pair("bob", idFor(9), make([]byte, book.SecretBytes))
	b.Belongs("bob", key)

	entry, ok := b.Lookup("bob")
	if !ok {
		t.Fatal("bob is not in the book")
	}
	return b, entry
}

func mounted(t *testing.T, at string, shared ns.Shared, named []string) *ns.Table {
	t.Helper()

	table := ns.NewTable()
	m := ns.Mount{Path: at, Archetype: "chat", Access: ns.Access{Named: named}, Shared: shared}
	if err := table.Add(m); err != nil {
		t.Fatalf("adding %s: %v", at, err)
	}
	return table
}

func logOf(t *testing.T, shared ns.Shared) *history.Log {
	t.Helper()

	l, err := history.Open(shared.ID())
	if err != nil {
		t.Fatalf("history.Open(): %v", err)
	}
	return l
}

// answers is the far end: it takes whatever this machine opens and serves it against a table and a
// history of its own, and remembers which paths were asked for.
type answers struct {
	table  *ns.Table
	pinned *book.Book

	mu    sync.Mutex
	asked []string
}

func (a *answers) To(ctx context.Context, entry book.Entry, alpn string) (io.Closer, proto.Stream, error) {
	here, there := net.Pipe()

	go func() {
		defer there.Close()
		_ = proto.Handle(ctx, there, idFor(9), proto.Policy{
			Mounts:     a.table,
			Archetypes: arch.NewRegistry(),
			Who: func(node.ID, proto.Badged) ns.Caller {
				return ns.Caller{ID: idFor(9).String(), Name: "bob", UserName: "bob", Paired: true}
			},
			Allow: func(_ node.ID, open proto.Opening) (bool, string) {
				a.mu.Lock()
				a.asked = append(a.asked, open.Path)
				a.mu.Unlock()
				return true, ""
			},
			Met: meeting(a.table, a.pinned, nil),
		})
	}()
	return here, here, nil
}

func (a *answers) paths() []string {
	a.mu.Lock()
	defer a.mu.Unlock()

	return append([]string(nil), a.asked...)
}

// A change made here reaches a machine this one speaks to first, without that machine asking.
func TestSpeakingFirstCatchesAPeerUp(t *testing.T) {
	key := asSomebody(t)
	pinned, entry := bookWith(t, key)

	mine := ns.Shared{Creator: key, At: "/notes", Nonce: "here"}
	theirs := ns.Shared{Creator: key, At: "/notes", Nonce: "there"}

	here := mounted(t, "/notes", mine, []string{"bob"})
	there := &answers{table: mounted(t, "/notes", theirs, []string{"bob"}), pinned: pinned}

	change, err := history.Sign([]byte("written here"), nil)
	if err != nil {
		t.Fatalf("Sign(): %v", err)
	}
	if _, err := logOf(t, mine).Add(change); err != nil {
		t.Fatalf("Add(): %v", err)
	}

	pushTo(context.Background(), there, entry, here, pinned)

	if !logOf(t, theirs).Has(change.ID()) {
		t.Fatal("the change did not reach the machine that holds it too")
	}
}

// Which namespaces a peer is spoken to about is the access rule read against the address book, so
// one the rule does not name them in is not one they hear about.
func TestSpeakingFirstLeavesAloneWhatThePeerDoesNotHold(t *testing.T) {
	key := asSomebody(t)
	pinned, entry := bookWith(t, key)

	shared := ns.Shared{Creator: key, At: "/notes", Nonce: "here"}
	mine := ns.Shared{Creator: key, At: "/private", Nonce: "here"}

	here := mounted(t, "/notes", shared, []string{"bob"})
	if err := here.Add(ns.Mount{
		Path:      "/private",
		Archetype: "chat",
		Access:    ns.Access{Named: []string{"carol"}},
		Shared:    mine,
	}); err != nil {
		t.Fatalf("adding /private: %v", err)
	}

	there := &answers{
		table:  mounted(t, "/notes", ns.Shared{Creator: key, At: "/notes", Nonce: "there"}, []string{"bob"}),
		pinned: pinned,
	}

	pushTo(context.Background(), there, entry, here, pinned)

	asked := there.paths()
	if !has(asked, "/notes") {
		t.Fatalf("the namespace they hold was not mentioned: %v", asked)
	}
	if has(asked, "/private") {
		t.Fatalf("a namespace the rule does not name them in was mentioned: %v", asked)
	}
}

// A namespace this machine holds alone is not one anybody is caught up on.
func TestSpeakingFirstSaysNothingAboutANamespaceHeldAlone(t *testing.T) {
	key := asSomebody(t)
	pinned, entry := bookWith(t, key)

	here := mounted(t, "/chat", ns.Shared{}, []string{"bob"})
	there := &answers{table: here, pinned: pinned}

	pushTo(context.Background(), there, entry, here, pinned)

	if asked := there.paths(); has(asked, "/chat") {
		t.Fatalf("a namespace nobody else holds was mentioned: %v", asked)
	}
}
