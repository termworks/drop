//go:build js || android

package gui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Remote reaches devices through a machine that is a drop node, over the same API the page used.
//
// This is what a browser and a phone get: neither can open a QUIC connection, so neither can be a
// node. A desktop uses Direct instead and the screens above cannot tell the difference.
type Remote struct {
	At   string
	HTTP *http.Client
}

// NewRemote points at a bridge. An empty address means the page this was served from, which is what
// the browser build wants and what a phone is told once.
func NewRemote(at string) *Remote {
	return &Remote{
		At:   strings.TrimSuffix(at, "/"),
		HTTP: &http.Client{Timeout: 60 * time.Second},
	}
}

func (r *Remote) get(path string, into any) error {
	res, err := r.HTTP.Get(r.At + path)
	if err != nil {
		return err
	}
	defer res.Body.Close()

	body, err := io.ReadAll(res.Body)
	if err != nil {
		return err
	}
	if res.StatusCode < 200 || res.StatusCode > 299 {
		return fmt.Errorf("%s", reason(body, res.Status))
	}
	return json.Unmarshal(body, into)
}

// reason pulls the far end's own words out of a refusal, so a person is told what went wrong rather
// than a status number.
func reason(body []byte, fallback string) string {
	var said struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(body, &said) == nil && said.Error != "" {
		return said.Error
	}
	return fallback
}

func (r *Remote) Self() (Identity, error) {
	var who Identity
	return who, r.get("/api/self", &who)
}

func (r *Remote) Peers() ([]Peer, error) {
	var found []Peer
	return found, r.get("/api/peers", &found)
}

func (r *Remote) Spaces(peer string) ([]Space, error) {
	var found []Space
	return found, r.get("/api/spaces/"+url.PathEscape(peer), &found)
}

func (r *Remote) Log(peer string) ([]Message, error) {
	var found []Message
	return found, r.get("/api/log/"+url.PathEscape(peer), &found)
}

func (r *Remote) Say(peer, body string, asLink bool) error {
	kind := "text"
	if asLink {
		kind = "link"
	}

	payload, err := json.Marshal(map[string]string{"to": peer, "body": body, "kind": kind})
	if err != nil {
		return err
	}

	res, err := r.HTTP.Post(r.At+"/api/say", "application/json", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	defer res.Body.Close()

	answer, _ := io.ReadAll(res.Body)
	if res.StatusCode < 200 || res.StatusCode > 299 {
		return fmt.Errorf("%s", reason(answer, res.Status))
	}
	return nil
}

// Watch reads the frames the bridge sends and turns them back into terminal bytes.
//
// The bridge sends a rendered screen rather than the raw stream, so what arrives is already parsed.
// Redrawing from it is what lets a watcher join halfway and see the same picture as everyone else.
func (r *Remote) Watch(peer, path string, into io.Writer, resize func(cols, rows int), done <-chan struct{}) error {
	req, err := http.NewRequest(http.MethodGet, r.At+"/api/watch/"+url.PathEscape(peer)+"/"+strings.TrimPrefix(path, "/"), nil)
	if err != nil {
		return err
	}

	// No timeout on a stream: it is meant to stay open, and the caller closing done is what ends it.
	stream := &http.Client{}
	res, err := stream.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()

	go func() {
		<-done
		res.Body.Close()
	}()

	return readFrames(res.Body, into, resize)
}

// Offer opens this device to a pairing, through the machine that served this page.
func (r *Remote) Offer() (string, error) {
	res, err := r.HTTP.Post(r.At+"/api/pair", "application/json", nil)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()

	body, err := io.ReadAll(res.Body)
	if err != nil {
		return "", err
	}
	if res.StatusCode < 200 || res.StatusCode > 299 {
		return "", fmt.Errorf("%s", reason(body, res.Status))
	}

	var said struct {
		Ticket string `json:"ticket"`
	}
	if err := json.Unmarshal(body, &said); err != nil {
		return "", err
	}
	return said.Ticket, nil
}

// Pairing reports how an offer is going. Polled rather than pushed: it is one small answer, asked
// for a few seconds while somebody points a camera, and a second connection to carry it would be
// more machinery than the question deserves.
func (r *Remote) Pairing() (string, string, error) {
	var said struct {
		Ticket string `json:"ticket"`
		With   string `json:"with"`
	}
	if err := r.get("/api/pair", &said); err != nil {
		return "", "", err
	}
	return said.Ticket, said.With, nil
}

func (r *Remote) Unpair() error {
	req, err := http.NewRequest(http.MethodDelete, r.At+"/api/pair", nil)
	if err != nil {
		return err
	}
	res, err := r.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()

	_, _ = io.Copy(io.Discard, res.Body)
	return nil
}
