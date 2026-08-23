// Package web serves drop to a browser on this machine.
//
// A bridge rather than a peer: a browser cannot dial QUIC to another device, so it talks to this
// server over loopback and this server talks drop. That is also why it binds 127.0.0.1 — the pages
// it serves act as this node, so reaching them is the same as being at the keyboard.
package web

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"path/filepath"
	"sync"
	"time"

	"github.com/bresilla/drop/src/pkg/book"
	"github.com/bresilla/drop/src/pkg/convo"
)

//go:embed assets
var assets embed.FS

// Sender is what the bridge calls to put something on the wire. The web layer does not know how
// drop reaches a peer, which keeps the transport out of the browser's business entirely.
type Sender interface {
	Say(ctx context.Context, to book.Entry, kind byte, body string) error
	SendFile(ctx context.Context, to book.Entry, path, name string, size int64, body io.Reader) error
	Watch(ctx context.Context, to book.Entry, path string, into Terminal) error
	Spaces(ctx context.Context, to book.Entry) ([]Space, error)
	Self(ctx context.Context) (Identity, error)
}

// Server is the bridge.
type Server struct {
	send Sender

	// open is set when the bridge was deliberately bound somewhere other than loopback.
	open bool

	mu       sync.Mutex
	watchers map[chan convo.Message]struct{}
}

func New(send Sender) *Server {
	return &Server{send: send, watchers: map[chan convo.Message]struct{}{}}
}

// Arrived tells every open page about a message, so what a peer sends appears without a reload.
func (s *Server) Arrived(m convo.Message) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for ch := range s.watchers {
		select {
		case ch <- m:
		default:
			// A page that is not reading is not worth stalling the network for.
		}
	}
}

// Handler is the whole site: the page, and the calls it makes.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/self", s.self)
	mux.HandleFunc("GET /api/peers", s.peers)
	mux.HandleFunc("GET /api/log/{peer}", s.log)
	mux.HandleFunc("POST /api/say", s.say)
	mux.HandleFunc("POST /api/send", s.sendFile)
	mux.HandleFunc("GET /api/events", s.events)
	mux.HandleFunc("GET /api/spaces/{peer}", s.spaces)
	mux.HandleFunc("GET /api/watch/{peer}/{path...}", s.watch)

	pages, err := fs.Sub(assets, "assets")
	if err != nil {
		panic(fmt.Sprintf("web assets are missing: %v", err))
	}
	mux.Handle("GET /", http.FileServerFS(pages))

	return s.guard(mux)
}

// guard turns away a request that did not come from this machine, unless the bridge was told to
// answer the network.
//
// The page acts as this node: it can read every conversation and send as you. A stray binding or a
// helpful reverse proxy would hand that to the network, so the check is here rather than resting
// on the listener address alone. AllowRemote is the one way past it, and nothing sets it by
// accident.
func (s *Server) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.open && !localRequest(r) {
			http.Error(w, "drop's web bridge only answers this machine", http.StatusForbidden)
			return
		}
		// The bridge is not a website and nothing should embed it.
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")

		next.ServeHTTP(w, r)
	})
}

func (s *Server) peers(w http.ResponseWriter, r *http.Request) {
	pinned, err := book.Load()
	if err != nil {
		fail(w, err)
		return
	}

	type peer struct {
		Name   string `json:"name"`
		ID     string `json:"id"`
		Paired bool   `json:"paired"`
		Unread int    `json:"unread"`
	}

	out := []peer{}
	for _, e := range pinned.All() {
		waiting := 0
		if store, err := convo.Open(e.ID); err == nil {
			if pending, err := store.Pending(); err == nil {
				waiting = len(pending)
			}
		}
		out = append(out, peer{Name: e.Name, ID: e.ID.String(), Paired: e.Paired(), Unread: waiting})
	}
	reply(w, out)
}

func (s *Server) log(w http.ResponseWriter, r *http.Request) {
	entry, err := book.Resolve(r.PathValue("peer"))
	if err != nil {
		fail(w, err)
		return
	}
	store, err := convo.Open(entry.ID)
	if err != nil {
		fail(w, err)
		return
	}
	history, err := store.History()
	if err != nil {
		fail(w, err)
		return
	}

	reply(w, shown(history))
}

