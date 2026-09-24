package cmd

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/spf13/cobra"

	"github.com/bresilla/drop/src/pkg/node"
	"github.com/bresilla/drop/src/pkg/tui"
)

// An open interface can be driven from another terminal on the same machine: shown, and pressed
// keys into. Somebody watching one screen while something else works the other, or one interface
// on each of two machines worked from a third, is how two devices get walked through pairing and
// talking without anybody reaching over to type.
//
// The socket is this account's alone, in a directory only it can read, the way a terminal
// multiplexer's is. Anything able to open it could already run drop as this account.
//
// One line asks: "who", "show", "keys <key>…" or "type <text>".

// steerPrefix names an interface's socket, followed by its process id.
const steerPrefix = "tui-"

// steer listens for whatever wants to drive this interface, until what it returns is called.
func steer(ctx context.Context, program *tea.Program, shown *tui.Screen) (func(), error) {
	dir, err := localDir()
	if err != nil {
		return nil, err
	}
	path := filepath.Join(dir, steerPrefix+strconv.Itoa(os.Getpid())+".sock")

	_ = os.Remove(path)
	var listen net.ListenConfig
	listening, err := listen.Listen(ctx, "unix", path)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = listening.Close()
		_ = os.Remove(path)
		return nil, fmt.Errorf("protecting %s: %w", path, err)
	}

	go func() {
		for {
			conn, err := listening.Accept()
			if err != nil {
				return
			}
			go func() {
				defer func() { _ = conn.Close() }()
				_ = steered(conn, program, shown)
			}()
		}
	}()

	return func() {
		_ = listening.Close()
		_ = os.Remove(path)
	}, nil
}

// steered answers one request.
func steered(conn net.Conn, program *tea.Program, shown *tui.Screen) error {
	if err := conn.SetReadDeadline(time.Now().Add(localHelloWithin)); err != nil {
		return err
	}
	line, err := readLocalLine(bufio.NewReader(conn))
	if err != nil {
		return err
	}
	what, rest, _ := strings.Cut(strings.TrimRight(line, "\r\n"), " ")

	switch what {
	case "who":
		brief := ""
		if id, err := node.LocalID(); err == nil {
			brief = node.Brief(id)
		}
		return writeLocal(conn, "ok\t%s\t%s\t%s\t%s\n", node.DisplayName(), brief, os.Getenv("DROP_PROFILE"), trail(shown.Now()))

	case "show":
		drawn := shown.Now()
		if rest != "color" {
			drawn = plainScreen(drawn)
		}
		return writeLocal(conn, "ok %d\n%s", len(drawn), drawn)

	case "keys":
		var keys []tea.KeyMsg
		for _, name := range strings.Fields(rest) {
			k, err := tui.Key(name)
			if err != nil {
				return writeLocal(conn, "no %v\n", err)
			}
			keys = append(keys, k)
		}
		for _, k := range keys {
			program.Send(k)
		}
		return writeLocal(conn, "ok\n")

	case "type":
		for _, k := range tui.Typed(rest) {
			program.Send(k)
		}
		return writeLocal(conn, "ok\n")
	}
	return writeLocal(conn, "no %q is not something an interface does\n", what)
}

// plainScreen is a screen as text: no escapes, and no spaces trailing off the end of each line.
func plainScreen(drawn string) string {
	lines := strings.Split(ansi.Strip(drawn), "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, " ")
	}
	return strings.TrimRight(strings.Join(lines, "\n"), "\n") + "\n"
}

// trail is the trail an interface's header shows: which screen it is on.
func trail(drawn string) string {
	first, _, _ := strings.Cut(plainScreen(drawn), "\n")
	return strings.Join(strings.Fields(first), " ")
}

// steering is one open interface, as another terminal sees it.
type steering struct {
	path    string
	pid     string
	name    string
	brief   string
	profile string
	at      string
}

// interfaces is every interface open on this machine. A socket nobody answers on is one whose
// interface died without cleaning up, and it is cleared away.
func interfaces(ctx context.Context) ([]steering, error) {
	dir, err := localDir()
	if err != nil {
		return nil, err
	}
	found, err := filepath.Glob(filepath.Join(dir, steerPrefix+"*.sock"))
	if err != nil {
		return nil, err
	}

	var out []steering
	for _, path := range found {
		said, err := askSteer(ctx, path, "who")
		if err != nil {
			if errors.Is(err, errNotAnswering) {
				_ = os.Remove(path)
			}
			continue
		}
		fields := strings.Split(strings.TrimRight(said, "\n"), "\t")
		for len(fields) < 5 {
			fields = append(fields, "")
		}
		pid := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(path), steerPrefix), ".sock")
		out = append(out, steering{path: path, pid: pid, name: fields[1], brief: fields[2], profile: fields[3], at: fields[4]})
	}
	return out, nil
}

var errNotAnswering = errors.New("nothing is answering there")

