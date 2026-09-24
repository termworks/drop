package user

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/bresilla/drop/src/pkg/keep"
	"github.com/bresilla/drop/src/pkg/node"
)

// Machines taken out of this user's, and kept out.
//
// A machine of yours is recognised by the badge it wears, not by anything written down, so
// forgetting it on one machine changed nothing: it still showed your badge everywhere, and the
// machines that still knew it told this one about it again. So taking a machine out is a mark,
// kept and handed between your machines the way the circle is, and every one of them turns a
// machine it holds a mark against away as the stranger it now is. Adding it again is a later mark
// the other way, and the later of two marks is the one that stands.

// Mark is one machine taken out, or put back, and when.
type Mark struct {
	At   int64 `json:"at"`
	Gone bool  `json:"gone"`
}

// mostMarks bounds the file: the oldest are let go first, and the ones putting a machine back
// before the ones keeping one out.
const mostMarks = 512

func marksAt() (string, error) {
	dir, err := node.ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "gone.json"), nil
}

// marks is the file as last read, kept because it is asked about on every connection.
var marks struct {
	sync.Mutex
	held map[string]Mark
	seen os.FileInfo
}

// Marks is every mark this machine holds, by machine id.
func Marks() (map[string]Mark, error) {
	at, err := marksAt()
	if err != nil {
		return nil, err
	}
	marks.Lock()
	defer marks.Unlock()

	now, err := os.Stat(at)
	if errors.Is(err, os.ErrNotExist) {
		marks.held, marks.seen = nil, nil
		return map[string]Mark{}, nil
	}
	if err != nil {
		return nil, err
	}
	if marks.seen == nil || !os.SameFile(marks.seen, now) || !marks.seen.ModTime().Equal(now.ModTime()) || marks.seen.Size() != now.Size() {
		held, err := readMarks(at)
		if err != nil {
			return nil, err
		}
		marks.held, marks.seen = held, now
	}
	out := make(map[string]Mark, len(marks.held))
	for id, m := range marks.held {
		out[id] = m
	}
	return out, nil
}

func readMarks(at string) (map[string]Mark, error) {
	raw, err := keep.ReadFile(at, keep.MaxState)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]Mark{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := map[string]Mark{}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// Removed says a machine was taken out of this user's and has not been put back.
func Removed(id string) bool {
	held, err := Marks()
	if err != nil {
		return false
	}
	return held[id].Gone
}

// Remove takes a machine out of this user's, from now.
func Remove(id string, now time.Time) error {
	_, err := change(map[string]Mark{id: {At: now.Unix(), Gone: true}})
	return err
}

// Restore puts a machine back, from now. A machine never taken out has nothing to undo.
func Restore(id string, now time.Time) error {
	held, err := Marks()
	if err != nil || !held[id].Gone {
		return err
	}
	_, err = change(map[string]Mark{id: {At: now.Unix()}})
	return err
}

// Merge takes the marks another machine of this user's holds, the later of two for one machine
// standing, and says which machines that took out.
func Merge(theirs map[string]Mark) ([]string, error) {
	return change(theirs)
}

// change writes marks in, each only when it is later than the one held, and says which machines
// that took out.
func change(fresh map[string]Mark) ([]string, error) {
	at, err := marksAt()
	if err != nil {
		return nil, err
	}
	var gone []string
	err = keep.While(at, func() error {
		held, err := readMarks(at)
		if err != nil {
			return err
		}
		wrote := false
		for id, m := range fresh {
			if was, ok := held[id]; ok && was.At >= m.At {
				continue
			}
			if m.Gone && !held[id].Gone {
				gone = append(gone, id)
			}
			held[id], wrote = m, true
		}
		if !wrote {
			return nil
		}
		trim(held)
		raw, err := json.MarshalIndent(held, "", "  ")
		if err != nil {
			return err
		}
		return keep.Replace(at, append(raw, '\n'))
	})
	return gone, err
}

// trim lets the oldest marks go once there are too many, the ones putting a machine back first.
func trim(held map[string]Mark) {
	if len(held) <= mostMarks {
		return
	}
	ids := make([]string, 0, len(held))
	for id := range held {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		a, b := held[ids[i]], held[ids[j]]
		if a.Gone != b.Gone {
			return !a.Gone
		}
		return a.At < b.At
	})
	for _, id := range ids[:len(held)-mostMarks] {
		delete(held, id)
	}
}
