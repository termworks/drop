package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Two pages open side by side are two devices. If the page cannot say which one it is, neither can
// the person looking at it.
func TestSelfNamesThisDevice(t *testing.T) {
	paired(t, "laptop")

	r := httptest.NewRequest(http.MethodGet, "/api/self", nil)
	r.RemoteAddr = "127.0.0.1:5000"

	w := httptest.NewRecorder()
	New(&stub{}).Handler().ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body)
	}

	var who Identity
	if err := json.Unmarshal(w.Body.Bytes(), &who); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if who.Name != "here" || who.ID != "abc123" {
		t.Fatalf("identity = %+v", who)
	}
	if len(who.Addrs) != 1 {
		t.Fatalf("addrs = %v", who.Addrs)
	}
}

func TestSelfRefusesFromOffMachine(t *testing.T) {
	paired(t, "laptop")

	r := httptest.NewRequest(http.MethodGet, "/api/self", nil)
	r.RemoteAddr = "192.168.1.40:5000"

	w := httptest.NewRecorder()
	New(&stub{}).Handler().ServeHTTP(w, r)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", w.Code)
	}
}
