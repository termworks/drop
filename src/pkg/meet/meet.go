// Package meet is two machines catching up on one thing.
//
// Heads and since, both ways at once: I tell you the changes nothing of mine comes after, you send
// me everything I am not already behind, and you do the same in the other direction. Neither side
// asks the other what it has — the heads say it in a few ids however long the history is — and
// neither side has to remember what it sent last time.
//
// Running it costs nothing when there is nothing to say, and running it twice is running it once,
// because a change already held is written down again as nothing. So it is safe on a connection
// arriving, on a change being made, and on a timer, which is exactly when it is wanted.
package meet

import (
	"fmt"

	"github.com/bresilla/drop/src/pkg/history"
	"github.com/bresilla/drop/src/pkg/wire"
)

// What one meeting carries in each direction.
//
// A cap rather than a failure: what is sent is the front of an order the far end can take straight
// through, so a meeting that stops short leaves that side further forward than it was, and the next
// one carries the rest.
const (
	// MaxChanges is how many changes one meeting carries.
	MaxChanges = 1 << 12
	// MaxBytes is how much they may weigh. A peer with a great deal to say says it over several
	// meetings rather than in one that lands on the disk all at once.
	MaxBytes = 1 << 24
)

// Caught is what one meeting came to.
type Caught struct {
	// Sent is how many changes went the other way, and Taken how many were written down here.
	Sent  int
	Taken int
	// Refused is how many arrived and were not written down: from somebody the access rule does not
	// admit, made after one of those, past what a meeting carries, or refused by the history itself.
	Refused int
	// More says one side had more than a meeting carries, so there is another one worth having.
	More bool
	// Trouble is the first change the history would not take, and why. A meeting carries on past
	// one rather than ending on it, so this is how it is heard about at all.
	Trouble error
}

// Ask runs a meeting from the side that opened the session.
//
// who is the far end, by whatever name this machine knows them by. What they say they hold is
// remembered under it, which is what lets settled history be folded away later.
//
// admits says whether a change's author was allowed to make it, by the name they signed with. Nil
// admits nobody, because a namespace that cannot say who may change it is one nobody may.
func Ask(conn *wire.Conn, l *history.Log, who string, admits func(author string) bool) (Caught, error) {
	var out Caught

	if err := writeHeads(conn, l.Heads()); err != nil {
		return out, err
	}
	theirs, err := readHeads(conn)
	if err != nil {
		return out, err
	}
	remember(l, who, theirs)

	sent, more, err := send(conn, l, theirs)
	out.Sent, out.More = sent, more
	if err != nil {
		return out, err
	}

	taken, err := take(conn, l, admits, &out)
	out.Taken = taken
	return out, err
}

// Answer runs a meeting from the side that took the session. The same exchange, in the order that
// keeps the two sides in step: whoever speaks first here listens first there.
func Answer(conn *wire.Conn, l *history.Log, who string, admits func(author string) bool) (Caught, error) {
	var out Caught

	theirs, err := readHeads(conn)
	if err != nil {
		return out, err
	}
	if err := writeHeads(conn, l.Heads()); err != nil {
		return out, err
	}
	remember(l, who, theirs)

	taken, err := take(conn, l, admits, &out)
	out.Taken = taken
	if err != nil {
		return out, err
	}

	sent, more, err := send(conn, l, theirs)
	out.Sent = sent
	out.More = out.More || more
	return out, err
}

// remember writes down how far the far end had got.
//
// A meeting that happened is worth nothing less because the note about it could not be written, so
// a failure here is not one: the only thing lost is that a fold waits for another meeting.
func remember(l *history.Log, who string, heads []history.ID) {
	if who == "" {
		return
	}
	_ = l.Seen(who, heads)
}

// send writes what the far end has not seen, and says how many were left over.
func send(conn *wire.Conn, l *history.Log, theirs []history.ID) (int, bool, error) {
	owed, err := l.Since(theirs)
	if err != nil {
		return 0, false, err
	}

	more := len(owed) > MaxChanges
	if more {
		owed = owed[:MaxChanges]
	}

	weight := 0
	for at, c := range owed {
		raw := c.Encode()
		weight += len(raw)
		if weight > MaxBytes && at > 0 {
			owed, more = owed[:at], true
			break
		}
		if err := conn.WriteFrame(wire.KindItem, raw); err != nil {
			return 0, more, fmt.Errorf("sending a change: %w", err)
		}
	}
	if err := conn.WriteFrame(wire.KindEnd, wire.End{Size: int64(len(owed))}.Encode()); err != nil {
		return 0, more, fmt.Errorf("sending a change: %w", err)
	}
	return len(owed), more, nil
}

