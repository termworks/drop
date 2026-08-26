package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/bresilla/drop/src/pkg/arch/files"
	"github.com/bresilla/drop/src/pkg/book"
	"github.com/bresilla/drop/src/pkg/discovery"
	"github.com/bresilla/drop/src/pkg/node"
	"github.com/bresilla/drop/src/pkg/proto"
	"github.com/bresilla/drop/src/pkg/wire"
)

// The verbs for a directory somebody else is serving.
//
// Top-level, the way `ls` is, rather than under a `drop files` of their own. A person typing
// `drop get orin/work/report.pdf` is not thinking about archetypes: they are copying a file, and
// which kind of namespace happens to be at that path is the address book's business. Putting them
// under a noun would also split one idea in half, because `drop ls` already walks these paths.

func newGetCmd() *cobra.Command {
	var wait time.Duration

	cmd := &cobra.Command{
		Use:   "get <device>/<path>/<name> [into]",
		Short: "Copy a file out of a directory somebody shares",
		Long: "get reads one file out of a directory another device serves.\n\n" +
			"With no destination it lands here under its own name. A destination that is a\n" +
			"directory takes it under its own name too; anything else is the file to write.",
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			into := ""
			if len(args) == 2 {
				into = args[1]
			}
			return getFrom(cmd.Context(), args[0], into, wait)
		},
	}

	cmd.Flags().DurationVarP(&wait, "wait", "w", 90*time.Second, "how long to spend reaching the device")

	return cmd
}

func newPutCmd() *cobra.Command {
	var (
		as   string
		wait time.Duration
	)

	cmd := &cobra.Command{
		Use:   "put <device>/<path> <file>...",
		Short: "Copy files into a directory somebody shares",
		Long: "put writes files into a directory another device serves, if that directory\n" +
			"takes anything back.\n\n" +
			"  drop put orin/work report.pdf      one file at the top of it\n" +
			"  drop put orin/work/deep a b c      into a directory inside it\n" +
			"  drop put orin/work - --as note.txt and - is standard input",
		Args: cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return putInto(cmd.Context(), args[0], args[1:], as, wait)
		},
	}

	cmd.Flags().StringVar(&as, "as", "stdin", "the name to give standard input")
	cmd.Flags().DurationVarP(&wait, "wait", "w", 90*time.Second, "how long to spend reaching the device")

	return cmd
}

func newRemoveCmd() *cobra.Command {
	var wait time.Duration

	cmd := &cobra.Command{
		Use:   "rm <device>/<path>/<name>",
		Short: "Remove a file from a directory somebody shares",
		Long:  "rm removes one file, or one directory that is already empty.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return changeThere(cmd.Context(), args[0], wait, func(w *walking) error {
				return w.Remove(w.rest)
			}, "removed")
		},
	}

	cmd.Flags().DurationVarP(&wait, "wait", "w", 90*time.Second, "how long to spend reaching the device")

	return cmd
}

func newMkdirCmd() *cobra.Command {
	var wait time.Duration

	cmd := &cobra.Command{
		Use:   "mkdir <device>/<path>/<name>",
		Short: "Make a directory inside one somebody shares",
		Long:  "mkdir makes one directory. Its parent has to be there already.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return changeThere(cmd.Context(), args[0], wait, func(w *walking) error {
				return w.Mkdir(w.rest)
			}, "made")
		},
	}

	cmd.Flags().DurationVarP(&wait, "wait", "w", 90*time.Second, "how long to spend reaching the device")

	return cmd
}

func newMoveCmd() *cobra.Command {
	var wait time.Duration

	cmd := &cobra.Command{
		Use:   "mv <device>/<path>/<from> <to>",
		Short: "Move something inside a directory somebody shares",
		Long: "mv renames something without it ever leaving that device.\n\n" +
			"The destination is named from the top of the same directory, so\n" +
			"`drop mv orin/work/deep/old.txt deep/new.txt` leaves it where it is.",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			to := strings.Trim(args[1], "/")
			return changeThere(cmd.Context(), args[0], wait, func(w *walking) error {
				return w.Move(w.rest, to)
			}, "moved")
		},
	}

	cmd.Flags().DurationVarP(&wait, "wait", "w", 90*time.Second, "how long to spend reaching the device")

	return cmd
}

