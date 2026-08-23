package web

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"sync"
	"time"
)

// Shared is something the phone handed over: a link, a note, or a file.
//
// It is held rather than sent, because the share sheet says what was shared but not who it is for.
// The page asks that, which is the one question the phone cannot answer.
type Shared struct {
	Title string `json:"title,omitempty"`
	Text  string `json:"text,omitempty"`
	URL   string `json:"url,omitempty"`
	Name  string `json:"name,omitempty"`
	Size  int64  `json:"size,omitempty"`

	body []byte
	at   time.Time
}

// ShareKeeps is how long a share waits to be claimed. Long enough to choose a device, short enough
// that a phone that shared something and wandered off does not leave it sitting in memory.
const ShareKeeps = 10 * time.Minute

// MaxShare bounds what the share sheet may hand over in one go.
const MaxShare = 512 << 20

type shares struct {
	mu   sync.Mutex
	held map[string]*Shared
}

func newShares() *shares { return &shares{held: map[string]*Shared{}} }

// keep files a share and returns the token that claims it.
func (s *shares) keep(item *Shared) (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("naming the share: %w", err)
	}
	token := base64.RawURLEncoding.EncodeToString(raw[:])

	s.mu.Lock()
	defer s.mu.Unlock()

	s.forget()
	item.at = time.Now()
	s.held[token] = item

	return token, nil
}

// claim hands a share over once. Once, because the page reloading should not re-send a file, and a
// token that stayed valid would be a link that quietly resends every time it is opened.
func (s *shares) claim(token string) (*Shared, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.forget()
	item, ok := s.held[token]
	if ok {
		delete(s.held, token)
	}
	return item, ok
}

// forget drops what nobody came back for. Called under the lock, on every use, so nothing needs to
// run on a timer.
func (s *shares) forget() {
	for token, item := range s.held {
		if time.Since(item.at) > ShareKeeps {
			delete(s.held, token)
		}
	}
}

// share is where Android's share sheet posts.
//
// It answers with a redirect rather than a page: the phone has left its own app to get here, and
// what should happen next is the page opening with the thing already in hand.
func (s *Server) share(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, MaxShare)

	item := &Shared{}

	if err := r.ParseMultipartForm(inMemory); err == nil {
		defer func() {
			if r.MultipartForm != nil {
				_ = r.MultipartForm.RemoveAll()
			}
		}()

		item.Title = r.FormValue("title")
		item.Text = r.FormValue("text")
		item.URL = r.FormValue("url")

		if file, head, err := r.FormFile("file"); err == nil {
			defer file.Close()

			body, err := io.ReadAll(file)
			if err != nil {
				fail(w, fmt.Errorf("reading what was shared: %w", err))
				return
			}
			item.Name, item.Size, item.body = onlyName(head.Filename), head.Size, body
		}
	} else {
		// A share sheet may post a plain form rather than multipart.
		item.Title = r.FormValue("title")
		item.Text = r.FormValue("text")
		item.URL = r.FormValue("url")
	}

	if item.Title == "" && item.Text == "" && item.URL == "" && item.Name == "" {
		fail(w, fmt.Errorf("nothing was shared"))
		return
	}

	token, err := s.shares.keep(item)
	if err != nil {
		fail(w, err)
		return
	}

	http.Redirect(w, r, "/?shared="+token, http.StatusSeeOther)
}

// shared hands the page what the phone gave up, so it can ask where it should go.
func (s *Server) shared(w http.ResponseWriter, r *http.Request) {
	item, ok := s.shares.claim(r.PathValue("token"))
	if !ok {
		fail(w, fmt.Errorf("that share has already been taken, or it waited too long"))
		return
	}

	s.mu.Lock()
	s.claimed = item
	s.mu.Unlock()

	reply(w, item)
}

// onlyName strips any directory a phone put in a name. The share sheet is not supposed to send one,
// which is exactly why it is worth not trusting.
func onlyName(name string) string {
	clean := filepath.Base(filepath.Clean("/" + name))
	if clean == "/" || clean == "." || clean == ".." {
		return "shared"
	}
	return clean
}

// Body is what was shared, for sending on.
func (s *Shared) Body() []byte { return s.body }