// take reads what the far end sends and writes down what may be written down.
//
// A change that cannot be taken is passed over rather than ending the meeting: somebody the rule
// does not admit, one the history refuses, one past what a meeting carries. The rest of what
// arrived is nobody else's fault, and the sender is very often a peer honestly relaying what a
// third machine gave it. Anything made after one that was passed over is passed over too, because
// a change cannot be placed in an order without what it names.
//
// A frame that is not a change at all is a different matter, and ends it.
func take(conn *wire.Conn, l *history.Log, admits func(author string) bool, out *Caught) (int, error) {
	taken, seen, weight := 0, 0, 0
	over := map[history.ID]bool{}

	for {
		kind, body, err := conn.ReadFrame()
		if err != nil {
			return taken, fmt.Errorf("reading a change: %w", err)
		}

		switch kind {
		case wire.KindItem:
			seen++
			if seen > MaxChanges {
				return taken, fmt.Errorf("more than %d changes in one meeting", MaxChanges)
			}
			weight += len(body)

			c, err := history.Decode(body)
			if err != nil {
				return taken, fmt.Errorf("reading a change: %w", err)
			}
			// Already here is asked first. A change this log holds can be built on whoever sent it
			// again, and counting it against a rule that has since narrowed would strand
			// everything made after it.
			if l.Has(c.ID()) {
				continue
			}
			if weight > MaxBytes {
				out.More = true
			}
			if admits == nil || !admits(c.Author) || behind(over, c.Heads) || weight > MaxBytes {
				over[c.ID()] = true
				out.Refused++
				continue
			}
			if _, err := l.Add(c); err != nil {
				over[c.ID()] = true
				out.Refused++
				if out.Trouble == nil {
					out.Trouble = err
				}
				continue
			}
			taken++

		case wire.KindEnd:
			end, err := wire.DecodeEnd(body)
			if err != nil {
				return taken, err
			}
			out.More = out.More || end.Size > int64(seen)
			return taken, nil

		default:
			return taken, fmt.Errorf("frame kind %d in a meeting", kind)
		}
	}
}

// behind reports whether a change names one that was passed over.
func behind(over map[history.ID]bool, heads []history.ID) bool {
	for _, head := range heads {
		if over[head] {
			return true
		}
	}
	return false
}

func writeHeads(conn *wire.Conn, heads []history.ID) error {
	if len(heads) > history.MaxHeads {
		return fmt.Errorf("saying what is here: %d changes, over the %d limit", len(heads), history.MaxHeads)
	}

	w := wire.NewWriter()
	w.Uint(uint64(len(heads)))
	for _, head := range heads {
		w.Bytes(head[:])
	}
	if err := conn.WriteFrame(wire.KindItem, w.Body()); err != nil {
		return fmt.Errorf("saying what is here: %w", err)
	}
	return nil
}

func readHeads(conn *wire.Conn) ([]history.ID, error) {
	kind, body, err := conn.ReadFrame()
	if err != nil {
		return nil, fmt.Errorf("reading what is there: %w", err)
	}
	if kind != wire.KindItem {
		return nil, fmt.Errorf("expected what is there, got frame kind %d", kind)
	}

	r := wire.NewReader(body)
	count, err := r.Uint()
	if err != nil {
		return nil, err
	}
	if count > history.MaxHeads {
		return nil, fmt.Errorf("the far end named %d changes, over the %d limit", count, history.MaxHeads)
	}

	out := make([]history.ID, 0, wire.Hint(count, body, len(history.ID{})+1))
	for range count {
		head, err := r.Bytes(len(history.ID{}))
		if err != nil {
			return nil, err
		}
		if len(head) != len(history.ID{}) {
			return nil, fmt.Errorf("the far end named a change of %d bytes", len(head))
		}
		out = append(out, history.ID(head))
	}
	return out, nil
}
