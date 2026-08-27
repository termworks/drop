package files

import (
	"fmt"
	"sort"

	"lukechampine.com/blake3"

	"github.com/bresilla/drop/src/pkg/history"
	"github.com/bresilla/drop/src/pkg/weave"
	"github.com/bresilla/drop/src/pkg/wire"
)

// What a change to a folder says, and what a set of them comes to.
//
// A note carries the whole file its author saved, because a note is one file. A folder cannot: it
// would carry every file in it every time anybody touched one of them. So a change says which paths
// moved and what each of them now is — a digest, a length, a time, and whether it is a program —
// and the bytes travel the way bytes already travel here, over a get that is now resumable.
//
// A file small enough to fit and made of lines carries its bytes in the change as well. That is
// what makes a line-wise merge possible at all: merging two versions needs both versions, and when
// two people were apart the log is the only place both of them are. Anything bigger, and anything
// that is not lines, is kept both ways instead — which is what a merge would have done with it.
//
// A deletion is a change like any other, and the path stays in the folder marked gone. That record
// is the whole reason the log exists: without it a file somebody deleted and a file that has not
// arrived yet look exactly the same, and one of those must be put back while the other must not.

// What one change to a folder may carry.
const (
	// MaxInline is the largest file whose bytes travel inside the change that names it.
	MaxInline = 1 << 16
	// MaxEdits caps how many paths one change names.
	MaxEdits = 1 << 12
	// MaxPaths caps how many paths one folder holds.
	MaxPaths = 1 << 17
)

// Held is one path, as the changes leave it.
type Held struct {
	// Gone says somebody deleted it. The path stays in the folder as the record of that.
	Gone bool
	Size int64
	Sum  [32]byte
	// Exec says it is a program, which is the one bit of the mode that travels. The rest does not:
	// what arrives is clamped, so two machines comparing modes would find them different for ever.
	Exec bool
	// At is when it last changed, in nanoseconds.
	At int64
	// Body is the bytes themselves, for a file small and plain enough to carry in a change.
	Body []byte
}

// Folder is what the changes say a directory holds, by path.
type Folder map[string]Held

// Edit is one path as one change left it.
type Edit struct {
	Path string
	Held
}

// same reports whether two paths hold the same thing.
//
// Not the time, and not the mode past whether it runs: the same bytes under the same name are the
// same file however each machine happens to date them, and a difference nobody can see is a change
// two machines would send each other for ever.
func (h Held) same(other Held) bool {
	return h.Gone == other.Gone && h.Sum == other.Sum && h.Size == other.Size && h.Exec == other.Exec
}

// carries reports whether a file's bytes travel inside the change that names it.
func carries(raw []byte) bool { return len(raw) <= MaxInline && weave.Textual(raw) }

// Changed is what a set of changes says a folder holds.
func Changed(changes []history.Change) Folder {
	made, _ := weave.Join(changes, folded, nil)
	return made
}

// folded is how a folder is woven.
//
// Nobody is named: what this machine calls somebody is this machine's own business, and the merged
// folder is written to a disk and then held up against another machine's. Two machines that called
// one person by different names would put a version aside under two different names and never
// agree. So the names come out of the keys, which are the same everywhere.
var folded = weave.Melding[Folder]{Take: took, Merge: melded}

// took is the folder one change leaves behind it: what its author was looking at, with what they
// did on top of it.
func took(was func() Folder, c history.Change) Folder {
	list, err := decodeEdits(c.Body)
	if err != nil || len(list) == 0 {
		return was()
	}

	before := was()
	out := make(Folder, len(before)+len(list))
	for path, held := range before {
		out[path] = held
	}
	for _, e := range list {
		out[e.Path] = e.Held
	}
	return out
}

// melded puts two folders together against the one they came from, path by path.
//
// A path only one side has ever heard of is that side's. A path both have is settled the way one
// file is settled, and what will not settle is kept beside it rather than lost.
func melded(base, ours, theirs Folder, us, them string) (Folder, []weave.Aside) {
	out := make(Folder, len(ours)+len(theirs))
	for path, o := range ours {
		t, both := theirs[path]
		if !both {
			out[path] = o
			continue
		}

		was, knew := base[path]
		kept, aside, both := agreed(was, knew, o, t, us, them)
		out[path] = kept
		if both {
			out[beside(path, us, aside.Sum, ours, theirs)] = aside
		}
	}
	for path, t := range theirs {
		if _, both := ours[path]; !both {
			out[path] = t
		}
	}
	return out, nil
}

