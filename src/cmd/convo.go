package cmd

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/bresilla/drop/src/pkg/arch/chat"
	"github.com/bresilla/drop/src/pkg/book"
	"github.com/bresilla/drop/src/pkg/convo"
	"github.com/bresilla/drop/src/pkg/discovery"
	"github.com/bresilla/drop/src/pkg/node"
	"github.com/bresilla/drop/src/pkg/proto"
)

// deliverTo sends whatever is waiting for a peer and clears what the far end confirms it stored.
//
// Undelivered messages stay queued rather than being dropped, which is what makes sending to a
// device that is asleep work: it goes out when the device comes back.
func deliverTo(ctx context.Context, n *node.Node, lan *discovery.LAN, entry book.Entry, path, archetype string) (int, error) {
	return deliverOver(ctx, best(n, lan), entry, path, archetype)
}

// deliverOver sends what is queued over whatever connection the caller has.
//
// A held one where there is one. Finding a device and standing up a relay session is five seconds;
// a message on a connection that already exists is eight milliseconds. Dialling again for every
// line somebody types throws that away.
func deliverOver(ctx context.Context, over reaches, entry book.Entry, path, archetype string) (int, error) {
	store, err := convo.Open(entry.ID)
	if err != nil {
		return 0, err
	}

	waiting, err := store.Pending()
	if err != nil {
		return 0, err
	}
	if len(waiting) == 0 {
		return 0, nil
	}

	done, s, err := over.To(ctx, entry, node.ALPNSession)
	if err != nil {
		return 0, err
	}
	defer done.Close()
	defer s.Close()

	conn, err := proto.Open(s, path, archetype, 0, "", node.DisplayName())
	if err == nil {
		var stored []string
		if stored, err = chat.Send(conn, waiting); err == nil {
			return len(stored), store.Delivered(stored...)
		}
	}

	// A settled refusal is an answer. Leaving those queued would retry them against a decision on
	// every connection from now on, and go on telling the sender they are on their way. A device
	// answering with one namespace up — a chat, a cast, a dropbox — says no to everything else it
	// will serve again a minute later, and that is not settled.
	if proto.Settled(err) {
		if done := ids(waiting); len(done) > 0 {
			_ = store.Delivered(done...)
		}
	}
	return 0, err
}

// deliver sends into the default chat namespace.
func deliver(ctx context.Context, n *node.Node, lan *discovery.LAN, entry book.Entry) (int, error) {
	return deliverTo(ctx, n, lan, entry, "/chat", "chat")
}

// compose queues a message for a peer without needing the network.
func compose(entry book.Entry, kind byte, body, extra string) (convo.Message, error) {
	store, err := convo.Open(entry.ID)
	if err != nil {
		return convo.Message{}, err
	}

	m, err := convo.New(kind, body, extra)
	if err != nil {
		return convo.Message{}, err
	}
	return m, store.Queue(m)
}

// receiving stores an arriving message and acts on the kinds that ask for it.
func receiving(pinned *book.Book, openLinks bool, show func(node.ID, convo.Message)) func(node.ID, convo.Message) error {
	return func(from node.ID, m convo.Message) error {
		store, err := convo.Open(from)
		if err != nil {
			return err
		}

		fresh, err := store.Add(m)
		if err != nil {
			return err
		}
		// A resend of something already stored is acknowledged again but not acted on twice.
		if !fresh {
			return nil
		}

		if show != nil {
			show(from, m)
		}
		if m.Kind == convo.KindLink && openLinks {
			openInBrowser(m.Body)
		}
		return nil
	}
}

// openInBrowser hands a link to the desktop. Detached, because drop is not the thing that should
// die if a browser does.
func openInBrowser(link string) {
	if !strings.HasPrefix(link, "http://") && !strings.HasPrefix(link, "https://") {
		return
	}

	opener := os.Getenv("DROP_OPENER")
	if opener == "" {
		opener = "xdg-open"
	}
	cmd := exec.Command(opener, link)
	if err := cmd.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "drop: could not open %s: %v\n", link, err)
		return
	}
	go cmd.Wait()
}

// nameFor is what to call a peer in a listing.
func nameFor(pinned *book.Book, id node.ID) string {
	if entry, ok := pinned.ByID(id); ok {
		return entry.Name
	}
	return node.Brief(id)
}

// render prints one message the way a log reads.
func render(who string, m convo.Message) string {
	arrow := "→"
	if m.Dir == convo.In {
		arrow = "←"
	}

	stamp := m.When().Format("15:04")
	switch m.Kind {
	case convo.KindLink:
		return fmt.Sprintf("%s %s %-12s link  %s", stamp, arrow, who, m.Body)
	case convo.KindFile:
		return fmt.Sprintf("%s %s %-12s file  %s (%s)", stamp, arrow, who, m.Body, m.Extra)
	case convo.KindEvent:
		return fmt.Sprintf("%s   %-12s %s", stamp, who, m.Body)
	default:
		return fmt.Sprintf("%s %s %-12s %s", stamp, arrow, who, m.Body)
	}
}

// noteFile records a file changing hands, so `drop log` reads as the whole story.
func noteFile(with node.ID, dir byte, name string, size int64) {
	store, err := convo.Open(with)
	if err != nil {
		return
	}
	_ = store.Note(convo.KindFile, dir, name, bytes(size))
}

// kindName is what a config sees a message kind as.
func kindName(kind byte) string {
	switch kind {
	case convo.KindLink:
		return "link"
	case convo.KindFile:
		return "file"
	case convo.KindEvent:
		return "event"
	default:
		return "text"
	}
}

// ids is what a batch of messages is called, for taking them off the queue.
func ids(all []convo.Message) []string {
	out := make([]string, 0, len(all))
	for _, m := range all {
		out = append(out, m.ID)
	}
	return out
}
