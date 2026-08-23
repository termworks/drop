package web

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func request(remote string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/api/peers", nil)
	r.RemoteAddr = remote
	return r
}

func TestLocalRequestAcceptsThisMachine(t *testing.T) {
	for _, remote := range []string{
		"127.0.0.1:54321",
		"127.0.0.53:9",
		"[::1]:54321",
	} {
		if !localRequest(request(remote)) {
			t.Errorf("localRequest(%q) refused a loopback address", remote)
		}
	}
}

// Everything that is not this machine, including the addresses a misconfigured bind would produce.
func TestLocalRequestRefusesEverythingElse(t *testing.T) {
	for _, remote := range []string{
		"192.168.1.20:54321",
		"10.0.0.7:80",
		"[2001:db8::1]:443",
		"8.8.8.8:53",
		"",
		"garbage",
		"not-an-ip:80",
	} {
		if localRequest(request(remote)) {
			t.Errorf("localRequest(%q) accepted a non-local address", remote)
		}
	}
}

// A forwarding header is written by whoever is in front, and there is not supposed to be anything
// in front of this. Trusting one would let a proxy hand the whole node to the network.
func TestLocalRequestIgnoresForwardingHeaders(t *testing.T) {
	r := request("192.168.1.20:54321")
	r.Header.Set("X-Forwarded-For", "127.0.0.1")
	r.Header.Set("X-Real-IP", "127.0.0.1")

	if localRequest(r) {
		t.Fatal("localRequest() believed X-Forwarded-For over the connection")
	}
}

// The guard has to refuse before the handler runs: the page acts as this node, so a request that
// reaches a handler at all has already read or sent something.
func TestGuardRefusesBeforeTheHandlerRuns(t *testing.T) {
	reached := false
	handler := guard(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
	}))

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, request("203.0.113.9:41000"))

	if reached {
		t.Fatal("a remote request reached the handler")
	}
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusForbidden)
	}
}

func TestGuardLetsThisMachineThrough(t *testing.T) {
	reached := false
	handler := guard(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
	}))

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, request("127.0.0.1:41000"))

	if !reached {
		t.Fatal("a local request was refused")
	}
	if got := w.Header().Get("X-Frame-Options"); got != "DENY" {
		t.Errorf("X-Frame-Options = %q, want DENY", got)
	}
}
