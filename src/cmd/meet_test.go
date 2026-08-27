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
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/bresilla/drop/src/pkg/among"
	"github.com/bresilla/drop/src/pkg/arch"
	"github.com/bresilla/drop/src/pkg/book"
	"github.com/bresilla/drop/src/pkg/history"
	"github.com/bresilla/drop/src/pkg/meet"
	"github.com/bresilla/drop/src/pkg/node"
	"github.com/bresilla/drop/src/pkg/ns"
	"github.com/bresilla/drop/src/pkg/proto"
	"github.com/bresilla/drop/src/pkg/user"
)

// This machine speaking first, answered by a machine that holds the same namespace.
//
// Both ends run in this process and the two of them hold one thing, so the far end's history is
// opened under a data directory of its own and handed to it. Two machines holding one namespace
// call it one name, and that name is what the meeting is addressed by.

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

// theirLog is the same namespace's history in a data directory of its own, which is what the far
// end has and what one process cannot get out of history.Open twice.
func theirLog(t *testing.T, shared ns.Shared) *history.Log {
	t.Helper()

	mine := os.Getenv("XDG_DATA_HOME")
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	l := logOf(t, shared)
	t.Setenv("XDG_DATA_HOME", mine)
	return l
}

// answers is the far end: it takes whatever this machine opens and serves it against a table and a
// history of its own, and remembers which namespaces were asked about.
type answers struct {
	table  *ns.Table
	pinned *book.Book
	log    *history.Log

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
			Who: func(node.ID, proto.Badged, proto.Stood) ns.Caller {
				return ns.Caller{ID: idFor(9).String(), Name: "bob", UserName: "bob", Paired: true}
			},
			Allow: func(_ node.ID, open proto.Opening) (bool, string) {
				if open.Meet {
					a.mu.Lock()
					a.asked = append(a.asked, open.Held)
					a.mu.Unlock()
				}
				return true, ""
			},
			Met: a.caught,
		})
	}()
	return here, here, nil
}

// caught answers a meeting against the history the far end was given.
func (a *answers) caught(m proto.Meeting) error {
	rule, _ := a.table.AccessFor(m.Mount.Path)
	_, err := meet.Answer(m.Conn, a.log, "bob", among.Admits(rule, a.pinned, ""))
	return err
}

func (a *answers) about() []string {
	a.mu.Lock()
	defer a.mu.Unlock()

	return append([]string(nil), a.asked...)
}

// A change made here reaches a machine this one speaks to first, without that machine asking.
func TestSpeakingFirstCatchesAPeerUp(t *testing.T) {
	key := asSomebody(t)
	pinned, entry := bookWith(t, key)

	shared := ns.Shared{Creator: key, At: "/notes", Nonce: "cafe"}

	here := mounted(t, "/notes", shared, []string{"bob"})
	there := &answers{table: mounted(t, "/notes", shared, []string{"bob"}), pinned: pinned, log: theirLog(t, shared)}

	change, err := history.Sign(shared.ID(), []byte("written here"), nil)
	if err != nil {
		t.Fatalf("Sign(): %v", err)
	}
	if _, err := logOf(t, shared).Add(change); err != nil {
		t.Fatalf("Add(): %v", err)
	}

	pushTo(context.Background(), there, entry, here, pinned)

	if !there.log.Has(change.ID()) {
		t.Fatal("the change did not reach the machine that holds it too")
	}
}

// A meeting is about the thing, not about where it is kept. A namespace joined at another path is
// one the two machines still catch up on, and one the far end holds at that same path by chance is
// not one they mistake it for.
func TestACatchUpFindsTheNamespaceUnderThePeersOwnName(t *testing.T) {
	key := asSomebody(t)
	pinned, entry := bookWith(t, key)

	shared := ns.Shared{Creator: key, At: "/notes", Nonce: "cafe"}
	other := ns.Shared{Creator: key, At: "/diary", Nonce: "beef"}

	here := mounted(t, "/bobs-notes", shared, []string{"bob"})

	table := mounted(t, "/notes", shared, []string{"bob"})
	if err := table.Add(ns.Mount{
		Path:      "/bobs-notes",
		Archetype: "chat",
		Access:    ns.Access{Named: []string{"bob"}},
		Shared:    other,
	}); err != nil {
		t.Fatalf("adding /bobs-notes: %v", err)
	}
	there := &answers{table: table, pinned: pinned, log: theirLog(t, shared)}

	change, err := history.Sign(shared.ID(), []byte("written here"), nil)
	if err != nil {
		t.Fatalf("Sign(): %v", err)
	}
	if _, err := logOf(t, shared).Add(change); err != nil {
		t.Fatalf("Add(): %v", err)
	}

	pushTo(context.Background(), there, entry, here, pinned)

	if !there.log.Has(change.ID()) {
		t.Fatal("a namespace held under another name here never reached the machine holding it")
	}
	if about := there.about(); !has(about, shared.ID()) {
		t.Fatalf("the meeting was about %v, want %s", about, shared.ID())
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

	there := &answers{table: mounted(t, "/notes", shared, []string{"bob"}), pinned: pinned, log: theirLog(t, shared)}

	pushTo(context.Background(), there, entry, here, pinned)

	asked := there.about()
	if !has(asked, shared.ID()) {
		t.Fatalf("the namespace they hold was not mentioned: %v", asked)
	}
	if has(asked, mine.ID()) {
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

	if asked := there.about(); len(asked) != 0 {
		t.Fatalf("a namespace nobody else holds was mentioned: %v", asked)
	}
}

// counting is a peer that can be reached, and remembers how often it was reached for.
type counting struct {
	started chan struct{}
	release chan struct{}

	mu sync.Mutex
	n  int
}

func (c *counting) To(ctx context.Context, entry book.Entry, alpn string) (io.Closer, proto.Stream, error) {
	c.mu.Lock()
	c.n++
	c.mu.Unlock()

	c.started <- struct{}{}
	<-c.release

	here, there := net.Pipe()
	there.Close()
	return here, here, nil
}

func (c *counting) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.n
}

// A change here reaches out to the other holders, and a change arriving while that is still going
// on is the same reaching out done once more rather than another one beside it.
//
// What a round says is whatever the history holds when it runs, so a peer sending changes one at a
// time would otherwise leave one outbound stream on this machine per change it chose to send.
func TestTellingTheOtherHoldersDoesNotStackUp(t *testing.T) {
	key := asSomebody(t)
	pinned, _ := bookWith(t, key)

	shared := ns.Shared{Creator: key, At: "/notes", Nonce: "cafe"}
	here := mounted(t, "/notes", shared, []string{"bob"})

	over := &counting{started: make(chan struct{}, 128), release: make(chan struct{})}
	changed := told(context.Background(), over, here, pinned)

	changed("/notes")
	<-over.started
	for range 50 {
		changed("/notes")
	}
	close(over.release)

	<-over.started
	time.Sleep(50 * time.Millisecond)

	if reached := over.count(); reached != 2 {
		t.Fatalf("the peer was reached for %d times, want one round and one more after it", reached)
	}
}
