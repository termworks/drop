package cmd

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/tmc/go-iroh/iroh"
	"golang.org/x/term"

	"github.com/bresilla/drop/src/pkg/arch/note"
	"github.com/bresilla/drop/src/pkg/arch/share"
	"github.com/bresilla/drop/src/pkg/book"
	"github.com/bresilla/drop/src/pkg/convo"
	"github.com/bresilla/drop/src/pkg/live"
	"github.com/bresilla/drop/src/pkg/node"
	"github.com/bresilla/drop/src/pkg/proto"
)

// What each kind of namespace does when it is opened from a terminal.

// openChat sends a line and exits, or opens the window when nothing was said.
func openChat(ctx context.Context, o opening) error {
	if len(o.args) > 0 {
		return sendMessage(ctx, o, "chat", convo.KindText, strings.Join(o.args, " "))
	}
	return talkTo(ctx, o)
}

// openLink hands a URL to the far end, which decides what to do with it.
func openLink(ctx context.Context, o opening) error {
	if len(o.args) == 0 {
		return fmt.Errorf("%s takes a link: write one after the address", o.where())
	}
	return sendMessage(ctx, o, "link", convo.KindLink, o.args[0])
}

// openShare sends the files named after the address.
func openShare(ctx context.Context, o opening) error {
	if len(o.args) == 0 {
		return fmt.Errorf("%s takes files: name them after the address, or - for standard input", o.where())
	}

	sources, err := gather(o.args, o.stdinName)
	if err != nil {
		return err
	}
	return sendFiles(ctx, o, sources)
}

// openTerminal attaches to a terminal somebody is serving.
func openTerminal(ctx context.Context, o opening) error { return readLive(ctx, o, true) }

// openStream follows what a namespace is writing.
func openStream(ctx context.Context, o opening) error { return readLive(ctx, o, false) }

// openNote prints the note as the far end has it.
//
// Reading and no more: a note is changed by holding it, editing the file it keeps here and letting
// the two machines meet, and `drop path join` is how this machine comes to hold one.
func openNote(ctx context.Context, o opening) error {
	find, cancel := o.within(ctx)
	defer cancel()

	done, s, err := o.over().To(find, o.entry, node.ALPNSession)
	if err != nil {
		return err
	}
	defer done.Close()
	defer s.Close()

	conn, err := proto.Open(s, o.served.Path, "note", 0, "", node.DisplayName())
	if err != nil {
		return err
	}

	body, err := note.Text(conn)
	if err != nil {
		return err
	}
	_, err = os.Stdout.Write(body)
	return err
}

// openFiles lists the directory and says what walks it from there.
func openFiles(ctx context.Context, o opening) error {
	find, cancel := o.within(ctx)
	defer cancel()

	b, done, err := browse(find, o.node, o.lan, o.entry, o.served.Path)
	if err != nil {
		return err
	}
	defer done()

	if err := listInside(b, o.entry.ID, o.at.String(), o.rest); err != nil {
		return err
	}

	fmt.Printf("  drop file get %s/<name>\n", o.at)
	if b.Writable() {
		fmt.Printf("  drop file put %s <file>...\n", o.at)
	}
	fmt.Println()
	return nil
}

// sendFiles opens a namespace that takes files and pushes them into it.
func sendFiles(parent context.Context, o opening, sources []share.Source) error {
	ctx, cancel := o.within(parent)
	defer cancel()

	done, s, err := o.over().To(ctx, o.entry, node.ALPNSession)
	if err != nil {
		return err
	}
	defer done.Close()
	defer s.Close()

	bar := &progress{}
	defer bar.clear()

	conn, err := proto.Open(s, o.served.Path, "share", 0, "", node.DisplayName())
	if err != nil {
		return err
	}
	if err := share.Send(conn, sources, bar.update); err != nil {
		return err
	}
	for _, src := range sources {
		noteFile(o.entry.ID, convo.Out, src.Name, src.Size)
	}

	fmt.Printf("\nsent %d item(s) to %s\n", len(sources), o.where())
	return nil
}

// sendMessage writes something down for the far end and tries to hand it over now.
func sendMessage(parent context.Context, o opening, archetype string, kind byte, body string) error {
	if strings.TrimSpace(body) == "" {
		return fmt.Errorf("nothing to send")
	}

	m, err := compose(o.entry, kind, body, "")
	if err != nil {
		return err
	}
	fmt.Println(render(o.entry.Name, m))

	ctx, cancel := o.within(parent)
	defer cancel()

	sent, err := deliverTo(ctx, o.node, o.lan, o.entry, o.served.Path, archetype)
	if proto.Settled(err) {
		// An answer, not a device that is off. Queueing it would mean retrying forever against a
		// decision somebody made, and telling the sender their message is on its way.
		return err
	}
	if err != nil {
		// Queued is not lost. A device that is off is the normal case, so this says where the
		// message is rather than only what went wrong.
		fmt.Printf("queued for %s: %v\n", o.entry.Name, err)
		return nil
	}
	if sent > 0 {
		fmt.Println("delivered")
	}
	return nil
}

