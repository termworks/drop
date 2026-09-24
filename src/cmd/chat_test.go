package cmd

import (
	"context"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bresilla/drop/src/pkg/book"
	"github.com/bresilla/drop/src/pkg/convo"
	"github.com/bresilla/drop/src/pkg/node"
	"github.com/bresilla/drop/src/pkg/ns"
	"github.com/bresilla/drop/src/pkg/proto"
	"github.com/bresilla/drop/src/pkg/wire"
)

// A chat serves one namespace while it is open, and a namespace with no rule on it is one nobody
// can ever reach. Without a rule every line sent to a device running `drop chat` is refused.
func TestAChatNamespaceTakesYourOwnMachines(t *testing.T) {
	table := chatMounts((&doings{}).talking())

	mine := ns.Caller{ID: idFor(1).String(), Name: "laptop", UserName: ns.LevelMe, Paired: true, Trusted: true}
	if ok, why := table.Admits("/chat", mine); !ok {
		t.Fatalf("a machine of yours was refused the chat: %s", why)
	}
	if ok, _ := table.Admits("/chat", ns.Caller{ID: idFor(4).String(), Name: "bo", UserName: "bo", Paired: true}); ok {
		t.Error("somebody merely paired was let into the chat before being let in")
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

func TestFailureToClearASettledRefusalIsReported(t *testing.T) {
	entry := queued(t, idFor(5), "let me in")
	outbox := filepath.Join(os.Getenv("XDG_DATA_HOME"), "drop", "convo", entry.ID.String(), "outbox")
	changed := make(chan error, 1)
	beforeReply := func() {
		if err := os.Remove(outbox); err != nil {
			changed <- err
			return
		}
		changed <- os.Mkdir(outbox, 0o700)
	}

	_, err := deliverOver(context.Background(), refusing{
		reason:      "/chat: not shared with you",
		settled:     true,
		beforeReply: beforeReply,
	}, entry, "/chat", "chat")
	if changedErr := <-changed; changedErr != nil {
		t.Fatal(changedErr)
	}
	if !proto.WasDeclined(err) {
		t.Fatalf("the joined error lost the settled refusal: %v", err)
	}
	if !strings.Contains(err.Error(), "clearing messages") {
		t.Fatalf("the failed outbox update was hidden: %v", err)
	}
}

func TestChatDeliveriesAreCoalescedAndSerialized(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	requests := make(chan struct{}, 1)
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	done := make(chan struct{})

	var mu sync.Mutex
	active, maximum, calls := 0, 0, 0
	deliver := func() error {
		mu.Lock()
		active++
		calls++
		if active > maximum {
			maximum = active
		}
		mu.Unlock()

		started <- struct{}{}
		<-release

		mu.Lock()
		active--
		mu.Unlock()
		return nil
	}
	go func() {
		deliveryLoop(ctx, time.Hour, requests, deliver, func(error) {})
		close(done)
	}()

	queueDelivery(requests)
	<-started
	for range 100 {
		queueDelivery(requests)
	}
	release <- struct{}{}
	<-started
	cancel()
	release <- struct{}{}

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("delivery loop did not stop")
	}
	mu.Lock()
	defer mu.Unlock()
	if calls != 2 {
		t.Fatalf("delivery calls = %d, want 2", calls)
	}
	if maximum != 1 {
		t.Fatalf("concurrent deliveries = %d, want 1", maximum)
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
	reason      string
	settled     bool
	beforeReply func()
}

func (r refusing) To(ctx context.Context, entry book.Entry, alpn string) (io.Closer, proto.Stream, error) {
	here, there := net.Pipe()

	go func() {
		defer func() { _ = there.Close() }()
		conn := wire.NewConn(there)
		if _, _, err := conn.ReadFrame(); err != nil {
			return
		}
		if r.beforeReply != nil {
			r.beforeReply()
		}
		_ = conn.WriteFrame(wire.KindReject, wire.Reject{Reason: r.reason, Settled: r.settled}.Encode())
	}()
	return here, here, nil
}
