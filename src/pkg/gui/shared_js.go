//go:build js

package gui

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"syscall/js"
)

// Shared is something handed over from outside — Android's share sheet, by way of the bridge.
type Shared struct {
	Title string `json:"title,omitempty"`
	Text  string `json:"text,omitempty"`
	URL   string `json:"url,omitempty"`
	Name  string `json:"name,omitempty"`
	Size  int64  `json:"size,omitempty"`
}

// What returns the one line worth showing about it.
func (s *Shared) What() string {
	switch {
	case s.Name != "":
		return s.Name
	case s.URL != "":
		return s.URL
	case s.Text != "":
		return s.Text
	default:
		return s.Title
	}
}

// Body is what would be sent as a message.
func (s *Shared) Body() string {
	if s.URL != "" {
		return s.URL
	}
	if s.Text != "" {
		return s.Text
	}
	return s.Title
}

// IsLink says the far end should treat this as something to open.
func (s *Shared) IsLink() bool { return s.URL != "" }

// Claim takes whatever the share sheet left for us.
//
// The bridge holds it and redirects here with a token, because the sheet says what was shared but
// not who it is for — which is the one question this interface is here to ask. The token is taken
// out of the address bar straight away: it is single use, and a reload should not look like a second
// share that expired.
func (r *Remote) Claim() (*Shared, error) {
	token := waiting()
	if token == "" {
		return nil, nil
	}
	forget()

	res, err := r.HTTP.Get(r.At + "/api/shared/" + url.PathEscape(token))
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()

	body, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, err
	}
	if res.StatusCode < 200 || res.StatusCode > 299 {
		return nil, errorOf(reason(body, res.Status))
	}

	var item Shared
	if err := json.Unmarshal(body, &item); err != nil {
		return nil, err
	}
	return &item, nil
}

// waiting reads the token the bridge redirected with.
func waiting() string {
	where := js.Global().Get("location").Get("search").String()

	query, err := url.ParseQuery(strings.TrimPrefix(where, "?"))
	if err != nil {
		return ""
	}
	return query.Get("shared")
}

// forget takes the token out of the address bar without reloading.
func forget() {
	history := js.Global().Get("history")
	if history.IsUndefined() {
		return
	}
	path := js.Global().Get("location").Get("pathname").String()
	history.Call("replaceState", js.Null(), "", path)
}

type said string

func (s said) Error() string { return string(s) }

func errorOf(text string) error { return said(text) }

var _ = http.StatusOK