// walking is a directory open on another device, and the path that was named inside it.
type walking struct {
	*files.Browsing
	// at is the namespace, and rest is what was named below it. Empty rest is the namespace itself.
	at   string
	rest string
	stop func()
}

// walk finds the namespace a typed path lands in and opens it.
//
// Two questions, in that order: what does this device serve, and which of those holds the path.
// Only the first half of the path is a drop path — the rest is a filename on the far machine, with
// whatever capitals and spaces that filesystem takes, and it travels exactly as it was typed.
func walk(parent context.Context, target string, wait time.Duration) (*walking, error) {
	peer, under := splitTarget(target)

	entry, err := book.Resolve(peer)
	if err != nil {
		return nil, err
	}

	ctx, stop := signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
	find, cancel := context.WithTimeout(ctx, wait)

	n, err := node.Start(ctx)
	if err != nil {
		cancel()
		stop()
		return nil, err
	}
	lan, _ := discovery.StartLAN(ctx, n)

	giveUp := func() {
		n.Close()
		cancel()
		stop()
	}

	hello, err := serving(find, n, lan, entry)
	if err != nil {
		giveUp()
		return nil, err
	}

	at, rest, ok := insideFiles(hello.Serves, under)
	if !ok {
		giveUp()
		return nil, fmt.Errorf("%s shares no directory holding %s", entry.Name, under)
	}

	b, done, err := browse(find, n, lan, entry, at)
	if err != nil {
		giveUp()
		return nil, err
	}
	return &walking{Browsing: b, at: at, rest: rest, stop: func() { done(); giveUp() }}, nil
}

// browse opens one files namespace on a device already reachable.
func browse(ctx context.Context, n *node.Node, lan *discovery.LAN, entry book.Entry, at string) (*files.Browsing, func(), error) {
	over, s, err := best(n, lan).To(ctx, entry, node.ALPNSession)
	if err != nil {
		return nil, nil, err
	}
	shut := func() {
		s.Close()
		over.Close()
	}

	conn, err := proto.Open(s, at, "files", 0, "", node.DisplayName())
	if err != nil {
		shut()
		return nil, nil, err
	}
	b, err := files.Browse(conn)
	if err != nil {
		shut()
		return nil, nil, err
	}
	return b, shut, nil
}

// serving asks a device what it shares.
func serving(ctx context.Context, n *node.Node, lan *discovery.LAN, entry book.Entry) (proto.Hello, error) {
	done, s, err := best(n, lan).To(ctx, entry, node.ALPNHello)
	if err != nil {
		return proto.Hello{}, err
	}
	defer done.Close()
	defer s.Close()

	return proto.AskHello(s)
}

// splitTarget takes the device off the front of what was typed.
//
// What is left is not put through ns.Clean: below a directory namespace these are real filenames,
// which that spelling is right to refuse and wrong to be handed.
func splitTarget(text string) (string, string) {
	peer, under, _ := strings.Cut(strings.TrimSpace(text), "/")
	return peer, "/" + strings.Trim(under, "/")
}

// insideFiles finds the directory namespace a path lands in: the deepest one that covers it, and
// what is left of the path below it.
func insideFiles(serves []proto.Served, under string) (string, string, bool) {
	at, rest, found := "", "", false

	for _, served := range serves {
		if served.Archetype != "files" || !covers(served.Path, under) {
			continue
		}
		if found && len(served.Path) <= len(at) {
			continue
		}
		at, rest, found = served.Path, strings.Trim(strings.TrimPrefix(under, served.Path), "/"), true
	}
	return at, rest, found
}

