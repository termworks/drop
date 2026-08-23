package web

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func shareTo(t *testing.T, s *Server, fields map[string]string, filename, body, from string) *httptest.ResponseRecorder {
	t.Helper()

	var buf bytes.Buffer
	form := multipart.NewWriter(&buf)
	for k, v := range fields {
		if err := form.WriteField(k, v); err != nil {
			t.Fatalf("writing %s: %v", k, err)
		}
	}
	if filename != "" {
		part, err := form.CreateFormFile("file", filename)
		if err != nil {
			t.Fatalf("creating the part: %v", err)
		}
		part.Write([]byte(body))
	}
	form.Close()

	r := httptest.NewRequest(http.MethodPost, "/share", &buf)
	r.RemoteAddr = from
	r.Header.Set("Content-Type", form.FormDataContentType())

	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	return w
}

// tokenOf reads the token out of the redirect the share sheet is sent to.
func tokenOf(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()

	if w.Code != http.StatusSeeOther {
		t.Fatalf("status = %d: %s", w.Code, w.Body)
	}
	to := w.Header().Get("Location")
	_, token, found := strings.Cut(to, "shared=")
	if !found {
		t.Fatalf("no token in %q", to)
	}
	return token
}

func claimed(t *testing.T, s *Server, token, from string) *httptest.ResponseRecorder {
	t.Helper()

	r := httptest.NewRequest(http.MethodGet, "/api/shared/"+token, nil)
	r.RemoteAddr = from

	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	return w
}

func TestALinkFromAPhoneCanBeClaimed(t *testing.T) {
	paired(t, "laptop")
	s := New(&stub{})

	token := tokenOf(t, shareTo(t, s, map[string]string{
		"title": "a page",
		"url":   "https://example.com/thing",
	}, "", "", "127.0.0.1:5000"))

	w := claimed(t, s, token, "127.0.0.1:5000")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body)
	}

	var got Shared
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if got.URL != "https://example.com/thing" || got.Title != "a page" {
		t.Fatalf("shared = %+v", got)
	}
}

// Once, because a reload should not re-send what was already sent.
func TestAShareIsClaimedOnlyOnce(t *testing.T) {
	paired(t, "laptop")
	s := New(&stub{})

	token := tokenOf(t, shareTo(t, s, map[string]string{"text": "hello"}, "", "", "127.0.0.1:5000"))

	if w := claimed(t, s, token, "127.0.0.1:5000"); w.Code != http.StatusOK {
		t.Fatalf("the first claim failed: %d", w.Code)
	}
	if w := claimed(t, s, token, "127.0.0.1:5000"); w.Code == http.StatusOK {
		t.Fatal("the same share was handed over twice")
	}
}

func TestAnUnknownTokenIsRefused(t *testing.T) {
	paired(t, "laptop")

	if w := claimed(t, New(&stub{}), "nothing", "127.0.0.1:5000"); w.Code == http.StatusOK {
		t.Fatal("a token nobody issued was accepted")
	}
}

// The name comes from a phone, so it is as untrusted as a peer's.
func TestASharedFileNameIsStripped(t *testing.T) {
	paired(t, "laptop")
	s := New(&stub{})

	token := tokenOf(t, shareTo(t, s, nil, "../../etc/passwd", "x", "127.0.0.1:5000"))

	var got Shared
	json.Unmarshal(claimed(t, s, token, "127.0.0.1:5000").Body.Bytes(), &got)

	if got.Name != "passwd" {
		t.Fatalf("name = %q", got.Name)
	}
	if strings.Contains(got.Name, "/") {
		t.Fatalf("a separator survived: %q", got.Name)
	}
}

func TestAnEmptyShareIsRefused(t *testing.T) {
	paired(t, "laptop")

	if w := shareTo(t, New(&stub{}), nil, "", "", "127.0.0.1:5000"); w.Code == http.StatusSeeOther {
		t.Fatal("a share with nothing in it was accepted")
	}
}

// The share sheet posts from the browser on this machine, so it is covered by the same rule as
// everything else.
func TestSharingFromOffMachineIsRefused(t *testing.T) {
	paired(t, "laptop")

	w := shareTo(t, New(&stub{}), map[string]string{"text": "hello"}, "", "", "192.168.1.40:5000")
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", w.Code)
	}
}

// Something shared and never claimed must not sit in memory forever.
func TestAnAbandonedShareIsForgotten(t *testing.T) {
	held := newShares()

	token, err := held.keep(&Shared{Text: "hello"})
	if err != nil {
		t.Fatalf("keeping: %v", err)
	}

	held.mu.Lock()
	held.held[token].at = time.Now().Add(-2 * ShareKeeps)
	held.mu.Unlock()

	if _, ok := held.claim(token); ok {
		t.Fatal("a share older than its welcome was still handed over")
	}
}

// The manifest is what puts drop in the share sheet, so it has to be served and to say where.
func TestTheManifestOffersAShareTarget(t *testing.T) {
	paired(t, "laptop")

	r := httptest.NewRequest(http.MethodGet, "/manifest.json", nil)
	r.RemoteAddr = "127.0.0.1:5000"

	w := httptest.NewRecorder()
	New(&stub{}).Handler().ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}

	var manifest struct {
		ShareTarget struct {
			Action string `json:"action"`
			Method string `json:"method"`
		} `json:"share_target"`
		Icons []struct {
			Src string `json:"src"`
		} `json:"icons"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &manifest); err != nil {
		t.Fatalf("the manifest is not valid json: %v", err)
	}
	if manifest.ShareTarget.Action != "/share" || manifest.ShareTarget.Method != "POST" {
		t.Fatalf("share target = %+v", manifest.ShareTarget)
	}
	if len(manifest.Icons) == 0 {
		t.Fatal("a manifest with no icon will not install")
	}
}
