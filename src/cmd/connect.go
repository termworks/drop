package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/bresilla/drop/src/pkg/book"
	"github.com/bresilla/drop/src/pkg/discovery"
	"github.com/bresilla/drop/src/pkg/node"
	"github.com/bresilla/drop/src/pkg/ns"
	"github.com/bresilla/drop/src/pkg/proto"
)

// What a namespace means to the command line.
//
// The archetype registry says what a namespace is; this says what opening one from a terminal does,
// and it is the same shape as the table the interface keeps on purpose. A kind of namespace is one
// value written down in one place, rather than a case in a switch that grows every time an
// archetype is added.

// opening is one namespace being opened: what was typed, what is there, and a node to reach it on.
type opening struct {
	// at is the address as written, and entry the machine it resolved to.
	at    ns.Address
	entry book.Entry
	// served is what the far end says is at the path, and rest whatever was left below it.
	served proto.Served
	rest   string
	// args is what followed the address, and stdinName the name standard input is given.
	args      []string
	stdinName string
	// node and lan are already up, and until is when to stop trying to reach the far end.
	node  *node.Node
	lan   *discovery.LAN
	until time.Time
}

// over is how this opening reaches the far end.
func (o opening) over() reaches { return best(o.node, o.lan) }

// within is whatever is left of the time this opening was given to reach the far end.
func (o opening) within(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithDeadline(ctx, o.until)
}

// where is the namespace as it would be typed, which is what a message about it should say.
func (o opening) where() string {
	at := o.at
	at.Path = o.served.Path
	return at.String()
}

// opener is how the command line behaves for one archetype.
type opener struct {
	// takes is the word for what may follow the address, and is empty when nothing may.
	takes string
	// does is the line connect's help gives this kind of namespace.
	does string
	// open runs until whoever asked is finished with it.
	open func(ctx context.Context, at opening) error
}

// openers is which opener answers to which archetype name. Teaching the command line a kind of
// namespace is adding a line here.
var openers = map[string]opener{
	"chat":   {takes: "a message", does: "a message sends it and exits; nothing at all opens the window", open: openChat},
	"tty":    {does: "attaches to the terminal, and what you type is typed there", open: openTerminal},
	"stream": {does: "follows what it is writing until you stop it", open: openStream},
	"files":  {does: "lists the directory; drop file walks it from there", open: openFiles},
	"link":   {takes: "a link", does: "sends a link, and the far end decides what to do with it", open: openLink},
	"note":   {does: "prints the note as it stands there; join it to write in it", open: openNote},
	"share":  {takes: "files", does: "sends the files named after it, and - is standard input", open: openShare},
}

// openerFor is how the command line opens an archetype, and says no for one it has never heard of.
func openerFor(archetype string) (opener, bool) {
	how, ok := openers[archetype]
	return how, ok
}

func newConnectCmd() *cobra.Command {
	var (
		as   string
		wait time.Duration
	)

	cmd := &cobra.Command{
		Use:   "connect <address> [argument...]",
		Short: "Open whatever is at an address",
		Long:  connectLong(),
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runConnect(cmd.Context(), args[0], args[1:], as, wait)
		},
	}

	cmd.Flags().StringVar(&as, "as", "stdin", "the name to give standard input")
	cmd.Flags().DurationVarP(&wait, "wait", "w", 90*time.Second, "how long to spend reaching the machine")

	return cmd
}

// connectLong says how an address is written and what each kind of namespace does when opened.
//
// The second half is the registry read back, so a kind of namespace this build can open is one the
// help mentions and one it cannot is not.
func connectLong() string {
	long := "An address is whose machine, which machine, and what on it:\n\n" +
		"  drop connect bob:laptop:/chat   bob's laptop, its /chat\n" +
		"  drop connect laptop:/chat       the machine called laptop\n" +
		"  drop connect bob::/chat         bob, whichever machine of his answers\n\n" +
		"What happens is decided by what is at the path, not by a flag here:\n\n"

	names := make([]string, 0, len(openers))
	for name := range openers {
		names = append(names, name)
	}
	sort.Strings(names)

	width := widest(0, names)
	for _, name := range names {
		long += fmt.Sprintf("  %-*s  %s\n", width, name, openers[name].does)
	}
	return long + "\nA machine that will not say what is there — because it is off, or because it\n" +
		"shares nothing with you — leaves what you typed to say it, and the far end still\n" +
		"decides. A message for a machine that is off is queued for it."
}

