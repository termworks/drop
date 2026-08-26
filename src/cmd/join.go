package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/bresilla/drop/src/pkg/arch"
	"github.com/bresilla/drop/src/pkg/book"
	"github.com/bresilla/drop/src/pkg/conf"
	"github.com/bresilla/drop/src/pkg/discovery"
	"github.com/bresilla/drop/src/pkg/made"
	"github.com/bresilla/drop/src/pkg/meet"
	"github.com/bresilla/drop/src/pkg/node"
	"github.com/bresilla/drop/src/pkg/ns"
	"github.com/bresilla/drop/src/pkg/proto"
)

// Taking up a namespace somebody else already holds.
//
// Nobody is invited. A namespace turns up in `drop path ls <somebody>` because their access rule
// names you, and this is how you say yes to it: the name it goes by is worked out from what they
// said about it rather than taken on trust, and from then on this machine is one of the machines
// that holds it.

func newJoinCmd() *cobra.Command {
	var (
		text, on, lists []string
		at              string
		wait            time.Duration
	)

	cmd := &cobra.Command{
		Use:   "join <address>",
		Short: "Hold a namespace somebody else holds",
		Long: "join takes up a namespace several machines share. It turns up in `drop path ls` on\n" +
			"the machine that has it because the rule there names you, and joining it makes this\n" +
			"machine one of the ones holding it.\n\n" +
			"  drop path join bob:laptop:/notes\n" +
			"  drop path join bob::/notes --at /bobs-notes\n\n" +
			"It is written down beside the config, so it is here after a restart, and what came\n" +
			"over comes over now. Who may reach it here is this machine's own decision: joining\n" +
			"names the person you joined from, and `drop path grant` names anybody else.\n\n" +
			"A setting is text, on or off, or a list of names, the same as `drop path create` —\n" +
			"a namespace that needs one here needs it given here.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			declared, err := settings(text, on, lists)
			if err != nil {
				return err
			}
			return runJoin(cmd.Context(), args[0], at, declared, wait)
		},
	}

	cmd.Flags().StringVar(&at, "at", "", "where to hold it here; the path it has there by default")
	cmd.Flags().DurationVarP(&wait, "wait", "w", 30*time.Second, "how long to spend reaching the machine")
	cmd.Flags().StringArrayVar(&text, "set", nil, "key=value, a setting read as text")
	cmd.Flags().StringArrayVar(&on, "flag", nil, "key[=false], a setting read as on or off")
	cmd.Flags().StringArrayVar(&lists, "list", nil, "key=a,b, a setting read as a list of names")

	return cmd
}

func runJoin(parent context.Context, target, here string, declared made.Settings, wait time.Duration) error {
	address, err := ns.ParseAddress(target)
	if err != nil {
		return err
	}
	if address.Here {
		return fmt.Errorf("%s is this machine, and join takes up somebody else's namespace", address)
	}
	if address.Path == ns.Root {
		return fmt.Errorf("%s names a machine and not a namespace on it: `drop path ls %s` says what it shares", address, address)
	}

	entry, err := resolve(address)
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

	find, cancel := context.WithTimeout(ctx, wait)
	hello, err := serving(find, n, lan, entry)
	cancel()
	if err != nil {
		return err
	}

	known := reading()
	served, err := joinable(known, hello.Serves, address)
	if err != nil {
		return err
	}

	at := here
	if at == "" {
		at = served.Path
	}
	at, err = ns.Clean(at)
	if err != nil {
		return err
	}

	line := made.Line{
		Path: at,
		Keep: true,
		Entry: made.Entry{
			Archetype: served.Archetype,
			Version:   served.Version,
			Settings:  declared,
			Access:    made.Access{Named: []string{personOf(entry)}},
			Shared:    served.Shared,
		},
	}

	held, err := taken(known, line)
	if err != nil {
		return err
	}
	if !held {
		if err := writeJoined(known, line); err != nil {
			return err
		}
	}

	caught, err := caughtUp(ctx, n, lan, entry, at, line.Entry)
	if err != nil {
		fmt.Printf("\n%s is held here; catching up with %s failed: %v\n\n", at, entry.Name, err)
		return nil
	}
	sayJoined(at, entry, served, caught, held)
	return nil
}

// joinable is the namespace an address lands on, and everything about it that means no.
//
// Every refusal says what is there instead, because somebody read a listing and typed what it said.
func joinable(known *arch.Registry, serves []proto.Served, address ns.Address) (proto.Served, error) {
	served, _, found := servedAt(serves, address.Path)
	if !found || served.Path != address.Path {
		return proto.Served{}, fmt.Errorf("%s serves nothing at %s", address.Machine, address.Path)
	}
	if served.Locked {
		return proto.Served{}, fmt.Errorf("%s is visible but not shared with you: ask for it with `drop path ask %s`", address, address)
	}
	if served.Archetype == "" {
		return proto.Served{}, fmt.Errorf("%s holds other namespaces and is none itself: `drop path ls %s`", address, address)
	}
	if served.Shared.Declared() {
		return served, nil
	}

	// It is not shared, and there are two reasons for that. One is a decision somebody made about
	// this namespace, and the other is that a namespace of that kind is nobody else's to hold.
	if answers, ok := known.Lookup(served.Archetype, served.Version); ok && !answers.Note(nil).Shareable {
		return proto.Served{}, fmt.Errorf("%s is a %s namespace, and a %s is one machine's own", address, served.Archetype, served.Archetype)
	}
	return proto.Served{}, fmt.Errorf("%s is a %s namespace that %s holds alone", address, served.Archetype, address.Machine)
}

