package cmd

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/tmc/go-iroh/iroh"

	"github.com/bresilla/drop/src/pkg/book"
	"github.com/bresilla/drop/src/pkg/dial"
	"github.com/bresilla/drop/src/pkg/discovery"
	"github.com/bresilla/drop/src/pkg/node"
	"github.com/bresilla/drop/src/pkg/proto"
	"github.com/bresilla/drop/src/pkg/tui"
	"github.com/bresilla/drop/src/pkg/user"
)

// Connecting to a device on the same network without typing anything.
//
// Every drop says on the local wire who it is and what it is called, so each one can list the
// devices around it nobody has connected with yet. Picking one asks it: to become one of your
// machines, to pair with whoever owns it, or to let this one join theirs. Its person sees the ask,
// with a number both screens show, and says yes or no. Underneath it is the pairing a code makes,
// with the code handed over on the connection the two devices already share instead of by a
// person.

// inviting is whatever process holds this machine's address, able to ask and to answer.
type inviting struct {
	node *node.Node
	lan  *discovery.LAN
	// held is the connections this machine keeps, which reach somebody already paired with.
	held *dial.Kept
	// offer shows a code for a moment, and yields the name whoever took it was filed under.
	offer func(ctx context.Context, code string, kind offerKind) (<-chan string, error)
	box   *inbox
}

// nearby is every device on this wire that is not already in the book, with whose it says it is.
func (h *inviting) nearby() []tui.Near {
	pinned, err := book.Load()
	if err != nil {
		return nil
	}
	var out []tui.Near
	for _, at := range h.lan.Nearby() {
		if _, known := pinned.ByID(at.ID); known {
			continue
		}
		name := at.Name
		if name == "" {
			name = node.Brief(at.ID)
		}
		out = append(out, tui.Near{ID: at.ID.String(), Name: name})
	}
	return out
}

// send asks one device to connect, and waits for its person's answer and whatever follows it.
func (h *inviting) send(ctx context.Context, to node.ID, kind string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, proto.DecideWithin+time.Minute)
	defer cancel()

	var code string
	var taken <-chan string
	switch kind {
	case proto.InviteMine, proto.InvitePair:
		as := offerPerson
		if kind == proto.InviteMine {
			as = offerMine
		}
		if err := canAdd(as); err != nil {
			// A machine wearing a badge has one that holds the key do it.
			if kind == proto.InviteMine {
				return h.throughMine(ctx, to, kind)
			}
			return "", err
		}
		fresh, err := proto.NewCode()
		if err != nil {
			return "", err
		}
		code = fresh
		if taken, err = h.offer(ctx, code, as); err != nil {
			return "", err
		}
	case proto.InviteJoin:
	default:
		return "", fmt.Errorf("%q is nothing a device can be asked", kind)
	}

	s, done, err := h.reach(ctx, to)
	if err != nil {
		return "", fmt.Errorf("reaching it: %w", err)
	}
	reply, err := proto.SendInvite(s, proto.Invite{Kind: kind, Code: code, Name: node.DisplayName()})
	done()
	if err != nil {
		return "", err
	}
	if !reply.Yes {
		if reply.Why == "" {
			reply.Why = "they said no"
		}
		return "", errors.New(reply.Why)
	}

	if kind == proto.InviteJoin {
		_, name, err := join(ctx, h.node, h.lan, ticketFor(to, reply.Code), "", offerMine, nil)
		return name, err
	}
	select {
	case <-ctx.Done():
		return "", errors.New("they said yes, and then never came")
	case name := <-taken:
		return name, nil
	}
}

// reach is a stream to a device for an invite: over the connections this machine keeps when it is
// somebody already paired with, which finds them wherever they are, and on this wire otherwise.
func (h *inviting) reach(ctx context.Context, to node.ID) (proto.Stream, func(), error) {
	if pinned, err := book.Load(); err == nil && h.held != nil {
		if entry, known := pinned.ByID(to); known && entry.Paired() {
			closer, s, err := kept{held: h.held}.To(ctx, entry, node.ALPNInvite)
			if err != nil {
				return nil, nil, err
			}
			return s, func() { _ = s.Close(); _ = closer.Close() }, nil
		}
	}
	conn, s, err := dial.At(ctx, h.node, h.lan, nil, book.Entry{Name: node.Brief(to), ID: to}, node.ALPNInvite, nil)
	if err != nil {
		return nil, nil, err
	}
	return s, func() { _ = s.Close(); _ = conn.Close() }, nil
}

