// Package weave is what a set of changes comes to when several people made them at once.
//
// A history is a graph and not a line: two people who could not see each other both made a change
// after the same one, and neither is behind the other. What the thing they were both changing now
// is has to be worked out rather than picked, and it is worked out the way git works it out — two
// versions merged against the version they last agreed on, and that version worked out the same way
// again, because it may itself be two versions somebody merged.
//
// What is being woven belongs to the caller. This walks the graph and says which three versions go
// together; the caller says what a version is and what putting two of them together means. A note
// weaves the whole file somebody saved. A folder weaves what it holds, path by path.
package weave

import (
	"bytes"
	"encoding/hex"
	"sort"
	"strings"

	"github.com/bresilla/drop/src/pkg/history"
)

// Aside is a version that was not merged into the thing, to be kept beside it.
type Aside struct {
	// Who is whose version it is, and Body is what they saved.
	Who  string
	Body []byte
}

// Melding is the thing being woven: how one change leaves it, and how two of them go together.
type Melding[T any] struct {
	// Take is the thing as one change left it. was is the thing as that change's author saw it,
	// worked out only if it is asked for, so a change that carries a whole version never pays for
	// the version it was made against.
	Take func(was func() T, c history.Change) T
	// Merge is two versions of the thing against the one they came from, named by whoever made
	// them, and whatever would not go in.
	Merge func(base, ours, theirs T, us, them string) (T, []Aside)
}

// Join is what a set of changes comes to, and whatever could not be merged into it.
//
// named says what to call the person who signed with a key; an empty answer, or no function at all,
// leaves the key to speak for itself. A thing whose merged version is written back and compared
// between machines wants no function at all: what this machine calls somebody is this machine's
// own business, and two machines that call one person different names would arrive at two versions.
func Join[T any](changes []history.Change, m Melding[T], named func(author string) string) (T, []Aside) {
	var zero T
	if len(changes) == 0 {
		return zero, nil
	}

	w := &woven[T]{
		by:    make(map[history.ID]history.Change, len(changes)),
		how:   m,
		named: named,
		made:  map[string]*T{},
		left:  map[history.ID]*T{},
	}
	for _, c := range changes {
		w.by[c.ID()] = c
	}
	return w.join(w.heads())
}

// woven is one replay: the changes by id, and what has already been worked out.
type woven[T any] struct {
	by    map[history.ID]history.Change
	how   Melding[T]
	named func(author string) string
	// made is what a set of heads came to, and left is what one change left behind it.
	made map[string]*T
	left map[history.ID]*T
}

// heads is the changes nothing else here comes after, in the one order they are taken.
func (w *woven[T]) heads() []history.ID {
	named := make(map[history.ID]bool, len(w.by))
	for _, c := range w.by {
		for _, head := range c.Heads {
			named[head] = true
		}
	}

	var out []history.ID
	for id := range w.by {
		if !named[id] {
			out = append(out, id)
		}
	}
	return sorted(out)
}

// left is the thing as one change left it: what its author saw, with their change on top of it.
func (w *woven[T]) after(id history.ID) T {
	if held, done := w.left[id]; done {
		return *held
	}

	c := w.by[id]
	made := w.how.Take(func() T {
		was, _ := w.join(c.Heads)
		return was
	}, c)
	w.left[id] = &made
	return made
}

// join is the thing a set of heads makes.
//
// One head is that change's own version, whole. Several are merged in the order their ids are in,
// each against the version the two sides last agreed on.
func (w *woven[T]) join(heads []history.ID) (T, []Aside) {
	var zero T
	switch len(heads) {
	case 0:
		return zero, nil
	case 1:
		return w.after(heads[0]), nil
	}

	if held, done := w.made[key(heads)]; done {
		return *held, nil
	}

	made := w.after(heads[0])
	mine := []string{w.who(heads[0])}
	var aside []Aside

	for i, head := range heads[1:] {
		base, _ := w.join(w.between(heads[:i+1], head))

		var kept []Aside
		made, kept = w.how.Merge(base, made, w.after(head), strings.Join(mine, ", "), w.who(head))
		aside = append(aside, kept...)
		mine = append(mine, w.who(head))
	}

	w.made[key(heads)] = &made
	return made, aside
}

// between is the last thing two sides agreed on: the changes both are behind, minus the ones that
// are themselves behind another of them.
func (w *woven[T]) between(mine []history.ID, theirs history.ID) []history.ID {
	ours, other := w.behind(mine), w.behind([]history.ID{theirs})

	var common, reach []history.ID
	for id := range ours {
		if other[id] {
			common = append(common, id)
			reach = append(reach, w.by[id].Heads...)
		}
	}

	covered := w.behind(reach)
	var out []history.ID
	for _, id := range common {
		if !covered[id] {
			out = append(out, id)
		}
	}
	return sorted(out)
}

// behind is those changes and everything they were made after.
func (w *woven[T]) behind(heads []history.ID) map[history.ID]bool {
	seen := make(map[history.ID]bool, len(w.by))

	var walk []history.ID
	for _, head := range heads {
		if _, held := w.by[head]; held && !seen[head] {
			seen[head] = true
			walk = append(walk, head)
		}
	}
	for len(walk) > 0 {
		at := walk[len(walk)-1]
		walk = walk[:len(walk)-1]
		for _, head := range w.by[at].Heads {
			if _, held := w.by[head]; held && !seen[head] {
				seen[head] = true
				walk = append(walk, head)
			}
		}
	}
	return seen
}

// who is what to call the person who made a change.
func (w *woven[T]) who(id history.ID) string { return Whose(w.by[id].Author, w.named) }

// sorted is a set of ids in the one order they are read in.
func sorted(ids []history.ID) []history.ID {
	sort.Slice(ids, func(i, j int) bool { return bytes.Compare(ids[i][:], ids[j][:]) < 0 })
	return ids
}

// key names a set of heads, so what a set of heads came to is worked out once.
func key(heads []history.ID) string {
	var out strings.Builder
	for _, id := range heads {
		out.WriteString(hex.EncodeToString(id[:]))
	}
	return out.String()
}