// agreed is what one path comes to when both sides changed it, and whatever would not go in.
//
// A deletion loses to an edit. Somebody who removed a file and somebody who was working on it were
// answering different questions, and the answer that leaves clutter is recoverable where the answer
// that leaves nothing is not.
func agreed(base Held, knew bool, ours, theirs Held, us, them string) (Held, Held, bool) {
	switch {
	case ours.same(theirs):
		return ours, Held{}, false
	case knew && base.same(ours):
		return theirs, Held{}, false
	case knew && base.same(theirs):
		return ours, Held{}, false
	case ours.Gone:
		return theirs, Held{}, false
	case theirs.Gone:
		return ours, Held{}, false
	}

	// Both are files, both were edited, and neither edit is behind the other. Lines merge; anything
	// else is kept both ways, because merging a database line by line destroys it.
	if ours.Body == nil || theirs.Body == nil || !weave.Textual(ours.Body) || !weave.Textual(theirs.Body) {
		return theirs, ours, true
	}

	body, _ := weave.Bytes(base.Body, ours.Body, theirs.Body, us, them)
	return Held{
		Size: int64(len(body)),
		Sum:  blake3.Sum256(body),
		Exec: ours.Exec || theirs.Exec,
		At:   later(ours.At, theirs.At),
		Body: body,
	}, Held{}, false
}

// beside is where a version that would not merge is kept: next to the path under the name of
// whoever saved it, and under its own digest when a real file is already at that name.
func beside(path, who string, sum [32]byte, ours, theirs Folder) string {
	at := path + "." + weave.Safe(who)
	if _, taken := ours[at]; !taken {
		if _, taken := theirs[at]; !taken {
			return at
		}
	}
	return fmt.Sprintf("%s.%x", path, sum[:4])
}

// later is the later of two times.
func later(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

// Paths is every path a folder mentions, in the one order they are walked.
func Paths(f Folder) []string {
	out := make([]string, 0, len(f))
	for path := range f {
		out = append(out, path)
	}
	sort.Strings(out)
	return out
}

// encodeEdits writes what one change says.
func encodeEdits(list []Edit) []byte {
	w := wire.NewWriter()
	w.Uint(uint64(len(list)))
	for _, e := range list {
		w.String(e.Path)
		w.Bool(e.Gone)
		w.Int(e.Size)
		w.Bytes(e.Sum[:])
		w.Bool(e.Exec)
		w.Int(e.At)
		w.Bytes(e.Body)
	}
	return w.Body()
}

// decodeEdits reads what one change says, refusing one that names a path no folder could hold.
func decodeEdits(raw []byte) ([]Edit, error) {
	r := wire.NewReader(raw)
	count, err := r.Uint()
	if err != nil {
		return nil, err
	}
	if count > MaxEdits {
		return nil, fmt.Errorf("a change names %d paths, over the %d limit", count, MaxEdits)
	}

	out := make([]Edit, 0, wire.Hint(count, raw, len(Held{}.Sum)+8))
	for range count {
		var e Edit

		name, err := r.String(MaxRel)
		if err != nil {
			return nil, err
		}
		if e.Path, err = clean(name); err != nil {
			return nil, err
		}
		if e.Path == "." {
			return nil, fmt.Errorf("a change names the folder itself")
		}
		if e.Gone, err = r.Bool(); err != nil {
			return nil, err
		}
		if e.Size, err = r.Int(); err != nil {
			return nil, err
		}
		sum, err := r.Bytes(len(e.Sum))
		if err != nil {
			return nil, err
		}
		if len(sum) != len(e.Sum) {
			return nil, fmt.Errorf("%s is named by a digest of %d bytes", e.Path, len(sum))
		}
		e.Sum = [32]byte(sum)
		if e.Exec, err = r.Bool(); err != nil {
			return nil, err
		}
		if e.At, err = r.Int(); err != nil {
			return nil, err
		}
		body, err := r.Bytes(MaxInline)
		if err != nil {
			return nil, err
		}
		if len(body) > 0 {
			e.Body = append([]byte(nil), body...)
		}
		out = append(out, e)
	}
	if !r.Done() {
		return nil, fmt.Errorf("a change has %d bytes nobody claims", len(raw))
	}
	return out, nil
}