func getFrom(parent context.Context, target, into string, wait time.Duration) error {
	w, err := walk(parent, target, wait)
	if err != nil {
		return err
	}
	defer w.stop()

	if w.rest == "" {
		return fmt.Errorf("%s is the directory itself, not a file in it", target)
	}

	bar := &progress{}
	defer bar.clear()

	into = landing(into, w.rest)
	if err := w.Get(w.rest, into, bar.update); err != nil {
		return err
	}
	fmt.Printf("\n%s is now %s\n", target, into)
	return nil
}

// landing is where a file copied out goes: its own name when nothing was said, its own name inside
// a destination that is a directory, and otherwise the file that was named.
func landing(into, name string) string {
	base := path.Base(name)
	if into == "" {
		return base
	}
	if stat, err := os.Stat(into); err == nil && stat.IsDir() {
		return filepath.Join(into, base)
	}
	return into
}

func putInto(parent context.Context, target string, sources []string, stdinName string, wait time.Duration) error {
	w, err := walk(parent, target, wait)
	if err != nil {
		return err
	}
	defer w.stop()

	if !w.Writable() {
		peer, _ := splitTarget(target)
		return fmt.Errorf("%s%s takes nothing: it is read-only", peer, w.at)
	}

	bar := &progress{}
	defer bar.clear()

	for _, from := range sources {
		if from == "-" {
			// Standard input has no length, so the far end reads until it ends rather than
			// counting down.
			if err := w.Put(below(w.rest, stdinName), os.Stdin, wire.SizeUnknown, 0o600, bar.update); err != nil {
				return err
			}
			continue
		}
		if err := w.PutFile(below(w.rest, filepath.Base(from)), from, bar.update); err != nil {
			return err
		}
	}
	fmt.Printf("\nput %d item(s) into %s\n", len(sources), target)
	return nil
}

// changeThere runs one operation that answers with nothing but a verdict.
func changeThere(parent context.Context, target string, wait time.Duration, do func(*walking) error, did string) error {
	w, err := walk(parent, target, wait)
	if err != nil {
		return err
	}
	defer w.stop()

	if w.rest == "" {
		return fmt.Errorf("%s is the directory itself, and this is for what is in it", target)
	}
	if err := do(w); err != nil {
		return err
	}
	fmt.Printf("%s %s\n", did, target)
	return nil
}

// below names something under whatever part of the namespace was addressed.
func below(rest, name string) string {
	if rest == "" {
		return name
	}
	return rest + "/" + name
}

// listInside prints one directory of a namespace, rather than the namespaces themselves.
func listInside(b *files.Browsing, entry book.Entry, at, rest string) error {
	items, err := b.List(rest)
	if err != nil {
		return err
	}

	where := entry.Name + at
	if rest != "" {
		where += "/" + rest
	}
	if len(items) == 0 {
		fmt.Printf("\n%s is empty\n\n", where)
		return nil
	}

	// Directories first, then names, which is the order a person reads a directory in.
	sort.Slice(items, func(i, j int) bool {
		if items[i].Dir != items[j].Dir {
			return items[i].Dir
		}
		return items[i].Name < items[j].Name
	})

	names := make([]string, 0, len(items))
	for _, item := range items {
		names = append(names, shownAs(item))
	}
	width := widest(0, names)

	fmt.Printf("\n%s  %s\n\n", where, node.Brief(entry.ID))
	for _, item := range items {
		fmt.Printf("  %-*s  %10s  %s\n", width, shownAs(item), sizeOf(item), changed(item.At))
	}
	fmt.Println()
	return nil
}

// shownAs marks a directory, so a listing needs no column to say which is which.
func shownAs(item files.Entry) string {
	if item.Dir {
		return item.Name + "/"
	}
	return item.Name
}

func sizeOf(item files.Entry) string {
	if item.Dir {
		return ""
	}
	return bytes(item.Size)
}

func changed(at int64) string {
	if at <= 0 {
		return ""
	}
	return time.Unix(at, 0).Format("2006-01-02 15:04")
}
