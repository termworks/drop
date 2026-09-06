package cmd

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"github.com/tmc/go-iroh/iroh"

	"github.com/bresilla/drop/src/pkg/arch"
	"github.com/bresilla/drop/src/pkg/asciicast"
	"github.com/bresilla/drop/src/pkg/book"
	"github.com/bresilla/drop/src/pkg/cast"
	"github.com/bresilla/drop/src/pkg/discovery"
	"github.com/bresilla/drop/src/pkg/keep"
	"github.com/bresilla/drop/src/pkg/node"
	"github.com/bresilla/drop/src/pkg/ns"
	"github.com/bresilla/drop/src/pkg/proto"
)

// CastPath is where a cast is served, so a watcher connects to `<machine>:/cast`.
const CastPath = "/cast"

func newCastCmd() *cobra.Command {
	var addressFile string

	cmd := &cobra.Command{
		Use:   "cast",
		Short: "Serve a terminal read from standard input as asciicast",
		Long: "cast reads asciicast v2 on standard input and serves it to paired devices at\n" +
			"<this node>/cast. It is the shape a hexe stream backend is handed a pane in:\n\n" +
			"  HEXE_SHARE_BACKEND=\"drop cast\" hexe ...\n\n" +
			"Nothing about it is hexe-specific: anything writing asciicast will do, including\n" +
			"`asciinema rec --stdout`.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCast(cmd.Context(), addressFile)
		},
	}

	cmd.Flags().StringVar(&addressFile, "address-file", defaultAddressFile(),
		"where to write the address watchers need; hexe reads this")

	return cmd
}

// defaultAddressFile is where hexe's share plugin looks for the address a backend published.
func defaultAddressFile() string {
	dir := os.Getenv("XDG_RUNTIME_DIR")
	if dir == "" {
		dir = "/tmp"
	}
	return filepath.Join(dir, "hexe-share-address")
}

func runCast(parent context.Context, addressFile string) error {
	// Through the node that is already running, when there is one: two listeners on one identity
	// means a watcher dialling the address it has can reach the wrong one.
	if err := castThroughDaemon(parent, addressFile); err == nil {
		return nil
	} else if !errors.Is(err, errNoDaemon) {
		return err
	}

	reader, head, err := asciicast.NewReader(os.Stdin)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
	defer stop()

	// The first interrupt ends the cast; the second one is the one the system handles, so somebody
	// who presses it twice is not held by a teardown that is taking its time.
	go func() {
		<-ctx.Done()
		stop()
	}()

	n, err := node.Start(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = n.Close() }()

	pinned, err := book.Load()
	if err != nil {
		return err
	}

	stage := cast.New(head.Width, head.Height)
	defer stage.Stop()

	doing := &doings{
		pinned: pinned,
		notes:  func(text string) { fmt.Fprintf(os.Stderr, "drop: %s\n", text) },
		shown: func(path string) (*cast.Caster, bool) {
			return stage, path == CastPath
		},
	}
	known := doing.watching()
	defer doing.stop()

	if _, err := discovery.StartLAN(ctx, n); err != nil {
		fmt.Fprintf(os.Stderr, "drop: local discovery unavailable: %v\n", err)
	}

	mounts := castMounts(known)
	go serveLoop(ctx, n, map[string]func(node.ID, *iroh.Stream){
		node.ALPNSession: func(from node.ID, s *iroh.Stream) {
			defer func() { _ = s.Close() }()
			_ = proto.Handle(ctx, s, from, proto.Policy{
				Mounts:     mounts,
				Archetypes: known,
				Allow:      accepting(pinned, false),
				Who:        whoIs(pinned),
				Moved:      moving(pinned, func(said string) { log.Printf("%s", said) }),
			})
		},
		node.ALPNHello: func(from node.ID, s *iroh.Stream) {
			defer func() { _ = s.Close() }()
			_ = proto.AnswerHello(s, from, func(badge proto.Badged) proto.Hello {
				return greeting(pinned, mounts, known, from, badge)
			}, moving(pinned, func(said string) { log.Printf("%s", said) }))
		},
	})

	// The address goes to a file as well as to stdout: hexe starts this detached and reads the
	// file, having no pipe to read a reply on.
	address := n.ID().String()
	unpublish, err := publishAddress(addressFile, address)
	if err != nil {
		fmt.Fprintf(os.Stderr, "drop: %v\n", err)
	} else {
		defer unpublish()
	}

	fmt.Println(address)
	fmt.Fprintf(os.Stderr, "drop: casting %dx%d; watch with `drop connect %s:%s`\n",
		head.Width, head.Height, node.Brief(n.ID()), CastPath)

	return pump(ctx, reader, stage)
}

