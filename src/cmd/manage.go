package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"

	"github.com/spf13/cobra"
	"github.com/tmc/go-iroh/iroh"

	"github.com/bresilla/drop/src/pkg/arch"
	"github.com/bresilla/drop/src/pkg/asked"
	"github.com/bresilla/drop/src/pkg/book"
	"github.com/bresilla/drop/src/pkg/conf"
	"github.com/bresilla/drop/src/pkg/grant"
	"github.com/bresilla/drop/src/pkg/node"
	"github.com/bresilla/drop/src/pkg/ns"
	"github.com/bresilla/drop/src/pkg/proto"
	"github.com/bresilla/drop/src/pkg/tui"
	"github.com/bresilla/drop/src/pkg/user"
)

// Who may reach this machine's paths, read and changed as steps on a ladder. The same answer for
// the interface on this machine and for a machine of this user's asking from elsewhere, so the two
// cannot come to mean different things.

// The shapes the answers take, which the terminal interface draws and the phone reads as JSON.
type (
	PathState   = tui.PathState
	PathDetail  = tui.PathDetail
	WhoState    = tui.WhoState
	AskingState = tui.AskingState
	ReachState  = tui.ReachState
	PathOpen    = tui.PathOpen
)

// errNotMine is an ask from a machine that is not this user's.
var errNotMine = errors.New("only a machine of this machine's owner may change who reaches it")

// ManageHere answers an ask about this machine's own paths.
func ManageHere(known *arch.Registry, m proto.Manage) ([]byte, error) {
	store, err := grant.Load()
	if err != nil {
		return nil, err
	}

	switch m.Op {
	case proto.ManageList:
		all, err := pathStates(known)
		if err != nil {
			return nil, err
		}
		return json.Marshal(all)
	case proto.ManageFor:
		reach, err := reachOf(known, m.Who)
		if err != nil {
			return nil, err
		}
		return json.Marshal(reach)
	case proto.ManageRead:
	case proto.ManageLevel:
		err = store.SetLevel(m.Path, m.Level)
	case proto.ManageShown:
		shown := m.Shown
		err = store.SetShown(m.Path, &shown)
	case proto.ManageAllow:
		err = editGrant(m.Path, m.Who, grantAllow)
	case proto.ManageDeny:
		err = editGrant(m.Path, m.Who, grantDeny)
	case proto.ManageUnset:
		err = editGrant(m.Path, m.Who, grantForget)
	default:
		return nil, fmt.Errorf("%q is nothing a machine is asked", m.Op)
	}
	if err != nil {
		return nil, err
	}

	detail, err := pathDetail(known, m.Path)
	if err != nil {
		return nil, err
	}
	return json.Marshal(detail)
}

// ruled is this machine's namespaces with what was taken up since, before and after the grants.
func ruled(known *arch.Registry) (written, granted *conf.Config, err error) {
	written, err = conf.Load(known)
	if err != nil {
		return nil, nil, err
	}
	if err := created(written); err != nil {
		written.Close()
		return nil, nil, err
	}
	granted, err = conf.Load(known)
	if err != nil {
		written.Close()
		return nil, nil, err
	}
	if err := created(granted); err != nil {
		written.Close()
		granted.Close()
		return nil, nil, err
	}
	if _, err := granted.Grants(); err != nil {
		written.Close()
		granted.Close()
		return nil, nil, err
	}
	return written, granted, nil
}

