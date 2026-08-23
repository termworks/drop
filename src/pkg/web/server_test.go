package web

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tmc/go-iroh/key"

	"github.com/bresilla/drop/src/pkg/book"
	"github.com/bresilla/drop/src/pkg/convo"
	"github.com/bresilla/drop/src/pkg/node"
)

// sent records what the bridge was asked to put on the wire, so a handler can be checked without
// a network.
type sent struct {
	to   book.Entry
	kind byte
	body string
	err  error
}

// upload records a file the bridge was handed.
// watched records the namespace the bridge was asked to read.
type watched struct {
	to   book.Entry
	path string
}

type upload struct {
	to   book.Entry
	name string
	at   string
	size int64
	body []byte
}

type stub struct {
	watched   *watched
	watchErr  error
	watchBody string
	serves    []Space
	spacesErr error
	asked     *book.Entry
	watchCols int
	watchRows int
	last      *sent
	file      *upload
	fileErr   error
}

func (s *stub) Say(ctx context.Context, to book.Entry, kind byte, body string) error {
	s.last = &sent{to: to, kind: kind, body: body}
	return s.err()
}

func (s *stub) SendFile(ctx context.Context, to book.Entry, path, name string, size int64, body io.Reader) error {
	if s.fileErr != nil {
		return s.fileErr
	}
	read, err := io.ReadAll(body)
	if err != nil {
		return err
	}
	s.file = &upload{to: to, at: path, name: name, size: size, body: read}
	return nil
}

func (s *stub) Spaces(ctx context.Context, to book.Entry) ([]Space, error) {
	s.asked = &to
	return s.serves, s.spacesErr
}

func (s *stub) err() error { return nil }

func testID(t *testing.T, seed byte) node.ID {
	t.Helper()

	var raw [32]byte
	for i := range raw {
		raw[i] = seed
	}
	return key.NewSecretKey(raw).Public().EndpointID()
}

// paired sets up a config and data directory holding one paired device.
func paired(t *testing.T, name string) node.ID {
	t.Helper()

	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	id := testID(t, 3)
	b, err := book.Load()
	if err != nil {
		t.Fatalf("book.Load(): %v", err)
	}
	b.Pair(name, id, make([]byte, book.SecretBytes))
	if err := b.Save(); err != nil {
		t.Fatalf("book.Save(): %v", err)
	}
	return id
}

