package cmd

import (
	"context"
	"io"
	"net"
	"testing"

	"github.com/bresilla/drop/src/pkg/book"
	"github.com/bresilla/drop/src/pkg/convo"
	"github.com/bresilla/drop/src/pkg/node"
	"github.com/bresilla/drop/src/pkg/ns"
	"github.com/bresilla/drop/src/pkg/proto"
	"github.com/bresilla/drop/src/pkg/wire"
)

// A chat serves one namespace while it is open, and a namespace with no rule on it is one nobody
// can ever reach. Without a rule every line sent to a device running `drop chat` is refused.
func TestAChatNamespaceTakesAPairedDevice(t *testing.T) {
	table := chatMounts((&doings{}).talking())

	who := ns.Caller{ID: idFor(1).String(), Name: "bo", Paired: true}
	if ok, why := table.Admits("/chat", who); !ok {
		t.Fatalf("a paired device was refused the chat: %s", why)
	}
	if ok, _ := table.Admits("/chat", ns.Caller{ID: idFor(2).String()}); ok {
		t.Error("a stranger was let into the chat")
	}
}

// A far end that is not serving a chat this minute has not made a decision about the sender, and
// what is queued has to survive it. The daemon's backlog sweep meets exactly this: a `drop share`
// or `drop cast` on the other machine answers the door and serves one namespace that is not /chat.
func TestARefusalAboutTheNamespaceKeepsTheQueue(t *testing.T) {
	entry := queued(t, idFor(3), "still here")

	_, err := deliverOver(context.Background(), refusing{reason: "/chat: nothing here is shared with anyone"}, entry, "/chat", "chat")
	if !proto.WasDeclined(err) {
		t.Fatalf("deliverOver(): %v", err)
	}
	if left := stillQueued(t, entry); len(left) != 1 {
		t.Fatalf("%d messages are queued after a refusal the far end will not repeat", len(left))
	}
}

// A settled refusal is an answer, and retrying it means asking the same decision on every
// connection for as long as the machine runs.
func TestARefusalAboutTheSenderEmptiesTheQueue(t *testing.T) {
	entry := queued(t, idFor(4), "let me in")

	if _, err := deliverOver(context.Background(), refusing{reason: "/chat: not shared with you", settled: true}, entry, "/chat", "chat"); !proto.WasDeclined(err) {
		t.Fatalf("deliverOver(): %v", err)
	}
	if left := stillQueued(t, entry); len(left) != 0 {
		t.Fatalf("%d messages are still queued against a decision", len(left))
	}
}

// queued is a peer with one message waiting for it, in a home of this test's own.
func queued(t *testing.T, id node.ID, text string) book.Entry {
	t.Helper()

	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	entry := book.Entry{Name: "bo", ID: id}
	if _, err := compose(entry, convo.KindText, text, ""); err != nil {
		t.Fatalf("compose(): %v", err)
	}
	return entry
}

func stillQueued(t *testing.T, entry book.Entry) []convo.Message {
	t.Helper()

	store, err := convo.Open(entry.ID)
	if err != nil {
		t.Fatalf("convo.Open(): %v", err)
	}
	left, err := store.Pending()
	if err != nil {
		t.Fatalf("Pending(): %v", err)
	}
	return left
}

// refusing is a far end that reads the open and says no, which is what a device serving something
// else does.
type refusing struct {
	reason  string
	settled bool
}

func (r refusing) To(ctx context.Context, entry book.Entry, alpn string) (io.Closer, proto.Stream, error) {
	here, there := net.Pipe()

	go func() {
		defer there.Close()
		conn := wire.NewConn(there)
		if _, _, err := conn.ReadFrame(); err != nil {
			return
		}
		_ = conn.WriteFrame(wire.KindReject, wire.Reject{Reason: r.reason, Settled: r.settled}.Encode())
	}()
	return here, here, nil
}
