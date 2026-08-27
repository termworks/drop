// Package asked holds the requests to reach a path that nobody has answered yet.
//
// A path that is visible but not shared is a door with a bell on it. Ringing it leaves a note here:
// who rang, what they wanted, and anything they said about why. Nothing about being in this file
// grants anything -- it is a message, and answering it is a grant, which lives elsewhere.
//
// Kept apart from the address book and from grants for that reason. A request is somebody else's
// wish; a grant is your decision.
package asked

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/bresilla/drop/src/pkg/convo"
	"github.com/bresilla/drop/src/pkg/keep"
	"github.com/bresilla/drop/src/pkg/node"
	"github.com/bresilla/drop/src/pkg/ns"
	"github.com/bresilla/drop/src/pkg/plain"
)

// Most is how many are kept, so somebody asking in a loop cannot grow the file without end.
const Most = 128

// MaxWhy bounds what a request may say, because it is somebody else's text on your disk.
const MaxWhy = 280

// Request is one device asking to reach one path.
type Request struct {
	// From is the device that asked, and Name is what it is filed under here if it is known.
	From node.ID
	Name string
	// Person is who owns that machine, when they carried a badge signed by somebody known here.
	Person string
	Path   string
	// Why is what they said about it, and may be empty.
	Why string
	At  time.Time
}

// Who is the name a grant would be written against: a person if one is known, else the device.
func (r Request) Who() string {
	if r.Person != "" {
		return r.Person
	}
	if r.Name != "" {
		return r.Name
	}
	return r.From.String()
}

type stored struct {
	Name   string    `json:"name,omitempty"`
	Person string    `json:"person,omitempty"`
	Path   string    `json:"path"`
	Why    string    `json:"why,omitempty"`
	At     time.Time `json:"at"`
}

// One lock: a serving node writes this from every request it takes while whatever is showing the
// list reads it.
var mu sync.Mutex

func where() (string, error) {
	base, err := convo.DataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "asked.json"), nil
}

// key is one device asking about one path. Asking twice about the same path replaces the first,
// rather than filling the list with the same wish.
func key(from node.ID, path string) string { return from.String() + " " + path }

// Ring records a request, replacing an earlier one for the same path from the same device.
func Ring(r Request) error {
	path, err := ns.Clean(r.Path)
	if err != nil {
		return err
	}
	// Somebody else's words, on your disk, and then on your terminal when you look at what has
	// been asked for. Cutting it to length is not enough: an escape in there moves the cursor and
	// rewrites the rows above, so a request can be made to show a different path or a different
	// person than the one you are about to allow.
	r.Why = plain.Text(r.Why, MaxWhy)

	mu.Lock()
	defer mu.Unlock()

	all, err := read()
	if err != nil {
		return err
	}
	all[key(r.From, path)] = stored{
		Name:   r.Name,
		Person: r.Person,
		Path:   path,
		Why:    r.Why,
		At:     r.At.UTC(),
	}
	return write(trim(all))
}

// All is what has been asked for, most recent first.
func All() ([]Request, error) {
	mu.Lock()
	defer mu.Unlock()

	all, err := read()
	if err != nil {
		return nil, err
	}

	out := make([]Request, 0, len(all))
	for at, one := range all {
		id, _, found := cut(at)
		if !found {
			continue
		}
		from, err := node.ParseID(id)
		if err != nil {
			continue
		}
		out = append(out, Request{
			From:   from,
			Name:   one.Name,
			Person: one.Person,
			Path:   one.Path,
			Why:    one.Why,
			At:     one.At,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].At.After(out[j].At) })
	return out, nil
}

// Answered drops a request, because it has been granted or turned down. Either way it is dealt
// with, and a list that kept answered requests would be a list nobody reads.
func Answered(from node.ID, path string) error {
	clean, err := ns.Clean(path)
	if err != nil {
		return err
	}

	mu.Lock()
	defer mu.Unlock()

	all, err := read()
	if err != nil {
		return err
	}
	delete(all, key(from, clean))

	return write(all)
}

// cut splits a key back into the device and the path.
func cut(at string) (string, string, bool) {
	for i := 0; i < len(at); i++ {
		if at[i] == ' ' {
			return at[:i], at[i+1:], true
		}
	}
	return "", "", false
}

func read() (map[string]stored, error) {
	file, err := where()
	if err != nil {
		return nil, err
	}

	raw, err := os.ReadFile(file)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]stored{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", file, err)
	}

	out := map[string]stored{}
	if err := json.Unmarshal(raw, &out); err != nil {
		return map[string]stored{}, nil
	}
	return out, nil
}

func write(all map[string]stored) error {
	file, err := where()
	if err != nil {
		return err
	}

	raw, err := json.MarshalIndent(all, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding what has been asked for: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		return fmt.Errorf("creating %s: %w", filepath.Dir(file), err)
	}
	return keep.Replace(file, append(raw, '\n'))
}

// trim keeps the most recent.
func trim(all map[string]stored) map[string]stored {
	if len(all) <= Most {
		return all
	}

	type at struct {
		key  string
		when time.Time
	}
	order := make([]at, 0, len(all))
	for k, one := range all {
		order = append(order, at{key: k, when: one.At})
	}
	sort.Slice(order, func(i, j int) bool { return order[i].when.After(order[j].when) })

	for _, old := range order[Most:] {
		delete(all, old.key)
	}
	return all
}