// askSteer sends one request and reads everything the interface says back.
func askSteer(ctx context.Context, path, request string) (string, error) {
	conn, err := dialLocal(ctx, path)
	if err != nil {
		return "", errNotAnswering
	}
	defer func() { _ = conn.Close() }()

	if err := writeLocal(conn, "%s\n", request); err != nil {
		return "", err
	}
	if err := conn.SetReadDeadline(time.Now().Add(localHelloWithin)); err != nil {
		return "", err
	}
	reading := bufio.NewReader(conn)
	first, err := readLocalLine(reading)
	if err != nil {
		return "", err
	}

	what, rest, _ := strings.Cut(strings.TrimRight(first, "\n"), " ")
	switch {
	case what == "no":
		return "", errors.New(rest)
	case strings.HasPrefix(first, "ok\t"):
		return first, nil
	case what == "ok" && rest != "":
		size, err := strconv.Atoi(rest)
		if err != nil || size < 0 || size > maxLocalLine {
			return "", fmt.Errorf("the interface answered %q", first)
		}
		body := make([]byte, size)
		if _, err := io.ReadFull(reading, body); err != nil {
			return "", err
		}
		return string(body), nil
	case what == "ok":
		return "", nil
	}
	return "", fmt.Errorf("the interface answered %q", first)
}

// pick is the interface a command means: the one named, by process, device name, profile or id,
// or the only one there is.
func pick(ctx context.Context, to string) (steering, error) {
	all, err := interfaces(ctx)
	if err != nil {
		return steering{}, err
	}
	if len(all) == 0 {
		return steering{}, errors.New("no interface is open on this machine: run `drop` in a terminal first")
	}

	var matched []steering
	for _, one := range all {
		if to == "" || to == one.pid || to == one.name || to == one.profile || to == one.brief {
			matched = append(matched, one)
		}
	}
	switch len(matched) {
	case 1:
		return matched[0], nil
	case 0:
		return steering{}, fmt.Errorf("no open interface is %q; `drop tui ls` says which are", to)
	}

	var names []string
	for _, one := range matched {
		names = append(names, one.pid+" ("+one.name+")")
	}
	return steering{}, fmt.Errorf("%d interfaces are open — say which with --to: %s", len(matched), strings.Join(names, ", "))
}

func newTUICmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "tui",
		Short: "Drive an interface open in another terminal",
		Long: "An interface open in a terminal can be looked at and typed into from another one on the same\n" +
			"machine — or from anywhere that can run a command on it, over ssh. `ls` says which are\n" +
			"open, `show` prints what one is showing, and `keys` and `type` press keys in it.\n\n" +
			"With more than one open, --to picks one: by process id, device name, profile or id.",
	}
	cmd.AddCommand(newTUILsCmd(), newTUIShowCmd(), newTUIKeysCmd(), newTUITypeCmd())
	return cmd
}

func newTUILsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "ls",
		Short: "The interfaces open on this machine",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			all, err := interfaces(cmd.Context())
			if err != nil {
				return err
			}
			if len(all) == 0 {
				fmt.Println("no interface is open on this machine")
				return nil
			}
			for _, one := range all {
				profile := one.profile
				if profile == "" {
					profile = "-"
				}
				fmt.Printf("  %-8s %-20s %-13s %-10s %s\n", one.pid, one.name, one.brief, profile, one.at)
			}
			return nil
		},
	}
}

func newTUIShowCmd() *cobra.Command {
	var (
		to    string
		color bool
	)
	cmd := &cobra.Command{
		Use:   "show",
		Short: "Print what an interface is showing",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return show(cmd.Context(), to, color)
		},
	}
	cmd.Flags().StringVar(&to, "to", "", "which interface, when more than one is open")
	cmd.Flags().BoolVar(&color, "color", false, "keep the colours")
	return cmd
}

func show(ctx context.Context, to string, color bool) error {
	one, err := pick(ctx, to)
	if err != nil {
		return err
	}
	request := "show"
	if color {
		request = "show color"
	}
	drawn, err := askSteer(ctx, one.path, request)
	if err != nil {
		return err
	}
	fmt.Print(drawn)
	return nil
}

// settle is how long a key is given to take effect before the screen is shown after it.
const settle = 400 * time.Millisecond

func newTUIKeysCmd() *cobra.Command {
	var (
		to      string
		showing bool
	)
	cmd := &cobra.Command{
		Use:   "keys <key>...",
		Short: "Press keys in an interface: enter, esc, up, down, tab, ctrl+], or any one character",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return press(cmd.Context(), to, "keys "+strings.Join(args, " "), showing)
		},
	}
	cmd.Flags().StringVar(&to, "to", "", "which interface, when more than one is open")
	cmd.Flags().BoolVarP(&showing, "show", "s", false, "print the screen once the keys have landed")
	return cmd
}

func newTUITypeCmd() *cobra.Command {
	var (
		to      string
		showing bool
	)
	cmd := &cobra.Command{
		Use:   "type <text>...",
		Short: "Type text into an interface, a key at a time",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return press(cmd.Context(), to, "type "+strings.Join(args, " "), showing)
		},
	}
	cmd.Flags().StringVar(&to, "to", "", "which interface, when more than one is open")
	cmd.Flags().BoolVarP(&showing, "show", "s", false, "print the screen once the text has landed")
	return cmd
}

func press(ctx context.Context, to, request string, showing bool) error {
	one, err := pick(ctx, to)
	if err != nil {
		return err
	}
	if _, err := askSteer(ctx, one.path, request); err != nil {
		return err
	}
	if !showing {
		return nil
	}
	time.Sleep(settle)
	return show(ctx, one.pid, false)
}
