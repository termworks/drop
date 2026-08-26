package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/spf13/cobra"
	"github.com/tmc/go-iroh/iroh"

	"github.com/bresilla/drop/src/pkg/arch"
	"github.com/bresilla/drop/src/pkg/asciicast"
	"github.com/bresilla/drop/src/pkg/book"
	"github.com/bresilla/drop/src/pkg/cast"
	"github.com/bresilla/drop/src/pkg/discovery"
	"github.com/bresilla/drop/src/pkg/node"
	"github.com/bresilla/drop/src/pkg/ns"
	"github.com/bresilla/drop/src/pkg/proto"
)

// CastPath is where a cast is served, so a watcher opens `<peer>/cast`.
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
	defer n.Close()

	pinned, err := book.Load()
	if err != nil {
		return err
	}

	stage := cast.New(uint16(head.Width), uint16(head.Height))
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

	go serveLoop(ctx, n, map[string]func(node.ID, *iroh.Stream){
		node.ALPNSession: func(from node.ID, s *iroh.Stream) {
			defer s.Close()
			_ = proto.Handle(ctx, s, from, proto.Policy{
				Mounts:     castMounts(known),
				Archetypes: known,
				Allow:      accepting(pinned, false),
				Who:        whoIs(pinned),
			})
		},
		node.ALPNHello: func(from node.ID, s *iroh.Stream) {
			defer s.Close()
			_ = proto.AnswerHello(s, from, func(proto.Badged) proto.Hello {
				return proto.Hello{Name: node.DisplayName(), Version: version}
			})
		},
	})

	// The address goes to a file as well as to stdout: hexe starts this detached and reads the
	// file, having no pipe to read a reply on.
	address := n.ID().String()
	if err := publishAddress(addressFile, address); err != nil {
		fmt.Fprintf(os.Stderr, "drop: %v\n", err)
	}
	defer os.Remove(addressFile)

	fmt.Println(address)
	fmt.Fprintf(os.Stderr, "drop: casting %dx%d; watch with `drop to %s%s`\n",
		head.Width, head.Height, node.Brief(n.ID()), CastPath)

	return pump(ctx, reader, stage)
}

// pump turns the cast into what watchers see, and stops when whoever started it asks.
func pump(ctx context.Context, reader *asciicast.Reader, stage *cast.Caster) error {
	events := reads(reader)

	for {
		var next read
		select {
		case <-ctx.Done():
			return nil
		case next = <-events:
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

// reads takes the recording apart on a goroutine of its own.
//
// A read of standard input cannot be cancelled: it ends when whatever is writing stops. On the
// reading goroutine that is fine, because the one waiting on this channel can be told to stop by a
// signal without waiting for a line that may never come.
func reads(reader *asciicast.Reader) <-chan read {
	out := make(chan read, 1)

	go func() {
		for {
			event, err := reader.Next()
			out <- read{event: event, err: err}
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
	m := ns.Mount{Path: CastPath, Archetype: "tty", Access: ns.Access{AnyPaired: true}}
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
func publishAddress(path, address string) error {
	if path == "" {
		return nil
	}
	if err := os.WriteFile(path, []byte(address), 0o600); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return nil
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

	conn, err := net.Dial("unix", path)
	if err != nil {
		return errNoDaemon
	}
	defer conn.Close()

	id, err := node.LocalID()
	if err != nil {
		return err
	}

	address := id.String()
	if err := publishAddress(addressFile, address); err != nil {
		fmt.Fprintf(os.Stderr, "drop: %v\n", err)
	}
	defer os.Remove(addressFile)

	// The first line says what this connection is for; the rest is the recording.
	if _, err := io.WriteString(conn, "cast\n"); err != nil {
		return err
	}

	fmt.Println(address)
	fmt.Fprintf(os.Stderr, "drop: casting through this node; watch with `drop to %s%s`\n",
		node.Brief(id), CastPath)

	// Closed when standard input runs out, which is what tells the daemon the cast is over.
	done := make(chan error, 1)
	go func() {
		_, err := io.Copy(conn, os.Stdin)
		if closer, ok := conn.(interface{ CloseWrite() error }); ok {
			_ = closer.CloseWrite()
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
