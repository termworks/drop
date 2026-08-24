package cmd

import (
	"context"
	"math/rand"
	"time"

	"github.com/bresilla/drop/src/pkg/book"
	"github.com/bresilla/drop/src/pkg/dial"
	"github.com/bresilla/drop/src/pkg/node"
)

// The daemon stays connected to every device it has paired with.
//
// Finding a device costs seconds: a rendezvous lookup, a relay session, a handshake. Doing it when
// somebody sends a message means every message waits for it. The thing that is always running is
// the one that should pay it, once, in advance — so that by the time anybody types anything the
// connection is already there.
//
// The interface does the opposite and connects to a device when it is entered, because looking at
// a list of names is not a reason to wake five relays.

const (
	// howOftenToCheck is how often the daemon looks at what it is holding.
	howOftenToCheck = 15 * time.Second
	// firstRetry is how long to wait after a device fails to answer, doubling from there.
	firstRetry = 5 * time.Second
	// slowestRetry is where the doubling stops. A device that is off is off, and asking every
	// second costs a relay connection and somebody's battery for nothing.
	slowestRetry = 5 * time.Minute
)

// staying reconnects to paired devices and keeps the connections up.
type staying struct {
	held *dial.Kept
	// next is when a device is worth trying again, and how long to wait after that.
	next map[string]time.Time
	wait map[string]time.Duration
}

func keepConnected(ctx context.Context, held *dial.Kept, pinned *book.Book) {
	s := &staying{
		held: held,
		next: map[string]time.Time{},
		wait: map[string]time.Duration{},
	}

	tick := time.NewTicker(howOftenToCheck)
	defer tick.Stop()

	for {
		s.round(ctx, pinned)

		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// round tries whatever is due.
func (s *staying) round(ctx context.Context, pinned *book.Book) {
	// A device paired since this started has a connection worth holding too.
	_ = pinned.Refresh()

	for _, entry := range pinned.Paired() {
		select {
		case <-ctx.Done():
			return
		default:
		}

		if !s.due(entry) {
			continue
		}
		s.reach(ctx, entry)
	}
}

// due reports whether a device is worth trying now.
func (s *staying) due(entry book.Entry) bool {
	at, waiting := s.next[entry.Name]
	return !waiting || time.Now().After(at)
}

// reach opens a connection if there is not one, and notes how it went.
func (s *staying) reach(ctx context.Context, entry book.Entry) {
	// A stream is what proves a connection is real: one that exists but cannot carry anything is
	// not a connection, and asking for a stream is what notices.
	ask, stop := context.WithTimeout(ctx, 30*time.Second)
	defer stop()

	stream, err := s.held.To(ask, entry, node.ALPNHello)
	if err != nil {
		s.backOff(entry)
		return
	}
	stream.Close()

	delete(s.next, entry.Name)
	delete(s.wait, entry.Name)
}

// backOff puts a device that did not answer further out of reach.
func (s *staying) backOff(entry book.Entry) {
	was := s.wait[entry.Name]
	switch {
	case was == 0:
		was = firstRetry
	case was < slowestRetry:
		was *= 2
	}
	if was > slowestRetry {
		was = slowestRetry
	}

	// Scattered a little, so a machine that wakes up with six devices to reach does not dial all
	// six on the same second for the rest of the day.
	spread := time.Duration(rand.Int63n(int64(was / 4)))

	s.wait[entry.Name] = was
	s.next[entry.Name] = time.Now().Add(was + spread)
}
