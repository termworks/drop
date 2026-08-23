package web

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/bresilla/drop/src/pkg/book"
	"github.com/bresilla/drop/src/pkg/term"
)

// FrameEvery is how often a watcher is sent what changed.
//
// Output arrives far faster than a screen refreshes. Sending on every read would be thousands of
// frames a second for a scrolling build log; at this rate the watcher sees the same thing.
const FrameEvery = 50 * time.Millisecond

// Terminal is what a watch draws into.
type Terminal interface {
	io.Writer
	Resize(cols, rows int)
}

// viewer keeps the screen for one watcher and sends it what changed.
//
// The screen lives here rather than in the page: the parsing is the part that goes wrong, and it
// belongs somewhere it can be tested. What reaches the browser is a list of styled strings.
type viewer struct {
	mu      sync.Mutex
	screen  *term.Screen
	painter *term.Painter
	dirty   bool

	w       http.ResponseWriter
	flusher http.Flusher
}

func newViewer(w http.ResponseWriter, flusher http.Flusher) *viewer {
	return &viewer{
		screen:  term.New(80, 24),
		painter: term.NewPainter(),
		w:       w,
		flusher: flusher,
	}
}

func (v *viewer) Write(p []byte) (int, error) {
	v.mu.Lock()
	defer v.mu.Unlock()

	n, err := v.screen.Write(p)
	v.dirty = true
	return n, err
}

func (v *viewer) Resize(cols, rows int) {
	v.mu.Lock()
	defer v.mu.Unlock()

	v.screen.Resize(cols, rows)
	v.dirty = true
}

// flush sends what changed, and reports whether the connection is still good.
func (v *viewer) flush() error {
	v.mu.Lock()
	if !v.dirty {
		v.mu.Unlock()
		return nil
	}
	frame := v.painter.Frame(v.screen)
	v.dirty = false
	v.mu.Unlock()

	if frame.Empty() {
		return nil
	}

	body, err := json.Marshal(frame)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(v.w, "data: %s\n\n", body); err != nil {
		return err
	}
	v.flusher.Flush()
	return nil
}

// watch streams a namespace on another device to the page.
func (s *Server) watch(w http.ResponseWriter, r *http.Request) {
	entry, err := book.Resolve(r.PathValue("peer"))
	if err != nil {
		fail(w, err)
		return
	}

	path := "/" + strings.Trim(r.PathValue("path"), "/")

	flusher, ok := w.(http.Flusher)
	if !ok {
		fail(w, fmt.Errorf("this connection cannot stream"))
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	v := newViewer(w, flusher)

	done := make(chan error, 1)
	go func() {
		// The request context ending is what stops the watch, so closing the tab closes the
		// stream rather than leaving a terminal being read by nobody.
		done <- s.send.Watch(r.Context(), entry, path, v)
	}()

	tick := time.NewTicker(FrameEvery)
	defer tick.Stop()

	for {
		select {
		case err := <-done:
			// A last frame, so whatever arrived just before the end is still drawn.
			_ = v.flush()
			if err != nil && r.Context().Err() == nil {
				fmt.Fprintf(w, "event: gone\ndata: %s\n\n", quoted(err.Error()))
				flusher.Flush()
			}
			return

		case <-tick.C:
			if err := v.flush(); err != nil {
				return
			}

		case <-r.Context().Done():
			return
		}
	}
}

// quoted renders a reason as a JSON string, so a message containing a newline cannot break the
// event it travels in.
func quoted(reason string) string {
	body, err := json.Marshal(reason)
	if err != nil {
		return `"the watch ended"`
	}
	return string(body)
}