// pump turns the cast into what watchers see, and stops when whoever started it asks.
func pump(ctx context.Context, reader *asciicast.Reader, stage *cast.Caster) error {
	events := reads(ctx, reader)

	for {
		var next read
		var ok bool
		select {
		case <-ctx.Done():
			return nil
		case next, ok = <-events:
			if !ok {
				return nil
			}
		}

		if err := next.err; err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return fmt.Errorf("reading the cast: %w", err)
		}

		switch next.event.Kind {
		case asciicast.Output:
			_, _ = stage.Write([]byte(next.event.Data))

		case asciicast.Resize:
			if cols, rows, ok := asciicast.Size(next.event.Data); ok {
				stage.Resize(cols, rows)
			}

		case asciicast.Marker:
			// The rule a backend must not skip. Everything kept so far may already contain the
			// prompt, so it is thrown away rather than merely paused.
			if next.event.Data == asciicast.PasswordOn {
				stage.Clear()
			}
		}
	}
}

// read is one event off a recording, or why there will not be another.
type read struct {
	event asciicast.Event
	err   error
}

// reads emits recording events until input ends or cancellation follows a completed read.
func reads(ctx context.Context, reader *asciicast.Reader) <-chan read {
	out := make(chan read, 1)

	go func() {
		defer close(out)
		for {
			event, err := reader.Next()
			select {
			case out <- read{event: event, err: err}:
			case <-ctx.Done():
				return
			}
			if err != nil {
				return
			}
		}
	}()

	return out
}

// castMounts is the one namespace a cast serves.
//
// Open to any paired device, and said so rather than left out: access is denied unless a rule
// grants it, and a mount with no rule is one nobody can ever watch.
func castMounts(known *arch.Registry) *ns.Table {
	table := ns.NewTable()
	_ = table.Add(castMount(known))
	return table
}

// castMount is where a cast is served: a terminal that takes no input, because a cast is somebody's
// screen and typing into it is a different grant.
func castMount(known *arch.Registry) ns.Mount {
	m := ns.Mount{Path: CastPath, Source: ns.Held, Archetype: "tty", Access: ns.Access{AnyPaired: true}}
	if answers, ok := known.Lookup(m.Archetype, 0); ok {
		m.Config, _ = answers.Read(nothing{})
	}
	return m
}

// nothing is a declaration that says nothing, for a namespace drop puts up itself.
type nothing struct{}

func (nothing) String(string) (string, bool)    { return "", false }
func (nothing) Bool(string) (bool, bool)        { return false, false }
func (nothing) Strings(string) ([]string, bool) { return nil, false }

// publishAddress writes the address where hexe will look for it.
func publishAddress(path, address string) (func(), error) {
	if path == "" {
		return func() {}, nil
	}
	if err := keep.Replace(path, []byte(address)); err != nil {
		return nil, fmt.Errorf("writing %s: %w", path, err)
	}
	published, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("looking at %s: %w", path, err)
	}
	return func() {
		current, err := os.Lstat(path)
		if err == nil && os.SameFile(published, current) {
			_ = os.Remove(path)
		}
	}, nil
}

// errNoDaemon says there is nothing listening locally, so a cast has to be its own node.
var errNoDaemon = errors.New("no daemon is running")

// castThroughDaemon hands standard input to the running node and lets it do the serving.
//
// The bytes go over untouched: what arrives is asciicast, and the daemon reads it exactly as this
// command would have. Nothing here interprets it, so there is one parser rather than two that can
// come to disagree.
func castThroughDaemon(ctx context.Context, addressFile string) error {
	path, err := castSocket()
	if err != nil {
		return errNoDaemon
	}

	conn, err := dialLocal(ctx, path)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errNoDaemon
	}
	defer func() { _ = conn.Close() }()

	id, err := node.LocalID()
	if err != nil {
		return err
	}

	input := bufio.NewReader(os.Stdin)
	header, err := readLocalLine(input)
	if err != nil {
		return fmt.Errorf("reading the cast header: %w", err)
	}

	if _, err := io.WriteString(conn, "cast\n"); err != nil {
		return err
	}
	if _, err := io.WriteString(conn, header); err != nil {
		return err
	}

	replies := bufio.NewReader(conn)
	answer, err := readLocalReply(conn, replies)
	if err != nil {
		return fmt.Errorf("asking this node to cast: %w", err)
	}
	if what, why, _ := strings.Cut(strings.TrimSpace(answer), " "); what != "ok" {
		return fmt.Errorf("this node will not cast: %s", why)
	}

	address := id.String()
	unpublish, err := publishAddress(addressFile, address)
	if err != nil {
		fmt.Fprintf(os.Stderr, "drop: %v\n", err)
	} else {
		defer unpublish()
	}

	fmt.Println(address)
	fmt.Fprintf(os.Stderr, "drop: casting through this node; watch with `drop connect %s:%s`\n",
		node.Brief(id), CastPath)

	// Closed when standard input runs out, which is what tells the daemon the cast is over.
	done := make(chan error, 1)
	go func() {
		_, err := io.Copy(conn, input)
		if closer, ok := conn.(interface{ CloseWrite() error }); ok {
			_ = closer.CloseWrite()
		}
		if err == nil {
			_ = conn.SetReadDeadline(time.Now().Add(localHelloWithin))
			line, readErr := readLocalLine(replies)
			if readErr != nil {
				err = readErr
			} else if strings.TrimSpace(line) != "done" {
				err = fmt.Errorf("the node stopped casting without confirming it")
			}
		}
		done <- err
	}()

	select {
	case <-ctx.Done():
		return nil
	case err := <-done:
		return err
	}
}