func call(t *testing.T, h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()

	r := httptest.NewRequest(method, path, stringReader(body))
	r.RemoteAddr = "127.0.0.1:5000"
	r.Header.Set("Content-Type", "application/json")

	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestPeersListsTheBook(t *testing.T) {
	paired(t, "laptop")

	w := call(t, New(&stub{}).Handler(), http.MethodGet, "/api/peers", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body)
	}

	var out []struct {
		Name   string `json:"name"`
		Paired bool   `json:"paired"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if len(out) != 1 || out[0].Name != "laptop" || !out[0].Paired {
		t.Fatalf("peers = %+v", out)
	}
}

// A bare URL is sent as a link, which is what makes it openable on the far side.
func TestSayCarriesTheKindThrough(t *testing.T) {
	paired(t, "laptop")

	send := &stub{}
	w := call(t, New(send).Handler(), http.MethodPost, "/api/say",
		`{"to":"laptop","body":"https://example.com","kind":"link"}`)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body)
	}
	if send.last == nil {
		t.Fatal("nothing was sent")
	}
	if send.last.kind != convo.KindLink {
		t.Fatalf("kind = %d, want a link", send.last.kind)
	}
	if send.last.body != "https://example.com" {
		t.Fatalf("body = %q", send.last.body)
	}
}

func TestSayRefusesAnUnknownPeer(t *testing.T) {
	paired(t, "laptop")

	w := call(t, New(&stub{}).Handler(), http.MethodPost, "/api/say",
		`{"to":"nobody-by-that-name","body":"hello"}`)

	if w.Code == http.StatusOK {
		t.Fatal("sending to an unknown peer was accepted")
	}
}

// The log is what a page renders, so what went in has to come back unchanged: a body is data, and
// nothing along the way may interpret it.
func TestLogReturnsBodiesVerbatim(t *testing.T) {
	id := paired(t, "laptop")

	store, err := convo.Open(id)
	if err != nil {
		t.Fatalf("convo.Open(): %v", err)
	}
	nasty := `<script>alert(1)</script> & "quotes" 'and' <b>`
	m, err := convo.New(convo.KindText, nasty, "")
	if err != nil {
		t.Fatalf("convo.New(): %v", err)
	}
	if _, err := store.Add(m); err != nil {
		t.Fatalf("Add(): %v", err)
	}

	w := call(t, New(&stub{}).Handler(), http.MethodGet, "/api/log/laptop", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body)
	}

	var out []message
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("log = %+v", out)
	}
	if out[0].Body != nasty {
		t.Fatalf("body came back as %q, want it unchanged", out[0].Body)
	}
}

// A page that is not reading must not stall the network: Arrived drops rather than blocks.
func TestArrivedDoesNotBlockOnAFullWatcher(t *testing.T) {
	s := New(&stub{})

	stuck := make(chan convo.Message) // unbuffered and nobody reading
	s.mu.Lock()
	s.watchers[stuck] = struct{}{}
	s.mu.Unlock()

	done := make(chan struct{})
	go func() {
		s.Arrived(convo.Message{Body: "nobody is listening"})
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Arrived() blocked on a watcher that was not reading")
	}
}

func TestKindNames(t *testing.T) {
	for kind, want := range map[byte]string{
		convo.KindText:  "text",
		convo.KindLink:  "link",
		convo.KindFile:  "file",
		convo.KindEvent: "event",
	} {
		if got := kindName(kind); got != want {
			t.Errorf("kindName(%d) = %q, want %q", kind, got, want)
		}
	}
}

// stringReader keeps the call helper readable; http.NoBody is not a *strings.Reader.
func stringReader(s string) *strings.Reader {
	return strings.NewReader(s)
}

// upload builds a multipart request the way a browser would.
func uploadTo(t *testing.T, h http.Handler, to, filename, content string) (*httptest.ResponseRecorder, *stub) {
	t.Helper()
	return uploadWith(t, h, &stub{}, to, filename, content)
}

func uploadWith(t *testing.T, h http.Handler, send *stub, to, filename, content string) (*httptest.ResponseRecorder, *stub) {
	t.Helper()

	var buf bytes.Buffer
	form := multipart.NewWriter(&buf)
	if err := form.WriteField("to", to); err != nil {
		t.Fatalf("writing the field: %v", err)
	}
	part, err := form.CreateFormFile("file", filename)
	if err != nil {
		t.Fatalf("creating the part: %v", err)
	}
	if _, err := part.Write([]byte(content)); err != nil {
		t.Fatalf("writing the part: %v", err)
	}
	form.Close()

	r := httptest.NewRequest(http.MethodPost, "/api/send", &buf)
	r.RemoteAddr = "127.0.0.1:5000"
	r.Header.Set("Content-Type", form.FormDataContentType())

	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w, send
}

func TestSendFileReachesTheWireIntact(t *testing.T) {
	paired(t, "laptop")

	send := &stub{}
	w, _ := uploadWith(t, New(send).Handler(), send, "laptop", "notes.txt", "the bytes as given")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body)
	}
	if send.file == nil {
		t.Fatal("nothing reached the bridge")
	}
	if got := string(send.file.body); got != "the bytes as given" {
		t.Fatalf("body = %q", got)
	}
	if send.file.name != "notes.txt" {
		t.Fatalf("name = %q", send.file.name)
	}
	// A wrong size is not cosmetic: the far end sizes its resume window from it.
	if send.file.size != int64(len("the bytes as given")) {
		t.Fatalf("size = %d, want %d", send.file.size, len("the bytes as given"))
	}
}

// The filename comes from the browser, so it is untrusted in exactly the way a peer's is. A page
// must not be able to choose where on the far machine the bytes land.
func TestSendFileStripsAPathFromTheName(t *testing.T) {
	cases := []struct {
		given string
		want  string
	}{
		{"../../.ssh/authorized_keys", "authorized_keys"},
		{"/etc/passwd", "passwd"},
		{`..\..\Windows\System32\evil.dll`, `..\..\Windows\System32\evil.dll`},
		{"plain.txt", "plain.txt"},
	}

	for _, c := range cases {
		t.Run(c.given, func(t *testing.T) {
			paired(t, "laptop")

			send := &stub{}
			w, _ := uploadWith(t, New(send).Handler(), send, "laptop", c.given, "x")
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d: %s", w.Code, w.Body)
			}
			if send.file == nil {
				t.Fatal("nothing reached the bridge")
			}
			if send.file.name != c.want {
				t.Fatalf("name = %q, want %q", send.file.name, c.want)
			}
			if strings.Contains(send.file.name, "/") {
				t.Fatalf("a separator survived: %q", send.file.name)
			}
		})
	}
}

func TestSendFileRefusesAnUnknownPeer(t *testing.T) {
	paired(t, "laptop")

	w, send := uploadTo(t, New(&stub{}).Handler(), "nobody", "notes.txt", "x")
	if w.Code == http.StatusOK {
		t.Fatalf("an unknown peer was accepted: %s", w.Body)
	}
	if send.file != nil {
		t.Fatal("bytes left the machine for an unknown peer")
	}
}

// A failure has to reach the page, because the row showing progress is otherwise left claiming a
// file went out when it did not.
func TestSendFileReportsAFailure(t *testing.T) {
	paired(t, "laptop")

	send := &stub{fileErr: errors.New("the peer is not reachable")}
	w, _ := uploadWith(t, New(send).Handler(), send, "laptop", "notes.txt", "x")
	if w.Code == http.StatusOK {
		t.Fatalf("a failed send reported success: %s", w.Body)
	}
	if !strings.Contains(w.Body.String(), "not reachable") {
		t.Fatalf("the reason did not reach the page: %s", w.Body)
	}
}

// The upload is not a local request, so the same rule that covers the rest of the API covers it.
func TestSendFileRefusesFromOffMachine(t *testing.T) {
	paired(t, "laptop")

	var buf bytes.Buffer
	form := multipart.NewWriter(&buf)
	form.WriteField("to", "laptop")
	part, _ := form.CreateFormFile("file", "notes.txt")
	part.Write([]byte("x"))
	form.Close()

	send := &stub{}
	r := httptest.NewRequest(http.MethodPost, "/api/send", &buf)
	r.RemoteAddr = "192.168.1.40:5000"
	r.Header.Set("Content-Type", form.FormDataContentType())

	w := httptest.NewRecorder()
	New(send).Handler().ServeHTTP(w, r)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403: %s", w.Code, w.Body)
	}
	if send.file != nil {
		t.Fatal("an off-machine request reached the wire")
	}
}

func (s *stub) Watch(ctx context.Context, to book.Entry, path string, into Terminal) error {
	s.watched = &watched{to: to, path: path}
	if s.watchErr != nil {
		return s.watchErr
	}
	if s.watchCols > 0 {
		into.Resize(s.watchCols, s.watchRows)
	}
	if s.watchBody != "" {
		if _, err := io.WriteString(into, s.watchBody); err != nil {
			return err
		}
	}
	return nil
}

func (s *stub) Self(ctx context.Context) (Identity, error) {
	return Identity{Name: "here", ID: "abc123", Addrs: []string{"192.168.1.10:47777"}}, nil
}
