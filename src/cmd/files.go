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

	"github.com/bresilla/drop/src/pkg/arch/files"
	"github.com/bresilla/drop/src/pkg/book"
	"github.com/bresilla/drop/src/pkg/discovery"
	"github.com/bresilla/drop/src/pkg/node"
	"github.com/bresilla/drop/src/pkg/ns"
	"github.com/bresilla/drop/src/pkg/plain"
	"github.com/bresilla/drop/src/pkg/proto"
	"github.com/bresilla/drop/src/pkg/wire"
)

// walking is a directory open on another device, and the path that was named inside it.
type walking struct {
	*files.Browsing
	// entry is the machine, at is the address with the namespace on it, and rest is what was named
	// below that namespace. Empty rest is the namespace itself.
	entry book.Entry
	at    ns.Address
	rest  string
	stop  func()
}

// where is the namespace as it would be typed.
func (w *walking) where() string { return w.at.String() }

// walk finds the namespace a typed path lands in and opens it.
//
// Two questions, in that order: what does this machine serve, and which of those holds the path.
// Only the first half of the path is a drop path — the rest is a filename on the far machine, with
// whatever capitals and spaces that filesystem takes, and it travels exactly as it was typed.
func walk(parent context.Context, target string, wait time.Duration) (*walking, error) {
	at, under, err := splitAddress(target)
	if err != nil {
		return nil, err
	}

	entry, err := resolve(at)
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

	found, rest, ok := insideFiles(hello.Serves, under)
	if !ok {
		giveUp()
		return nil, fmt.Errorf("%s shares no directory holding %s", entry.Name, under)
	}
	at.Path = found

	b, done, err := browse(find, n, lan, entry, found)
	if err != nil {
		giveUp()
		return nil, err
	}
	return &walking{Browsing: b, entry: entry, at: at, rest: rest, stop: func() { done(); giveUp() }}, nil
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
	if err := w.Get(w.rest, into, files.Want{Progress: bar.update}); err != nil {
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
		return fmt.Errorf("%s takes nothing: it is read-only", w.where())
	}

	bar := &progress{}
	defer bar.clear()

	for _, from := range sources {
		if from == "-" {
			// Standard input has no length, so the far end reads until it ends rather than
			// counting down.
			if err := w.Put(below(w.rest, stdinName), os.Stdin, files.Given{Size: wire.SizeUnknown, Mode: 0o600, Progress: bar.update}); err != nil {
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
func listInside(b *files.Browsing, id node.ID, where, rest string) error {
	items, err := b.List(rest)
	if err != nil {
		return err
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

	fmt.Printf("\n%s  %s\n\n", where, node.Brief(id))
	for _, item := range items {
		fmt.Printf("  %-*s  %10s  %s\n", width, shownAs(item), sizeOf(item), changed(item.At))
	}
	fmt.Println()
	return nil
}

// shownAs marks a directory, so a listing needs no column to say which is which.
// shownAs is a remote file as it is printed.
//
// Made safe here and not where it arrives: the name that reaches this disk when the file is fetched
// is a different question, answered by containment, and a name cleaned up on the way in would be a
// name that no longer matches the file it asks for.
func shownAs(item files.Entry) string {
	name := plain.Text(item.Name, files.MaxRel)
	if item.Dir {
		return name + "/"
	}
	return name
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
	return time.Unix(0, at).Format("2006-01-02 15:04")
}
