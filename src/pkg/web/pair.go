package web

import (
	"context"
	"net/http"
	"sync"
)

// offering is a pairing this node is currently open to.
//
// One at a time: a second code offered while the first is up would mean two ways in for whoever is
// watching, and there is only ever one person standing at the other device.
type offering struct {
	mu     sync.Mutex
	ticket string
	with   string
	stop   context.CancelFunc
}

func (o *offering) begin(ticket string, stop context.CancelFunc) {
	o.mu.Lock()
	defer o.mu.Unlock()

	if o.stop != nil {
		o.stop()
	}
	o.ticket, o.with, o.stop = ticket, "", stop
}

func (o *offering) finished(with string) {
	o.mu.Lock()
	defer o.mu.Unlock()

	o.with = with
}

func (o *offering) state() (ticket, with string) {
	o.mu.Lock()
	defer o.mu.Unlock()

	return o.ticket, o.with
}

func (o *offering) end() {
	o.mu.Lock()
	defer o.mu.Unlock()

	if o.stop != nil {
		o.stop()
		o.stop = nil
	}
	o.ticket, o.with = "", ""
}

// pair opens this device to a pairing and hands back the ticket to show.
//
// The ticket comes back at once rather than when the pairing completes: it is the thing that has to
// be on screen for the other device to use, so waiting for the result would mean showing nothing
// during the only moment it matters.
func (s *Server) pair(w http.ResponseWriter, r *http.Request) {
	ctx, stop := context.WithCancel(context.Background())

	ticket, done, err := s.send.Offer(ctx)
	if err != nil {
		stop()
		fail(w, err)
		return
	}
	s.offer.begin(ticket, stop)

	go func() {
		with, ok := <-done
		if ok {
			s.offer.finished(with)
		}
	}()

	reply(w, map[string]string{"ticket": ticket})
}

// pairing says how it is going, so the page can show the ticket until it is used and then move on.
func (s *Server) pairing(w http.ResponseWriter, r *http.Request) {
	ticket, with := s.offer.state()
	reply(w, map[string]string{"ticket": ticket, "with": with})
}

// unpair takes the offer down, so a code shown by mistake stops being one.
func (s *Server) unpair(w http.ResponseWriter, r *http.Request) {
	s.offer.end()
	reply(w, map[string]bool{"ok": true})
}
