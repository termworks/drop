// Package seen remembers devices that dialled this one and were not let in.
//
// Without it, letting a bare id reach a path means copying sixty-four characters of hex out of a
// log by hand. It dialled -- drop already knows the id, and knows when. What is kept here is a
// record of an attempt and nothing else: being in this file is not a step towards being allowed,
// and nothing reads it to decide anything.
package seen

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
)

// Most is how many are kept. A device that dials in a loop must not grow a file without end, and
// the oldest attempt is the least interesting one.
const Most = 64

// Knock is one device that dialled and did not get in.
type Knock struct {
	ID node.ID
	// At is when it last tried, which is the useful one: a device that has been trying all week is
	// worth knowing about as of today.
	At time.Time
	// Asked is the path it wanted, empty when it never got that far.
	Asked string
	// Why is what it was told.
	Why string
}

type stored struct {
	At    time.Time `json:"at"`
	Asked string    `json:"asked,omitempty"`
	Why   string    `json:"why,omitempty"`
}

// one lock, because the serving loop writes this from every refused connection while whatever is
// showing the list reads it.
var mu sync.Mutex

func path() (string, error) {
	base, err := convo.DataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "seen.json"), nil
}

// Knocked records an attempt, replacing an earlier one from the same device.
func Knocked(id node.ID, asked, why string, now time.Time) error {
	mu.Lock()
	defer mu.Unlock()

	all, err := read()
	if err != nil {
		return err
	}
	all[id.String()] = stored{At: now.UTC(), Asked: asked, Why: why}

	return write(trim(all))
}

// All is what has knocked, most recent first.
func All() ([]Knock, error) {
	mu.Lock()
	defer mu.Unlock()

	all, err := read()
	if err != nil {
		return nil, err
	}

	out := make([]Knock, 0, len(all))
	for text, at := range all {
		id, err := node.ParseID(text)
		if err != nil {
			continue
		}
		out = append(out, Knock{ID: id, At: at.At, Asked: at.Asked, Why: at.Why})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].At.After(out[j].At) })
	return out, nil
}

// Forget drops one, for a device that has since been paired or that nobody wants to look at.
func Forget(id node.ID) error {
	mu.Lock()
	defer mu.Unlock()

	all, err := read()
	if err != nil {
		return err
	}
	delete(all, id.String())

	return write(all)
}

func read() (map[string]stored, error) {
	file, err := path()
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
		// A file that will not parse is not worth failing a connection over: it is a note of who
		// knocked, and starting it again loses nothing that was decided.
		return map[string]stored{}, nil
	}
	return out, nil
}

func write(all map[string]stored) error {
	file, err := path()
	if err != nil {
		return err
	}

	raw, err := json.MarshalIndent(all, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding what has knocked: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		return fmt.Errorf("creating %s: %w", filepath.Dir(file), err)
	}
	return keep.Replace(file, append(raw, '\n'))
}

// trim keeps the most recent, so a device dialling in a loop cannot grow the file without end.
func trim(all map[string]stored) map[string]stored {
	if len(all) <= Most {
		return all
	}

	type at struct {
		id   string
		when time.Time
	}
	order := make([]at, 0, len(all))
	for id, one := range all {
		order = append(order, at{id: id, when: one.At})
	}
	sort.Slice(order, func(i, j int) bool { return order[i].when.After(order[j].when) })

	for _, old := range order[Most:] {
		delete(all, old.id)
	}
	return all
}