// runConnect asks the far end what it serves, finds the namespace the address lands in, and hands
// it to whatever this build knows how to do with that kind.
func runConnect(parent context.Context, text string, args []string, stdinName string, wait time.Duration) error {
	at, err := ns.ParseAddress(text)
	if err != nil {
		return err
	}
	if at.Here {
		return fmt.Errorf("%s is this machine, and connect opens somebody else's namespace", at)
	}

	entry, err := resolve(at)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
	defer stop()

	n, err := node.Start(ctx)
	if err != nil {
		return err
	}
	defer n.Close()

	lan, _ := discovery.StartLAN(ctx, n)

	// One deadline for both halves: asking what is there and then opening it are two halves of one
	// wait, and a machine that answers nothing still leaves what was typed somewhere to go.
	until := time.Now().Add(wait)

	find, cancel := context.WithDeadline(ctx, until)
	hello, _ := serving(find, n, lan, entry)
	cancel()

	served, rest, how, err := choose(hello.Serves, at, args)
	if err != nil {
		return err
	}

	return how.open(ctx, opening{
		at: at, entry: entry, served: served, rest: rest,
		args: args, stdinName: stdinName,
		node: n, lan: lan, until: until,
	})
}

// choose is what an address lands on: the namespace the far end serves there, whatever is left of
// the path below it, and how this build opens that kind of namespace.
//
// Everything it can refuse is refused with a sentence saying what is there instead, because an
// archetype this build has never heard of is a thing somebody can see in a listing and is entitled
// to be told about.
func choose(serves []proto.Served, at ns.Address, args []string) (proto.Served, string, opener, error) {
	served, rest, ok := servedAt(serves, at.Path)
	if !ok && at.Path == ns.Root {
		return proto.Served{}, "", opener{}, fmt.Errorf("%s is a machine and not a namespace on it: `drop path ls %s` says what it serves", at, at)
	}
	if !ok {
		return blind(at, args)
	}
	if served.Locked {
		return proto.Served{}, "", opener{}, fmt.Errorf("%s is visible but not shared with you: ask for it with `drop path ask %s`", at, at)
	}
	if served.Archetype == "" {
		return proto.Served{}, "", opener{}, fmt.Errorf("%s holds other namespaces and is none itself: `drop path ls %s`", at, at)
	}

	how, ok := openerFor(served.Archetype)
	if !ok {
		return proto.Served{}, "", opener{}, fmt.Errorf("%s is a %s namespace, which this build cannot open from the command line", at, served.Archetype)
	}
	if how.takes == "" && len(args) > 0 {
		return proto.Served{}, "", opener{}, fmt.Errorf("%s is a %s namespace, and takes nothing after the address", at, served.Archetype)
	}
	return served, rest, how, nil
}

// blind is what to do when the far end has not said what is at the path: it is off, it answers no
// hello, or it shows this caller nothing there.
//
// What was typed is then the only thing that says what this is, so it is read — and the namespace
// is opened without naming an archetype, so the far end decides. Its refusal is a better sentence
// than a guess made here, and a message for a machine that is off belongs in the queue for it
// rather than in an error.
func blind(at ns.Address, args []string) (proto.Served, string, opener, error) {
	how, ok := openerFor(guessed(args))
	if !ok {
		return proto.Served{}, "", opener{}, fmt.Errorf("nothing is served at %s", at)
	}
	return proto.Served{Path: at.Path}, "", how, nil
}

// guessed reads the arguments rather than asking for a kind: a URL is a link, something on this
// disk is a file, other words are a message, and nothing at all is a screen to watch.
func guessed(args []string) string {
	if len(args) == 0 {
		return "tty"
	}
	if len(args) == 1 && (strings.HasPrefix(args[0], "http://") || strings.HasPrefix(args[0], "https://")) {
		return "link"
	}
	for _, arg := range args {
		if arg == "-" {
			return "share"
		}
		if _, err := os.Stat(arg); err != nil {
			return "chat"
		}
	}
	return "share"
}

// servedAt is the namespace an address lands in: the deepest one the far end serves that covers the
// path, and whatever is left of the path below it.
func servedAt(serves []proto.Served, path string) (proto.Served, string, bool) {
	out, rest, found := proto.Served{}, "", false

	for _, served := range serves {
		if !covers(served.Path, path) {
			continue
		}
		if found && len(served.Path) <= len(out.Path) {
			continue
		}
		out, rest, found = served, strings.Trim(strings.TrimPrefix(path, served.Path), "/"), true
	}
	return out, rest, found
}
