package cmd

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/bresilla/drop/src/pkg/arch"
	"github.com/bresilla/drop/src/pkg/book"
	"github.com/bresilla/drop/src/pkg/node"
	"github.com/bresilla/drop/src/pkg/ns"
)

// SharePath is where a dropbox is served, so a sender opens `<peer>/share`.
const SharePath = "/share"

func newShareCmd() *cobra.Command {
	var to []string

	cmd := &cobra.Command{
		Use:   "share [dir]",
		Short: "Open a dropbox for as long as you are waiting for something",
		Long: "share asks the node already running here to put a dropbox up at\n" +
			"<this device>/share, prints where to send to and who may, and takes it down\n" +
			"again when a transfer has come through it.\n\n" +
			"Nothing is written to the config. The path exists while this command does, and\n" +
			"it takes one transfer: when that is over, so is the path.\n\n" +
			"With no directory it takes things into the one you are in.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := ""
			if len(args) == 1 {
				dir = args[0]
			}
			return runShare(cmd.Context(), dir, to)
		},
	}

	cmd.Flags().StringSliceVar(&to, "to", nil, "who may send, by the name they are filed under; any paired device by default")

	return cmd
}

func runShare(parent context.Context, dir string, to []string) error {
	dir, err := dropboxDir(dir)
	if err != nil {
		return err
	}

	// Through the node that is already running, and only that one. A second endpoint on this
	// identity is not reachable at the address everybody has written down, so a dropbox it served
	// would be one nobody could find.
	path, err := castSocket()
	if err != nil {
		return err
	}
	conn, err := net.Dial("unix", path)
	if err != nil {
		return fmt.Errorf("nothing is serving on this device: start `drop serve` first")
	}
	defer conn.Close()

	ctx, stop := signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
	defer stop()

	if _, err := fmt.Fprintf(conn, "share %s %s\n", whoLine(to), dir); err != nil {
		return err
	}

	reading := bufio.NewReader(conn)
	said, err := reading.ReadString('\n')
	if err != nil {
		return fmt.Errorf("asking this node for a dropbox: %w", err)
	}
	if what, why, _ := strings.Cut(strings.TrimSpace(said), " "); what != "ok" {
		return fmt.Errorf("this node will not open a dropbox: %s", why)
	}

	id, err := node.LocalID()
	if err != nil {
		return err
	}
	whereToSend(dir, to, id)

	// One more line when the transfer is over. Going away instead is what takes the path down when
	// somebody presses ctrl-c: the daemon holds the mount for exactly as long as this connection.
	over := make(chan string, 1)
	go func() {
		line, _ := reading.ReadString('\n')
		over <- strings.TrimSpace(line)
	}()

	select {
	case <-ctx.Done():
		fmt.Printf("\nthe dropbox at %s is closed\n", SharePath)
		return nil
	case line := <-over:
		if line != "done" {
			return fmt.Errorf("the node holding the dropbox stopped")
		}
		fmt.Printf("\na transfer finished; the dropbox at %s is closed\n", SharePath)
		return nil
	}
}

// dropboxDir is where a dropbox takes things: the directory named, or the one you are in.
func dropboxDir(dir string) (string, error) {
	if dir == "" {
		here, err := os.Getwd()
		if err != nil {
			return "", fmt.Errorf("finding the current directory: %w", err)
		}
		dir = here
	}

	full, err := filepath.Abs(dir)
	if err != nil {
		return "", fmt.Errorf("resolving %s: %w", dir, err)
	}
	stat, err := os.Stat(full)
	if err != nil {
		return "", fmt.Errorf("opening %s: %w", dir, err)
	}
	if !stat.IsDir() {
		return "", fmt.Errorf("%s is not a directory", full)
	}
	return full, nil
}

// whereToSend says where to send and who may.
func whereToSend(dir string, to []string, id node.ID) {
	fmt.Printf("\n%s%s  →  %s\n\n", node.DisplayName(), SharePath, dir)
	fmt.Printf("  drop to %s%s <file>...\n\n", node.Brief(id), SharePath)

	who := mayReach(to)
	if len(who) == 0 {
		fmt.Println("  nobody can reach it: no device is paired with this one yet")
	}
	for _, name := range who {
		fmt.Printf("  %s may send\n", name)
	}

	fmt.Println("\nwaiting; ctrl-c to stop")
}

// mayReach is who the dropbox is open to, by name, so it can be read rather than reasoned about.
func mayReach(to []string) []string {
	if len(to) > 0 {
		return to
	}

	pinned, err := book.Load()
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(pinned.Paired()))
	for _, entry := range pinned.Paired() {
		out = append(out, entry.Name)
	}
	return out
}

// whoLine puts the names on one field of the line the daemon reads, and a dash for nobody named.
func whoLine(to []string) string {
	if len(to) == 0 {
		return "-"
	}
	return strings.Join(to, ",")
}

// sendersNamed reads the field saying who may send. A dash is nobody named, and then the rule is
// any paired device.
func sendersNamed(who string) []string {
	if who == "" || who == "-" {
		return nil
	}
	return strings.Split(who, ",")
}

// shareMount is where a dropbox is served: a share namespace over the directory that was named.
//
// The rule is any paired device unless somebody said otherwise. Pairing is the widest rule there
// is, which for a dropbox is the point: it is up for one transfer that its owner is standing there
// waiting for, and narrowing it to trust would mean a laptop paired this morning cannot send you
// the file you asked it for. --to is how it gets narrowed when that matters.
func shareMount(known *arch.Registry, dir string, to []string) (ns.Mount, error) {
	m := ns.Mount{Path: SharePath, Archetype: "share", Access: ns.Access{AnyPaired: true}}
	if len(to) > 0 {
		m.Access = ns.Access{Named: to}
	}

	answers, ok := known.Lookup(m.Archetype, 0)
	if !ok {
		return ns.Mount{}, known.Missing(m.Archetype, 0)
	}
	cfg, err := answers.Read(saying{"dir": dir})
	if err != nil {
		return ns.Mount{}, err
	}
	m.Config = cfg
	return m, nil
}

// saying is a declaration made here rather than read out of a config, for a namespace drop puts up
// itself.
type saying map[string]string

func (s saying) String(key string) (string, bool) {
	value, ok := s[key]
	return value, ok
}

func (s saying) Bool(key string) (bool, bool) {
	value, ok := s[key]
	return value == "true", ok
}

func (saying) Strings(string) ([]string, bool) { return nil, false }
