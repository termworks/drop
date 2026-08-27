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

	"github.com/bresilla/drop/src/pkg/among"
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
			"over comes over now. Who may reach it here is this machine's own decision, and it is\n" +
			"also who this machine takes a change from: joining names the person you joined from\n" +
			"and nobody else, so a change by anybody else holding it is refused until you have\n" +
			"paired with them and `drop path grant` names them. Joining says who those are.\n\n" +
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
	if err := offered(served, entry); err != nil {
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
	sayJoined(at, entry, served, line.Access.Rule(), caught, held)
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
	// What kind of thing it is is asked first, and of this machine rather than of the far end. A
	// namespace of a kind that is one machine's own is nobody else's to hold however the peer
	// describes it: joining one would put a terminal up here under a rule naming whoever offered it.
	if answers, ok := known.Lookup(served.Archetype, served.Version); ok && !answers.Note(nil).Shareable {
		return proto.Served{}, fmt.Errorf("%s is a %s namespace, and a %s is one machine's own", address, served.Archetype, served.Archetype)
	}
	if served.Shared.Declared() {
		return served, nil
	}
	return proto.Served{}, fmt.Errorf("%s is a %s namespace that %s holds alone", address, served.Archetype, address.Machine)
}

// offered refuses a namespace whose own account of itself does not hold together.
//
// What it is called is worked out from three facts the far end sends, and this machine files a
// history under that name — so the three are worth reading rather than copying. A machine that says
// it made this one is naming the path it made it at, and that is a path it serves.
func offered(served proto.Served, entry book.Entry) error {
	if entry.User == "" || served.Shared.Creator != entry.User {
		return nil
	}
	if served.Shared.At != served.Path {
		return fmt.Errorf("%s serves it at %s and says it was made at %s: a namespace somebody made is one they hold where they made it",
			personOf(entry), served.Path, served.Shared.At)
	}
	return nil
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
	if at, twice := already(cfg.Mounts.All(), store, line); twice {
		return false, fmt.Errorf("%s is the namespace already held at %s, and one thing is held once here: `drop path rm %s` first, or catch up on it where it is", line.Path, at, at)
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

// already is where this machine holds that same namespace, when it holds it somewhere else.
//
// A namespace is one thing and its history is filed under the one name every machine holding it
// works out, so two paths carrying that name are two access rules over one history: whatever
// arrives through either is written into the other, and whatever the other sends goes out under a
// rule its own holders never agreed to. The name is the far end's word, and this is the part of it
// this machine can check.
func already(mounts []ns.Mount, store *made.Store, line made.Line) (string, bool) {
	name := line.Shared.ID()
	if name == "" {
		return "", false
	}

	for _, m := range mounts {
		if m.Path != line.Path && m.Shared.ID() == name {
			return m.Path, true
		}
	}
	for _, at := range store.Paths() {
		if at == line.Path {
			continue
		}
		if was, ok := store.Get(at); ok && was.Shared.ID() == name {
			return at, true
		}
	}
	return "", false
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

// sayJoined says what was taken up: what it is, who else has it, whose changes it will not take,
// and how much came over.
func sayJoined(at string, entry book.Entry, served proto.Served, rule ns.Access, caught meet.Caught, was bool) {
	verb := "is held here"
	if was {
		verb = "was already held here"
	}

	fmt.Printf("\n%s %s  →  %s, shared\n\n", at, verb, served.Archetype)
	fmt.Printf("  also held by  %s\n", holdersOf(served.Holders))
	fmt.Printf("  history       %s\n", howMuch(caught))
	fmt.Printf("  reachable by  %s\n", personOf(entry))
	for _, line := range sayUnheard(at, notNamed(served.Holders, rule)) {
		fmt.Printf("  %s\n", line)
	}
	fmt.Println()
}

// unheard is the holders of a namespace whose changes this machine will not take.
type unheard struct {
	// named is what this machine calls the ones it has a name for, and strangers is how many of the
	// rest there are. There is nothing to call somebody nobody here has paired with.
	named     []string
	strangers int
}

// notNamed is who else holds a namespace and is not named by the rule written for it here.
//
// Joining names the person joined from and nobody else, so anybody else holding it is somebody
// whose changes are passed over — and, because a change cannot be placed without the ones it names,
// so is everything made after one of them. That is a namespace that stops moving rather than an
// error anybody sees, which is why it is said out loud while there is still somebody to say it
// about.
func notNamed(keys []string, rule ns.Access) unheard {
	pinned, err := book.Load()
	if err != nil {
		return unheard{}
	}

	var out unheard
	mine, admits := myKey(), among.Admits(rule, pinned, myKey())
	for _, key := range keys {
		if key == mine || admits(key) {
			continue
		}
		if owner, ok := pinned.ByUser(key); ok {
			out.named = append(out.named, personOf(owner))
			continue
		}
		out.strangers++
	}
	sort.Strings(out.named)
	return out
}

// sayUnheard is what to tell somebody about the holders whose changes will not be taken, and what
// each kind of them needs before they will be.
func sayUnheard(at string, who unheard) []string {
	var out []string

	if len(who.named) > 0 {
		out = append(out, fmt.Sprintf("not taken     changes by %s: `drop path grant %s %s`",
			strings.Join(who.named, ", "), at, who.named[0]))
	}
	switch {
	case who.strangers == 1:
		out = append(out, fmt.Sprintf("not taken     changes by 1 holder you have not paired with: pair, then grant %s", at))
	case who.strangers > 1:
		out = append(out, fmt.Sprintf("not taken     changes by %d holders you have not paired with: pair, then grant %s", who.strangers, at))
	}
	if len(out) > 0 {
		out = append(out, "              and everything made after one of theirs, until then")
	}
	return out
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
//
// What was refused is said beside what came over. A change signed by somebody this machine's rule
// does not name is passed over along with everything made after it, so a count of what arrived and
// no count of what did not is a namespace that reads as caught up and is not.
func howMuch(caught meet.Caught) string {
	came := "nothing has happened there yet"
	switch {
	case caught.More:
		came = fmt.Sprintf("%d changes came over, and there are more", caught.Taken)
	case caught.Taken == 1:
		came = "1 change came over"
	case caught.Taken > 1:
		came = fmt.Sprintf("%d changes came over", caught.Taken)
	case caught.Refused > 0:
		came = "nothing came over"
	}

	switch {
	case caught.Refused == 1:
		return came + ", and 1 was refused"
	case caught.Refused > 1:
		return fmt.Sprintf("%s, and %d were refused", came, caught.Refused)
	}
	return came
}

// personOf is what to call whoever a machine belongs to: the person when it is somebody's, and the
// machine itself when it is nobody's.
func personOf(entry book.Entry) string {
	if entry.Person != "" {
		return entry.Person
	}
	return entry.Name
}
