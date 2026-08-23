package web

import (
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func watchAt(t *testing.T, send *stub, path, from string) *httptest.ResponseRecorder {
	t.Helper()

	r := httptest.NewRequest(http.MethodGet, path, nil)
	r.RemoteAddr = from

	w := httptest.NewRecorder()
	New(send).Handler().ServeHTTP(w, r)
	return w
}

// Terminal output is full of newlines and an event is delimited by one, so the bytes have to arrive
// encoded. Sending them raw would split a single write across several events, each truncated.
func TestWatchEncodesSoNewlinesSurvive(t *testing.T) {
	paired(t, "laptop")

	send := &stub{watchBody: "first\nsecond\n\nthird"}
	w := watchAt(t, send, "/api/watch/laptop/tty", "127.0.0.1:5000")

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body)
	}
	if ct := w.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content-type = %q", ct)
	}

	var got strings.Builder
	for _, line := range strings.Split(w.Body.String(), "\n") {
		data, ok := strings.CutPrefix(line, "data: ")
		if !ok {
			continue
		}
		raw, err := base64.StdEncoding.DecodeString(data)
		if err != nil {
			t.Fatalf("frame did not decode: %v", err)
		}
		got.WriteString(string(raw))
	}

	if got.String() != "first\nsecond\n\nthird" {
		t.Fatalf("reassembled = %q", got.String())
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
	if !strings.Contains(body, base64.StdEncoding.EncodeToString([]byte("no such namespace"))) {
		t.Fatalf("the reason did not travel: %q", body)
	}
}
