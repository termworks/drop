package mobile

import (
	"context"
	"fmt"
	"time"

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
