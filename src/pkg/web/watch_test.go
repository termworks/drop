package web

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bresilla/drop/src/pkg/term"
)

func watchAt(t *testing.T, send *stub, path, from string) *httptest.ResponseRecorder {
	t.Helper()

	r := httptest.NewRequest(http.MethodGet, path, nil)
	r.RemoteAddr = from

	w := httptest.NewRecorder()
	New(send).Handler().ServeHTTP(w, r)
	return w
}

// frames pulls the frames out of an event stream.
func frames(t *testing.T, body string) []term.Frame {
	t.Helper()

	var out []term.Frame
	for _, line := range strings.Split(body, "\n") {
		data, ok := strings.CutPrefix(line, "data: ")
		if !ok {
			continue
		}
		var f term.Frame
		if err := json.Unmarshal([]byte(data), &f); err != nil {
			t.Fatalf("a frame did not decode: %v (%q)", err, data)
		}
		out = append(out, f)
	}
	return out
}

// What reaches the page is the screen, not the bytes that drew it. A watcher joining halfway
// through should see what the terminal looks like, and never has to interpret an escape.
func TestWatchSendsTheScreenNotTheBytes(t *testing.T) {
	paired(t, "laptop")

	send := &stub{watchBody: "\x1b[31mred\x1b[0m and plain"}
	w := watchAt(t, send, "/api/watch/laptop/tty", "127.0.0.1:5000")

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body)
	}
	if ct := w.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content-type = %q", ct)
	}

	body := w.Body.String()
	if strings.Contains(body, "\x1b") {
		t.Fatal("a raw escape reached the page")
	}

	got := frames(t, body)
	if len(got) == 0 {
		t.Fatalf("no frames: %q", body)
	}

	first := got[0]
	if len(first.Lines[0]) != 2 {
		t.Fatalf("row 0 = %+v, want a coloured run and a plain one", first.Lines[0])
	}
	if first.Lines[0][0].Text != "red" || first.Lines[0][0].FG != "var(--t1)" {
		t.Fatalf("first run = %+v", first.Lines[0][0])
	}
	if first.Lines[0][1].Text != " and plain" || first.Lines[0][1].FG != "" {
		t.Fatalf("second run = %+v", first.Lines[0][1])
	}
}

// A newline in the output must not end the event carrying it. JSON is what makes that safe, and it
// is worth asserting rather than assuming.
func TestWatchSurvivesNewlinesInOutput(t *testing.T) {
	paired(t, "laptop")

	send := &stub{watchBody: "first\r\nsecond\r\n\r\nthird"}
	w := watchAt(t, send, "/api/watch/laptop/tty", "127.0.0.1:5000")

	got := frames(t, w.Body.String())
	if len(got) == 0 {
		t.Fatalf("no frames: %q", w.Body)
	}

	last := got[len(got)-1]
	for at, want := range map[int]string{0: "first", 1: "second", 3: "third"} {
		if runs := last.Lines[at]; len(runs) == 0 || runs[0].Text != want {
			t.Fatalf("row %d = %+v, want %q", at, runs, want)
		}
	}
}

// The far end says how big its terminal is. Ignoring that wraps every line in the wrong column.
func TestWatchFollowsTheFarEndsSize(t *testing.T) {
	paired(t, "laptop")

	send := &stub{watchCols: 30, watchRows: 8, watchBody: "hi"}
	w := watchAt(t, send, "/api/watch/laptop/tty", "127.0.0.1:5000")

	got := frames(t, w.Body.String())
	if len(got) == 0 {
		t.Fatalf("no frames: %q", w.Body)
	}
	if got[0].Cols != 30 || got[0].Rows != 8 {
		t.Fatalf("size = %d,%d, want 30,8", got[0].Cols, got[0].Rows)
	}
}

func TestWatchPassesTheWholePath(t *testing.T) {
	for _, c := range []struct{ url, want string }{
		{"/api/watch/laptop/tty", "/tty"},
		{"/api/watch/laptop/stream/1", "/stream/1"},
		{"/api/watch/laptop/a/b/c", "/a/b/c"},
	} {
		t.Run(c.url, func(t *testing.T) {
			paired(t, "laptop")

			send := &stub{}
			watchAt(t, send, c.url, "127.0.0.1:5000")

			if send.watched == nil {
				t.Fatal("the bridge was never asked")
			}
			if send.watched.path != c.want {
				t.Fatalf("path = %q, want %q", send.watched.path, c.want)
			}
		})
	}
}

func TestWatchRefusesAnUnknownPeer(t *testing.T) {
	paired(t, "laptop")

	send := &stub{}
	w := watchAt(t, send, "/api/watch/nobody/tty", "127.0.0.1:5000")

	if w.Code == http.StatusOK {
		t.Fatalf("an unknown peer was accepted: %s", w.Body)
	}
	if send.watched != nil {
		t.Fatal("the bridge was asked to read for an unknown peer")
	}
}

// The watch reads another device, so it is covered by the same rule as the rest of the API.
func TestWatchRefusesFromOffMachine(t *testing.T) {
	paired(t, "laptop")

	send := &stub{}
	w := watchAt(t, send, "/api/watch/laptop/tty", "192.168.1.40:5000")

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", w.Code)
	}
	if send.watched != nil {
		t.Fatal("an off-machine request reached the wire")
	}
}

// A watch that fails after the headers are out cannot change the status code, so the reason has to
// travel as an event or the page is left showing a terminal that silently stopped.
func TestWatchReportsAFailureAsAnEvent(t *testing.T) {
	paired(t, "laptop")

	send := &stub{watchErr: errors.New("no such namespace")}
	w := watchAt(t, send, "/api/watch/laptop/nope", "127.0.0.1:5000")

	body := w.Body.String()
	if !strings.Contains(body, "event: gone") {
		t.Fatalf("no gone event: %q", body)
	}
	if !strings.Contains(body, "no such namespace") {
		t.Fatalf("the reason did not travel: %q", body)
	}
}

// A reason containing a newline would otherwise split its own event in half.
func TestAFailureWithANewlineStaysOneEvent(t *testing.T) {
	paired(t, "laptop")

	send := &stub{watchErr: errors.New("first line\nsecond line")}
	w := watchAt(t, send, "/api/watch/laptop/nope", "127.0.0.1:5000")

	for _, line := range strings.Split(w.Body.String(), "\n") {
		if strings.HasPrefix(line, "data: ") && strings.Contains(line, "second line") {
			return
		}
	}
	t.Fatalf("the reason was split across events: %q", w.Body)
}