// answering holds each ask until this machine's person answers it, then does what they said.
func (h *inviting) answering(pinned *book.Book) func(node.ID, *iroh.Stream) {
	return func(from node.ID, s *iroh.Stream) {
		defer func() { _ = s.Close() }()
		_ = pinned.Refresh()

		_ = proto.AnswerInvite(s, from, func(badge proto.Badged, ask proto.Invite) proto.Reply {
			whose := ""
			if badge.Shown() {
				if mine := myKey(); mine != "" && badge.Key == mine {
					whose = "you"
				} else if owner, ok := pinned.ByUser(badge.Key); ok {
					whose = owner.Person
				}
			}
			asking := from.String()
			if badge.Shown() {
				asking = badge.Key
			}
			yes, err := h.box.wait(tui.Invited{
				ID: from.String(), Name: ask.Name, Whose: whose, Kind: ask.Kind,
				Check: proto.Check(h.node.ID().String(), asking), When: time.Now().Unix(),
			})
			if err != nil {
				return proto.Reply{Why: err.Error()}
			}
			if !yes {
				return proto.Reply{Why: "they said no"}
			}

			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			switch ask.Kind {
			case proto.InviteMine, proto.InvitePair:
				as := offerPerson
				if ask.Kind == proto.InviteMine {
					as = offerMine
				}
				if _, _, err := join(ctx, h.node, h.lan, ticketFor(from, ask.Code), "", as, nil); err != nil {
					return proto.Reply{Why: err.Error()}
				}
				return proto.Reply{Yes: true}
			default:
				if err := canAdd(offerMine); err != nil {
					return proto.Reply{Why: err.Error()}
				}
				code, err := proto.NewCode()
				if err != nil {
					return proto.Reply{Why: err.Error()}
				}
				showing, stop := context.WithTimeout(context.Background(), time.Minute)
				taken, err := h.offer(showing, code, offerMine)
				if err != nil {
					stop()
					return proto.Reply{Why: err.Error()}
				}
				go func() {
					defer stop()
					select {
					case <-showing.Done():
					case <-taken:
					}
				}()
				return proto.Reply{Yes: true, Code: code}
			}
		})
	}
}

// inbox is every ask waiting for this machine's person.
type inbox struct {
	mu      sync.Mutex
	waiting map[string]*waitingAsk
	// rang tells whoever draws this machine that something is waiting for them.
	rang func()
}

type waitingAsk struct {
	tui.Invited
	decided chan bool
}

// mostAsking bounds how many asks wait at once, so a stranger with a script cannot fill a screen.
const mostAsking = 8

func newInbox(rang func()) *inbox { return &inbox{waiting: map[string]*waitingAsk{}, rang: rang} }

// wait holds one ask until it is answered or the time for it runs out.
func (b *inbox) wait(ask tui.Invited) (bool, error) {
	b.mu.Lock()
	if _, already := b.waiting[ask.ID]; already {
		b.mu.Unlock()
		return false, errors.New("that device is already asking")
	}
	if len(b.waiting) >= mostAsking {
		b.mu.Unlock()
		return false, errors.New("too many devices are asking at once")
	}
	one := &waitingAsk{Invited: ask, decided: make(chan bool, 1)}
	b.waiting[ask.ID] = one
	b.mu.Unlock()

	defer func() {
		b.mu.Lock()
		delete(b.waiting, ask.ID)
		b.mu.Unlock()
		if b.rang != nil {
			b.rang()
		}
	}()
	if b.rang != nil {
		b.rang()
	}

	select {
	case yes := <-one.decided:
		return yes, nil
	case <-time.After(proto.DecideWithin):
		return false, errors.New("nobody answered in time")
	}
}

// list is every ask waiting, oldest first.
func (b *inbox) list() []tui.Invited {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]tui.Invited, 0, len(b.waiting))
	for _, one := range b.waiting {
		out = append(out, one.Invited)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].When < out[j].When })
	return out
}

// decide answers one ask.
func (b *inbox) decide(id string, yes bool) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	one, ok := b.waiting[id]
	if !ok {
		return errors.New("nothing is asking any more")
	}
	select {
	case one.decided <- yes:
	default:
	}
	return nil
}