// readLive opens a namespace that is a running stream and writes what arrives to this terminal.
func readLive(parent context.Context, o opening, raw bool) error {
	find, cancel := o.within(parent)
	defer cancel()

	over, s, err := o.over().To(find, o.entry, node.ALPNSession)
	if err != nil {
		return err
	}
	defer over.Close()

	conn, err := proto.Open(s, o.served.Path, o.served.Archetype, 0, "", node.DisplayName())
	if err != nil {
		return err
	}
	d := live.New(conn, s)

	// Raw mode and a size only make sense for a terminal, but what is typed goes over either way:
	// a pipe is how a script drives a shell on another machine, and refusing its input made this
	// usable only by hand.
	local := int(os.Stdin.Fd())
	if raw && term.IsTerminal(local) {
		state, err := term.MakeRaw(local)
		if err == nil {
			defer term.Restore(local, state)
		}
		if w, h, err := term.GetSize(local); err == nil {
			_ = d.Resize(uint16(w), uint16(h))
		}
	}

	// Closed when standard input runs out, so a piped-in script ends the far side's shell rather
	// than leaving it waiting for a line that is never coming.
	go func() {
		_, _ = io.Copy(d, os.Stdin)
		_ = d.Close()
	}()

	fmt.Fprintf(os.Stderr, "drop: reading %s; ctrl-c to stop\r\n", o.where())

	done := make(chan error, 1)
	go func() { done <- d.Pump(os.Stdout) }()

	select {
	case <-parent.Done():
		return nil
	case err := <-done:
		if streamOver(err) {
			return nil
		}
		return err
	}
}

// talkTo is the chat window: lines typed go out, and what arrives is printed.
//
// It listens while you type, because the far end can start a session at any moment and a chat that
// only receives when it is sending is not a chat.
func talkTo(ctx context.Context, o opening) error {
	pinned, err := book.Load()
	if err != nil {
		return err
	}

	store, err := convo.Open(o.entry.ID)
	if err != nil {
		return err
	}
	history, err := store.History()
	if err != nil {
		return err
	}
	for _, m := range history[max(0, len(history)-shownOnOpening):] {
		fmt.Println(render(o.entry.Name, m))
	}

	doing := &doings{
		pinned: pinned,
		said: func(from node.ID, m convo.Message) {
			fmt.Printf("\r%s\n", render(nameFor(pinned, from), m))
		},
	}
	known := doing.talking()
	mounts := chatMounts(known)

	policy := proto.Policy{
		Mounts:     mounts,
		Archetypes: known,
		Allow:      accepting(pinned, false),
		Who:        whoIs(pinned),
	}
	go serveLoop(ctx, o.node, map[string]func(node.ID, *iroh.Stream){
		node.ALPNSession: func(from node.ID, s *iroh.Stream) {
			defer s.Close()
			_ = proto.Handle(ctx, s, from, policy)
		},
		node.ALPNHello: func(from node.ID, s *iroh.Stream) {
			defer s.Close()
			_ = proto.AnswerHello(s, from, func(badge proto.Badged) proto.Hello {
				return greeting(pinned, mounts, known, from, badge)
			})
		},
	})

	fmt.Printf("\ntalking to %s; ctrl-c or ctrl-d to stop\n\n", o.entry.Name)

	go flushLoop(ctx, o)

	lines := make(chan string)
	go func() {
		defer close(lines)
		scan := bufio.NewScanner(os.Stdin)
		scan.Buffer(make([]byte, 0, 64<<10), convo.MaxBody)
		for scan.Scan() {
			lines <- scan.Text()
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return nil
		case line, ok := <-lines:
			if !ok {
				return nil
			}
			text := strings.TrimSpace(line)
			if text == "" {
				continue
			}
			if _, err := compose(o.entry, convo.KindText, text, ""); err != nil {
				fmt.Fprintf(os.Stderr, "drop: %v\n", err)
				continue
			}
			// Sent in the background so a slow or absent far end does not stop the typing.
			go func() {
				_, err := deliverTo(ctx, o.node, o.lan, o.entry, o.served.Path, "chat")
				switch {
				case err == nil:
				case proto.Settled(err):
					fmt.Printf("  (not delivered: %v)\n", err)
				default:
					fmt.Printf("  (queued: %v)\n", err)
				}
			}()
		}
	}
}

// shownOnOpening is how much of a conversation a window opens on.
const shownOnOpening = 20

// flushEvery is how often a chat retries whatever is still queued.
const flushEvery = 15 * time.Second

// flushLoop keeps trying whatever is still queued, so a device coming back gets the backlog without
// anyone typing again.
func flushLoop(ctx context.Context, o opening) {
	tick := time.NewTicker(flushEvery)
	defer tick.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			_, _ = deliverTo(ctx, o.node, o.lan, o.entry, o.served.Path, "chat")
		}
	}
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
