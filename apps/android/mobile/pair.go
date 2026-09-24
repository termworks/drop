package mobile

import (
	"context"
	"fmt"
	"time"

	"github.com/bresilla/drop/src/cmd"
	tickets "github.com/bresilla/drop/src/pkg/ticket"
)

// offerFor is how long a code stays up when nobody takes it.
const offerFor = 10 * time.Minute

// Offer puts this device up for pairing and hands back the ticket to show. Paired is called once
// somebody takes it; a second offer replaces the first.
func (n *Node) Offer() (string, error) {
	n.mu.Lock()
	defer n.mu.Unlock()

	if n.offer != nil {
		n.offer()
	}
	ctx, cancel := context.WithTimeout(n.ctx, offerFor)

	ticket, done, err := n.back.Offer(ctx)
	if err != nil {
		cancel()
		return "", err
	}
	n.offer = cancel

	go func() {
		defer cancel()
		select {
		case <-ctx.Done():
		case with := <-done:
			if n.events != nil {
				n.events.Paired(with)
			}
			changed(n.events)
		}
	}()
	return ticket, nil
}

// StopOffer takes a code back down before anybody has taken it.
func (n *Node) StopOffer() {
	n.mu.Lock()
	defer n.mu.Unlock()

	if n.offer != nil {
		n.offer()
		n.offer = nil
	}
}

// Join takes a ticket another device is showing — typed, pasted, or read off its screen by the
// camera — and says what the other device is now called here.
func (n *Node) Join(ticket string) (string, error) {
	ctx, cancel := context.WithTimeout(n.ctx, time.Minute)
	defer cancel()

	with, err := n.back.Join(ctx, tickets.FromLink(ticket))
	changed(n.events)
	return with, err
}

// Code draws a ticket as a QR code, as a PNG, each module scale pixels across.
func Code(ticket string, scale int) ([]byte, error) {
	code, err := tickets.Code(ticket)
	if err != nil {
		return nil, err
	}
	if scale < 1 || scale > 64 {
		return nil, fmt.Errorf("a scale of %d is not a size to draw at", scale)
	}
	code.Scale = scale
	return code.PNG(), nil
}

// Take makes this phone one of somebody's machines, from a code a computer of theirs showed —
// `drop me user vouch` or `drop me user export` — and says what it now is. The node wears it from
// its next start.
func Take(code string) (string, error) { return cmd.TakeCode(code) }

// JoinMachine makes this phone one of the machines of whoever is showing a code — `drop machine
// add` on a computer of theirs, typed, pasted or scanned — and says what that computer is called.
func (n *Node) JoinMachine(code string) (string, error) {
	more, err := n.more()
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(n.ctx, time.Minute)
	defer cancel()

	with, err := more.JoinMachine(ctx, code)
	changed(n.events)
	return with, err
}

// OfferMachine shows a code a computer joins this phone's user with, `drop machine join <code>`, and
// hands back the ticket. Paired is called once it has; a second offer replaces the first.
func (n *Node) OfferMachine() (string, error) {
	more, err := n.more()
	if err != nil {
		return "", err
	}

	n.mu.Lock()
	defer n.mu.Unlock()

	if n.offer != nil {
		n.offer()
	}
	ctx, cancel := context.WithTimeout(n.ctx, offerFor)

	ticket, done, err := more.OfferMachine(ctx)
	if err != nil {
		cancel()
		return "", err
	}
	n.offer = cancel

	go func() {
		defer cancel()
		select {
		case <-ctx.Done():
		case with := <-done:
			if n.events != nil {
				n.events.Paired(with)
			}
			changed(n.events)
		}
	}()
	return ticket, nil
}

// MachineCode draws a ticket for joining as a machine of one's own, as a PNG, each module scale
// pixels across.
func MachineCode(ticket string, scale int) ([]byte, error) {
	code, err := tickets.CodeOf(tickets.LinkAs(tickets.KindMachine, ticket))
	if err != nil {
		return nil, err
	}
	if scale < 1 || scale > 64 {
		return nil, fmt.Errorf("a scale of %d is not a size to draw at", scale)
	}
	code.Scale = scale
	return code.PNG(), nil
}