// taken reports whether this machine already holds that namespace, and refuses a path that is
// something else.
//
// Joining twice writes nothing: the same namespace at the same path is the one already here, and
// the catching up that follows is what somebody asking again actually wanted.
func taken(known *arch.Registry, line made.Line) (bool, error) {
	cfg, err := conf.Load(known)
	if err != nil {
		return false, err
	}
	defer cfg.Close()

	if declares(cfg, line.Path) {
		return false, fmt.Errorf("%s declares %s already, so it is not this command's to hold", where(cfg), line.Path)
	}

	store, err := made.Load()
	if err != nil {
		return false, err
	}
	was, ok := store.Get(line.Path)
	if !ok {
		return false, nil
	}
	if was.Shared.ID() != line.Shared.ID() {
		return false, fmt.Errorf("%s is something else here already: `drop path rm %s` first, or join it --at another path", line.Path, line.Path)
	}
	return true, nil
}

// writeJoined checks this build can serve what is being joined, writes it down, and puts it up.
//
// Checked before it is written, because a namespace written down and refused at the next start is a
// path that silently is not there — a `files` joined without saying which directory it is, on the
// machine doing the joining.
func writeJoined(known *arch.Registry, line made.Line) error {
	answers, ok := known.Lookup(line.Archetype, line.Version)
	if !ok {
		return known.Missing(line.Archetype, line.Version)
	}
	if _, err := answers.Read(made.Declared(line.Settings)); err != nil {
		return fmt.Errorf("%s: %w", line.Path, err)
	}

	store, err := made.Load()
	if err != nil {
		return err
	}
	if err := store.Add(line.Path, line.Entry); err != nil {
		return err
	}

	// Nothing serving here is the ordinary case, and the file is what makes it here after a
	// restart. The catching up that follows needs no node of this machine's own.
	conn, err := asking()
	if errors.Is(err, errNoNode) {
		return nil
	}
	if err != nil {
		return err
	}
	defer conn.Close()

	_, err = tell(conn, line)
	return err
}

// caughtUp fetches what has happened in a namespace this machine has just taken up.
func caughtUp(ctx context.Context, n *node.Node, lan *discovery.LAN, entry book.Entry, at string, e made.Entry) (meet.Caught, error) {
	pinned, err := book.Load()
	if err != nil {
		return meet.Caught{}, err
	}

	mount := ns.Mount{Path: at, Archetype: e.Archetype, Version: e.Version, Access: e.Access.Rule(), Shared: e.Shared}
	return catchUp(ctx, best(n, lan), entry, mount, mount.Access, pinned)
}

// sayJoined says what was taken up: what it is, who else has it, and how much came over.
func sayJoined(at string, entry book.Entry, served proto.Served, caught meet.Caught, was bool) {
	verb := "is held here"
	if was {
		verb = "was already held here"
	}

	fmt.Printf("\n%s %s  →  %s, shared\n\n", at, verb, served.Archetype)
	fmt.Printf("  also held by  %s\n", holdersOf(served.Holders))
	fmt.Printf("  history       %s\n", howMuch(caught))
	fmt.Printf("  reachable by  %s\n\n", personOf(entry))
}

// holdersOf is who else holds a namespace, in this machine's own words for them.
//
// The keys travel because a name in an address book is one machine's private label and means
// nothing anywhere else. Somebody this machine has never paired with is counted rather than named:
// there is nothing to call them yet.
func holdersOf(keys []string) string {
	mine, named, strangers := myKey(), []string{}, 0

	pinned, err := book.Load()
	for _, key := range keys {
		if key == mine {
			continue
		}
		if err == nil {
			if owner, ok := pinned.ByUser(key); ok {
				named = append(named, personOf(owner))
				continue
			}
		}
		strangers++
	}

	sort.Strings(named)
	switch {
	case strangers == 1:
		named = append(named, "1 person you have not paired with")
	case strangers > 1:
		named = append(named, fmt.Sprintf("%d people you have not paired with", strangers))
	}
	if len(named) == 0 {
		return "nobody else, yet"
	}
	return strings.Join(named, ", ")
}

// howMuch is what a catch-up came to, in the words somebody joining would use.
func howMuch(caught meet.Caught) string {
	switch {
	case caught.More:
		return fmt.Sprintf("%d changes came over, and there are more", caught.Taken)
	case caught.Taken == 1:
		return "1 change came over"
	case caught.Taken > 1:
		return fmt.Sprintf("%d changes came over", caught.Taken)
	}
	return "nothing has happened there yet"
}

// personOf is what to call whoever a machine belongs to: the person when it is somebody's, and the
// machine itself when it is nobody's.
func personOf(entry book.Entry) string {
	if entry.Person != "" {
		return entry.Person
	}
	return entry.Name
}