// message is one entry as a page sees it.
type message struct {
	ID    string `json:"id"`
	Kind  string `json:"kind"`
	Mine  bool   `json:"mine"`
	Body  string `json:"body"`
	Extra string `json:"extra"`
	At    int64  `json:"at"`
}

func shown(history []convo.Message) []message {
	out := make([]message, 0, len(history))
	for _, m := range history {
		out = append(out, message{
			ID: m.ID, Kind: kindName(m.Kind), Mine: m.Dir == convo.Out,
			Body: m.Body, Extra: m.Extra, At: m.At,
		})
	}
	return out
}

func kindName(kind byte) string {
	switch kind {
	case convo.KindLink:
		return "link"
	case convo.KindFile:
		return "file"
	case convo.KindEvent:
		return "event"
	default:
		return "text"
	}
}

func (s *Server) say(w http.ResponseWriter, r *http.Request) {
	var asked struct {
		To   string `json:"to"`
		Body string `json:"body"`
		Kind string `json:"kind"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&asked); err != nil {
		fail(w, err)
		return
	}

	entry, err := book.Resolve(asked.To)
	if err != nil {
		fail(w, err)
		return
	}

	kind := convo.KindText
	if asked.Kind == "link" {
		kind = convo.KindLink
	}

	// Queued by the sender, so a peer that is asleep is not an error the page has to explain.
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()

	if err := s.send.Say(ctx, entry, kind, asked.Body); err != nil {
		fail(w, err)
		return
	}
	reply(w, map[string]bool{"ok": true})
}

// events is the live half: what arrives while a page is open.
func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	ch := make(chan convo.Message, 16)
	s.mu.Lock()
	s.watchers[ch] = struct{}{}
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		delete(s.watchers, ch)
		s.mu.Unlock()
	}()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	flusher.Flush()

	// A comment every so often, so a proxy or a sleeping tab does not quietly drop the connection.
	beat := time.NewTicker(30 * time.Second)
	defer beat.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-beat.C:
			fmt.Fprint(w, ": still here\n\n")
			flusher.Flush()
		case m := <-ch:
			body, err := json.Marshal(shown([]convo.Message{m})[0])
			if err != nil {
				continue
			}
			fmt.Fprintf(w, "data: %s\n\n", body)
			flusher.Flush()
		}
	}
}

func reply(w http.ResponseWriter, body any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(body)
}

func fail(w http.ResponseWriter, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}

// MaxUpload bounds one upload. Large enough for what a person drags onto a page, small enough that
// a runaway request cannot fill the disk while it is being buffered.
const MaxUpload = 2 << 30

// inMemory is how much of an upload is held in memory before the rest goes to a temporary file.
const inMemory = 8 << 20

// sendFile takes a file the page offered and puts it on the wire.
func (s *Server) sendFile(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, MaxUpload)
	if err := r.ParseMultipartForm(inMemory); err != nil {
		fail(w, fmt.Errorf("reading the upload: %w", err))
		return
	}
	defer func() {
		if r.MultipartForm != nil {
			_ = r.MultipartForm.RemoveAll()
		}
	}()

	entry, err := book.Resolve(r.FormValue("to"))
	if err != nil {
		fail(w, err)
		return
	}

	file, head, err := r.FormFile("file")
	if err != nil {
		fail(w, fmt.Errorf("no file in the upload: %w", err))
		return
	}
	defer file.Close()

	// The name is the browser's, so only its last element is kept: a page must not choose where on
	// the far machine the bytes land any more than a peer may.
	name := filepath.Base(filepath.Clean("/" + head.Filename))
	if name == "/" || name == "." || name == ".." {
		fail(w, fmt.Errorf("that file has no usable name"))
		return
	}

	// No timeout of its own: a large file over a slow link is the ordinary case, and the request
	// being cancelled is what should stop it.
	at := r.FormValue("path")
	if at == "" {
		at = "/inbox"
	}

	if err := s.send.SendFile(r.Context(), entry, at, name, head.Size, file); err != nil {
		fail(w, err)
		return
	}
	reply(w, map[string]any{"ok": true, "name": name, "size": head.Size})
}

// AllowRemote lets the bridge answer machines other than this one.
//
// Nothing else about the page changes: it still acts as this node, with nothing asked of whoever
// opens it. Anyone who can reach the address can read every conversation, send as you, and watch a
// terminal. It exists because a phone cannot reach loopback, and it is off unless asked for.
func (s *Server) AllowRemote() { s.open = true }
