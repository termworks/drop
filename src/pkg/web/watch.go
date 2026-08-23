package web

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"

	"github.com/bresilla/drop/src/pkg/book"
)

// frames writes terminal bytes to the page as server-sent events.
//
// The bytes are base64 encoded rather than sent as they are. Terminal output carries newlines
// constantly and an event is delimited by one, so raw output would be read as a stream of truncated
// events. Encoding costs a third more bytes and removes the whole class of problem.
type frames struct {
	w       http.ResponseWriter
	flusher http.Flusher
}

func (f frames) Write(p []byte) (int, error) {
	if _, err := fmt.Fprintf(f.w, "data: %s\n\n", base64.StdEncoding.EncodeToString(p)); err != nil {
		return 0, err
	}
	f.flusher.Flush()
	return len(p), nil
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

	out := frames{w: w, flusher: flusher}

	// The request context ending is what stops the watch, so closing the tab closes the stream
	// rather than leaving a terminal being read by nobody.
	if err := s.send.Watch(r.Context(), entry, path, out); err != nil && r.Context().Err() == nil {
		fmt.Fprintf(w, "event: gone\ndata: %s\n\n", base64.StdEncoding.EncodeToString([]byte(err.Error())))
		flusher.Flush()
	}
}
