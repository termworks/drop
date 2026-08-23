package web

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func askSpaces(t *testing.T, send *stub, peer, from string) *httptest.ResponseRecorder {
	t.Helper()

	r := httptest.NewRequest(http.MethodGet, "/api/spaces/"+peer, nil)
	r.RemoteAddr = from

	w := httptest.NewRecorder()
	New(send).Handler().ServeHTTP(w, r)
	return w
}

func TestSpacesListsWhatAPeerServes(t *testing.T) {
	paired(t, "laptop")

	send := &stub{serves: []Space{
		{Path: "/inbox", Kind: "files", Writable: true},
		{Path: "/term", Kind: "tty"},
	}}
	w := askSpaces(t, send, "laptop", "127.0.0.1:5000")

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body)
	}

	var got []Space
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if len(got) != 2 || got[0].Path != "/inbox" || got[1].Kind != "tty" {
		t.Fatalf("spaces = %+v", got)
	}
	if !got[0].Writable || got[1].Writable {
		t.Fatalf("writability did not survive: %+v", got)
	}
}

// A device that offers nothing must come back as an empty list, not null: the page tells the two
// apart and says something different for each.
func TestSpacesAreAlwaysAList(t *testing.T) {
	paired(t, "laptop")

	w := askSpaces(t, &stub{}, "laptop", "127.0.0.1:5000")
	if got := w.Body.String(); got != "[]\n" {
		t.Fatalf("body = %q, want an empty list", got)
	}
}

func TestSpacesReportsAnUnreachablePeer(t *testing.T) {
	paired(t, "laptop")

	send := &stub{spacesErr: errors.New("no route")}
	w := askSpaces(t, send, "laptop", "127.0.0.1:5000")

	if w.Code == http.StatusOK {
		t.Fatalf("an unreachable peer looked fine: %s", w.Body)
	}
}

func TestSpacesRefusesAnUnknownPeer(t *testing.T) {
	paired(t, "laptop")

	send := &stub{}
	w := askSpaces(t, send, "nobody", "127.0.0.1:5000")

	if w.Code == http.StatusOK {
		t.Fatalf("an unknown peer was accepted: %s", w.Body)
	}
	if send.asked != nil {
		t.Fatal("the bridge was asked about an unknown peer")
	}
}

func TestSpacesRefusesFromOffMachine(t *testing.T) {
	paired(t, "laptop")

	send := &stub{}
	w := askSpaces(t, send, "laptop", "192.168.1.40:5000")

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", w.Code)
	}
	if send.asked != nil {
		t.Fatal("an off-machine request reached the wire")
	}
}