func pathStates(known *arch.Registry) ([]PathState, error) {
	written, granted, err := ruled(known)
	if err != nil {
		return nil, err
	}
	defer written.Close()
	defer granted.Close()

	waiting, _ := waitingAll()
	var out []PathState
	for _, m := range granted.Mounts.All() {
		if m.Branch() {
			continue
		}
		out = append(out, stateOf(known, written, granted, m, waiting[m.Path]))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

func pathDetail(known *arch.Registry, path string) (PathDetail, error) {
	at, err := ns.Clean(path)
	if err != nil {
		return PathDetail{}, err
	}
	written, granted, err := ruled(known)
	if err != nil {
		return PathDetail{}, err
	}
	defer written.Close()
	defer granted.Close()

	mount, _, ok := granted.Mounts.Lookup(at)
	if !ok || mount.Path != at {
		return PathDetail{}, fmt.Errorf("%s is not a path of this machine's", at)
	}
	asking, err := waiting(at)
	if err != nil {
		return PathDetail{}, err
	}
	out := PathDetail{PathState: stateOf(known, written, granted, mount, len(asking))}
	for _, a := range asking {
		out.Asking = append(out.Asking, AskingState(a))
	}

	pinned, err := book.Load()
	if err != nil {
		return PathDetail{}, err
	}
	rule, _ := granted.Mounts.AccessFor(at)
	config, _ := written.Mounts.AccessFor(at)
	for _, who := range others(pinned) {
		who.At = standingName(standingIn(rule, who.Name))
		who.InConfig = named(config, who.Name)
		out.Who = append(out.Who, who)
	}
	return out, nil
}

// reachOf is what one person may open here, who being their user key or, for a machine that
// belongs to nobody, its id. Judged by the same rule a connection from them would be.
func reachOf(known *arch.Registry, who string) (ReachState, error) {
	pinned, err := book.Load()
	if err != nil {
		return ReachState{}, err
	}
	out := ReachState{Paths: []PathOpen{}}
	caller := ns.Caller{ID: who, User: who}
	if owner, ok := pinned.ByUser(who); ok {
		out.Called, out.Known = owner.Person, true
		caller = ns.Caller{ID: owner.ID.String(), Name: owner.Name, User: who, UserName: owner.Person, Paired: true, Trusted: owner.Trusted}
	} else if id, err := node.ParseID(who); err == nil {
		if entry, ok := pinned.ByID(id); ok {
			out.Called, out.Known = entry.Name, true
			caller = ns.Caller{ID: who, Name: entry.Name, Paired: entry.Paired(), Trusted: entry.Trusted}
		}
	}

	written, granted, err := ruled(known)
	if err != nil {
		return ReachState{}, err
	}
	defer written.Close()
	defer granted.Close()

	for _, m := range granted.Mounts.All() {
		if m.Branch() {
			continue
		}
		rule, _ := granted.Mounts.AccessFor(m.Path)
		opens, _ := rule.Admits(caller)
		one := PathOpen{Path: m.Path, Archetype: m.Archetype, Level: ns.LevelOf(rule), Opens: opens}
		if out.Called != "" {
			one.At = standingName(standingIn(rule, out.Called))
		}
		if answers, ok := known.Lookup(m.Archetype, m.Version); ok {
			one.About = answers.Note(m.Config).About
		}
		out.Paths = append(out.Paths, one)
	}
	sort.Slice(out.Paths, func(i, j int) bool { return out.Paths[i].Path < out.Paths[j].Path })
	return out, nil
}

// stateOf is one mount as a step, and who is let in and kept out beyond it.
func stateOf(known *arch.Registry, written, granted *conf.Config, m ns.Mount, asked int) PathState {
	rule, _ := granted.Mounts.AccessFor(m.Path)
	config, _ := written.Mounts.AccessFor(m.Path)
	store, _ := grant.Load()
	chosen := ""
	if store != nil {
		chosen, _ = store.Level(m.Path)
	}

	out := PathState{
		Path:      m.Path,
		Archetype: m.Archetype,
		Level:     ns.LevelOf(rule),
		Chosen:    chosen != "",
		Config:    ns.LevelOf(config),
		Shown:     rule.Shows(),
		Password:  rule.Password != "",
		Refused:   append([]string{}, rule.Refused...),
		Asked:     asked,
	}
	for _, name := range rule.Named {
		if name != ns.LevelMe {
			out.Allowed = append(out.Allowed, name)
		}
	}
	if out.Allowed == nil {
		out.Allowed = []string{}
	}
	if answers, ok := known.Lookup(m.Archetype, m.Version); ok {
		out.About = answers.Note(m.Config).About
	}
	return out
}

// others is everybody in the address book but this user: each person once, and each machine that
// belongs to nobody on its own. Your own machines are on every step, so there is nothing to decide.
func others(pinned *book.Book) []WhoState {
	seen := map[string]bool{}
	var out []WhoState
	for _, entry := range pinned.All() {
		if entry.User != "" && entry.User == myKey() {
			continue
		}
		if !entry.Owned() {
			out = append(out, WhoState{Name: entry.Name, Trusted: entry.Trusted})
			continue
		}
		if seen[entry.Person] {
			continue
		}
		seen[entry.Person] = true
		out = append(out, WhoState{Name: entry.Person, Person: true, Trusted: entry.Trusted})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// standingName is a standing as the interface reads it.
func standingName(at tui.Standing) string {
	switch at {
	case tui.Allowed:
		return "allowed"
	case tui.Refused:
		return "refused"
	}
	return ""
}

// waitingAll is how many are waiting at each path.
func waitingAll() (map[string]int, error) {
	out := map[string]int{}
	all, err := asked.All()
	if err != nil {
		return out, err
	}
	for _, one := range all {
		out[one.Path]++
	}
	return out, nil
}

// managing answers a machine of this user's asking about this machine's paths.
func managing(pinned *book.Book, known *arch.Registry) func(node.ID, *iroh.Stream) {
	return func(from node.ID, s *iroh.Stream) {
		defer func() { _ = s.Close() }()
		if err := pinned.Refresh(); err != nil {
			return
		}
		_ = proto.AnswerManage(s, from, func(badge proto.Badged, m proto.Manage) ([]byte, error) {
			if who := whoIs(pinned)(from, badge, proto.Stood{}); who.UserName != ns.LevelMe {
				return nil, errNotMine
			}
			return ManageHere(known, m)
		})
	}
}

// Ask asks a machine of this user's, by name, about its paths; this one when the name is empty.
func (l *running) Ask(ctx context.Context, machine string, m proto.Manage) ([]byte, error) {
	if machine == "" {
		return l.Manage(ctx, nil, m)
	}
	on, err := mineNamed(machine)
	if err != nil {
		return nil, err
	}
	return l.Manage(ctx, &on, m)
}

// Reachable is what somebody may open on this machine and on every other of this user's, each
// asked at once so one that is off costs its own wait and nobody else's.
func (l *running) Reachable(ctx context.Context, name string) ([]tui.Reachable, error) {
	pinned, err := book.Load()
	if err != nil {
		return nil, err
	}
	entries, _, err := managedEntries(pinned, name, true)
	if err != nil {
		return nil, err
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("%s is not in the address book", name)
	}
	who := entries[0].User
	if who == "" {
		who = entries[0].ID.String()
	}

	machines := []string{""}
	for _, entry := range pinned.All() {
		if entry.User != "" && entry.User == myKey() && !user.Removed(entry.ID.String()) {
			machines = append(machines, entry.Name)
		}
	}
	sort.Strings(machines[1:])

	out := make([]tui.Reachable, len(machines))
	var wg sync.WaitGroup
	for i, machine := range machines {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out[i] = tui.Reachable{Machine: machine}
			raw, err := l.Ask(ctx, machine, proto.Manage{Op: proto.ManageFor, Who: who})
			if err == nil {
				err = json.Unmarshal(raw, &out[i].ReachState)
			}
			if err != nil {
				out[i].Err = err.Error()
			}
		}()
	}
	wg.Wait()
	return out, nil
}

// Manage asks about a path on another machine of this user's, or on this one when on is nil.
func (l *running) Manage(ctx context.Context, on *book.Entry, m proto.Manage) ([]byte, error) {
	if on == nil {
		return ManageHere(l.known, m)
	}
	s, done, err := l.open(ctx, *on, node.ALPNManage)
	if err != nil {
		return nil, err
	}
	defer done()
	defer stopStreamOnDone(ctx, s)()
	return proto.AskManage(s, m)
}

func newLevelCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "level <path> [me|trusted|paired|anyone|config]",
		Short: "Who may open a path, as one step: only me, trusted, paired or anyone",
		Long: "The same ladder the phone shows. A step replaces whatever the config says about who may\n" +
			"open the path; names let in or kept out with grant and revoke still count. `config` hands\n" +
			"it back to the config. With no step, says which one it stands on.",
		Args: cobra.RangeArgs(1, 2),
		RunE: func(_ *cobra.Command, args []string) error {
			m := proto.Manage{Op: proto.ManageRead, Path: args[0]}
			if len(args) == 2 {
				m.Op, m.Level = proto.ManageLevel, args[1]
				if m.Level == "config" {
					m.Level = ""
				}
			}
			raw, err := ManageHere(reading(), m)
			if err != nil {
				return err
			}
			var d PathDetail
			if err := json.Unmarshal(raw, &d); err != nil {
				return err
			}
			how := "set here"
			if !d.Chosen {
				how = "as its config says"
			}
			fmt.Printf("%s  %s, %s\n", d.Path, d.Level, how)
			return nil
		},
	}
}