// takeInviting answers an interface or a command on this machine about devices nearby: "nearby"
// and "invited" are one line of JSON each, "invite <kind> <id>" waits for the far end and says who
// it was filed as, and "decide <id> <yes|no>" answers an ask.
func takeInviting(ctx context.Context, h *inviting, conn net.Conn, what, rest string) error {
	if h == nil {
		return writeLocal(conn, "failed this node does not invite\n")
	}
	switch what {
	case "nearby":
		return writeLocal(conn, "%s\n", encode(h.nearby()))
	case "invited":
		return writeLocal(conn, "%s\n", encode(h.box.list()))
	case "decide":
		id, yes, _ := strings.Cut(strings.TrimSpace(rest), " ")
		if err := h.box.decide(id, yes == "yes"); err != nil {
			return writeLocal(conn, "failed %v\n", err)
		}
		return writeLocal(conn, "ok\n")
	}
	kind, at, _ := strings.Cut(strings.TrimSpace(rest), " ")
	to, err := node.ParseID(at)
	if err != nil {
		return writeLocal(conn, "failed %q is not a device\n", at)
	}
	defer context.AfterFunc(ctx, func() { _ = conn.Close() })()
	name, err := h.send(ctx, to, kind)
	if err != nil {
		return writeLocal(conn, "failed %s\n", strings.ReplaceAll(err.Error(), "\n", " "))
	}
	return writeLocal(conn, "paired %s\n", name)
}

// encode is one value as one line of JSON.
func encode(v any) string {
	raw, err := json.Marshal(v)
	if err != nil {
		return "null"
	}
	return string(raw)
}

// atDaemon asks the running node one line and reads the one it answers with.
func atDaemon(ctx context.Context, line string) (string, error) {
	path, err := castSocket()
	if err != nil {
		return "", errNoDaemon
	}
	conn, err := dialLocal(ctx, path)
	if err != nil {
		return "", errNoDaemon
	}
	defer func() { _ = conn.Close() }()
	defer context.AfterFunc(ctx, func() { _ = conn.Close() })()

	if err := writeLocal(conn, "%s\n", line); err != nil {
		return "", err
	}
	said, err := readLocalLine(bufio.NewReader(conn))
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", fmt.Errorf("the node stopped answering: %w", err)
	}
	said = strings.TrimSpace(said)
	if rest, failed := strings.CutPrefix(said, "failed "); failed {
		return "", errors.New(rest)
	}
	return said, nil
}

// Nearby is every device on this network nobody here has connected with yet.
func (l *running) Nearby() ([]tui.Near, error) {
	if !l.daemon {
		return l.invites.nearby(), nil
	}
	said, err := atDaemon(context.Background(), "nearby")
	if err != nil {
		return nil, err
	}
	var out []tui.Near
	return out, json.Unmarshal([]byte(said), &out)
}

// Invite asks one device nearby to connect, and waits for its person.
func (l *running) Invite(ctx context.Context, id, kind string) (string, error) {
	if !l.daemon {
		to, err := node.ParseID(id)
		if err != nil {
			return "", err
		}
		return l.invites.send(ctx, to, kind)
	}
	said, err := atDaemon(ctx, "invite "+kind+" "+id)
	if err != nil {
		return "", err
	}
	return strings.TrimPrefix(said, "paired "), nil
}

// Invited is every device waiting for a yes from here.
func (l *running) Invited() ([]tui.Invited, error) {
	if !l.daemon {
		return l.invites.box.list(), nil
	}
	said, err := atDaemon(context.Background(), "invited")
	if err != nil {
		return nil, err
	}
	var out []tui.Invited
	return out, json.Unmarshal([]byte(said), &out)
}

// Decide answers a device waiting for a yes.
func (l *running) Decide(id string, yes bool) error {
	if !l.daemon {
		return l.invites.box.decide(id, yes)
	}
	answer := "no"
	if yes {
		answer = "yes"
	}
	_, err := atDaemon(context.Background(), "decide "+id+" "+answer)
	return err
}

// throughMine has a machine of this user's that holds the key ask a device to become one of theirs,
// for this one, which wears a badge and cannot sign one for anybody.
func (h *inviting) throughMine(ctx context.Context, to node.ID, kind string) (string, error) {
	pinned, err := book.Load()
	if err != nil {
		return "", err
	}
	last := errors.New("none of your machines that holds your key can be reached")
	for _, entry := range pinned.All() {
		if entry.User == "" || entry.User != myKey() || user.Removed(entry.ID.String()) || h.held == nil {
			continue
		}
		closer, s, err := kept{held: h.held}.To(ctx, entry, node.ALPNManage)
		if err != nil {
			last = err
			continue
		}
		raw, err := proto.AskManageWithin(s, proto.Manage{Op: proto.ManageInvite, Who: to.String(), Level: kind}, proto.DecideWithin+time.Minute)
		_ = s.Close()
		_ = closer.Close()
		if err != nil {
			last = err
			continue
		}
		var with string
		if err := json.Unmarshal(raw, &with); err != nil {
			return "", err
		}
		return with, nil
	}
	return "", last
}
