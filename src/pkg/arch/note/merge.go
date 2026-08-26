package note

import (
	"bytes"
	"encoding/hex"
	"sort"
	"strings"
	"unicode/utf8"

	"golang.org/x/crypto/ssh"

	"github.com/bresilla/drop/src/pkg/history"
	"github.com/bresilla/drop/src/pkg/user"
)

// The merge: what the ordered changes come to when several people have been editing at once.
//
// Every change carries a whole file, so the content of the note as one person saw it is that
// person's change and nothing has to be replayed to find it. What is left is the join: where two
// changes are concurrent — neither is behind the other — the two files are merged three-way
// against the file their common ancestors make, which is the same recursion git does and the reason
// "Alice edited the top, Bob the bottom" comes out with both.

// Aside is a version that was not merged into the file, to be kept beside it.
type Aside struct {
	// Who is whose version it is, and Body is what they saved.
	Who  string
	Body []byte
}

// maxCells caps the table two versions are lined up in. A middle bigger than that is left as one
// changed region, which merges as a whole rather than line by line.
const maxCells = 1 << 21

// maxLine is the longest line a file may have and still be treated as lines.
const maxLine = 1 << 16

// Whole is the file a set of changes makes, and whatever could not be merged into it.
//
// named says what to call the person who signed with a key; an empty answer, or no function at all,
// leaves the key to speak for itself.
func Whole(changes []history.Change, named func(author string) string) ([]byte, []Aside) {
	if len(changes) == 0 {
		return nil, nil
	}

	w := &weave{by: make(map[history.ID]history.Change, len(changes)), named: named, made: map[string][]byte{}}
	for _, c := range changes {
		w.by[c.ID()] = c
	}
	return w.join(w.heads())
}

// weave is one replay: the changes by id, and what has already been worked out.
type weave struct {
	by    map[history.ID]history.Change
	named func(author string) string
	made  map[string][]byte
}

// heads is the changes nothing else here comes after, in the one order they are taken.
func (w *weave) heads() []history.ID {
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

// join is the file a set of heads makes.
//
// One head is one person's file, whole, exactly as they saved it. Several are merged in the order
// their ids are in, each against the file the two sides last agreed on.
func (w *weave) join(heads []history.ID) ([]byte, []Aside) {
	switch len(heads) {
	case 0:
		return nil, nil
	case 1:
		return w.by[heads[0]].Body, nil
	}

	if made, held := w.made[key(heads)]; held {
		return made, nil
	}

	made := w.by[heads[0]].Body
	mine := []string{w.who(heads[0])}
	var aside []Aside

	for i, head := range heads[1:] {
		base, _ := w.join(w.between(heads[:i+1], head))
		theirs := w.by[head]

		var kept []Aside
		made, kept = merge(base, made, theirs.Body, strings.Join(mine, ", "), w.who(head))
		aside = append(aside, kept...)
		mine = append(mine, w.who(head))
	}

	w.made[key(heads)] = made
	return made, aside
}

// between is the last thing two sides agreed on: the changes both are behind, minus the ones that
// are themselves behind another of them.
func (w *weave) between(mine []history.ID, theirs history.ID) []history.ID {
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
func (w *weave) behind(heads []history.ID) map[history.ID]bool {
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
func (w *weave) who(id history.ID) string { return whose(w.by[id].Author, w.named) }

// merge is one three-way merge of two versions of a file against the one they came from.
//
// A file that is not text is not merged at all: the version whose change is ordered later becomes
// the file and the other is handed back to be kept beside it. Merging a database line by line
// destroys it, and there is no undoing that, so anything this is not sure about is kept both ways.
func merge(base, ours, theirs []byte, us, them string) ([]byte, []Aside) {
	switch {
	case bytes.Equal(ours, theirs):
		return ours, nil
	case bytes.Equal(base, ours):
		return theirs, nil
	case bytes.Equal(base, theirs):
		return ours, nil
	}

	if !textual(base) || !textual(ours) || !textual(theirs) {
		return theirs, []Aside{{Who: us, Body: ours}}
	}
	return lineWise(base, ours, theirs, us, them)
}

// lineWise walks the two versions against the base together, taking a run of lines when only one
// side touched it and marking it when both did.
func lineWise(base, ours, theirs []byte, us, them string) ([]byte, []Aside) {
	b, o, t := split(base), split(ours), split(theirs)
	om, tm := matched(b, o), matched(b, t)

	var out []byte
	bi, oi, ti := 0, 0, 0

	for bi < len(b) || oi < len(o) || ti < len(t) {
		// A line both sides left where it was, with nothing put in front of it, is that line.
		if bi < len(b) && om[bi] == oi && tm[bi] == ti {
			out = append(out, b[bi]...)
			bi, oi, ti = bi+1, oi+1, ti+1
			continue
		}

		next := bi
		for next < len(b) && !(om[next] >= oi && tm[next] >= ti) {
			next++
		}
		oNext, tNext := len(o), len(t)
		if next < len(b) {
			oNext, tNext = om[next], tm[next]
		}

		was, mine, yours := run(b[bi:next]), run(o[oi:oNext]), run(t[ti:tNext])
		switch {
		case bytes.Equal(mine, yours):
			out = append(out, mine...)
		case bytes.Equal(mine, was):
			out = append(out, yours...)
		case bytes.Equal(yours, was):
			out = append(out, mine...)
		default:
			out = append(out, marked(mine, yours, us, them)...)
		}
		bi, oi, ti = next, oNext, tNext
	}
	return out, nil
}

// marked is a run of lines both sides changed, written the way everybody already reads one, with
// the two people named instead of "ours" and "theirs".
func marked(mine, yours []byte, us, them string) []byte {
	var out []byte
	out = append(out, "<<<<<<< "+us+"\n"...)
	out = append(out, ended(mine)...)
	out = append(out, "=======\n"...)
	out = append(out, ended(yours)...)
	out = append(out, ">>>>>>> "+them+"\n"...)
	return out
}

// ended is a run of lines with a line ending on the last of them, so a marker starts where a line
// starts.
func ended(run []byte) []byte {
	if len(run) == 0 || run[len(run)-1] == '\n' {
		return run
	}
	return append(append([]byte(nil), run...), '\n')
}

// run is a stretch of lines as the bytes they are.
func run(lines []string) []byte {
	var out []byte
	for _, line := range lines {
		out = append(out, line...)
	}
	return out
}

// split is a file as its lines, each keeping its own ending, so putting them back together is
// putting them back together.
func split(raw []byte) []string {
	var out []string
	for len(raw) > 0 {
		at := bytes.IndexByte(raw, '\n')
		if at < 0 {
			out = append(out, string(raw))
			break
		}
		out = append(out, string(raw[:at+1]))
		raw = raw[at+1:]
	}
	return out
}

// matched says, for each line of base, which line of other it is, and -1 for one that is neither
// there nor anywhere.
//
// What both ends share is taken off first, so the usual edit — a few lines changed in a long file —
// never reaches the table at all.
func matched(base, other []string) []int {
	out := make([]int, len(base))
	for i := range out {
		out[i] = -1
	}

	head := 0
	for head < len(base) && head < len(other) && base[head] == other[head] {
		out[head] = head
		head++
	}
	tail := 0
	for tail < len(base)-head && tail < len(other)-head &&
		base[len(base)-1-tail] == other[len(other)-1-tail] {
		out[len(base)-1-tail] = len(other) - 1 - tail
		tail++
	}

	lineUp(base[head:len(base)-tail], other[head:len(other)-tail], out[head:len(base)-tail], head)
	return out
}

// lineUp fills in the longest run of lines the two middles have in common, in order.
func lineUp(base, other []string, into []int, off int) {
	n, m := len(base), len(other)
	if n == 0 || m == 0 || int64(n)*int64(m) > maxCells {
		return
	}

	// table[i][j] is how many lines base[i:] and other[j:] still have in common.
	table := make([]int32, (n+1)*(m+1))
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			switch {
			case base[i] == other[j]:
				table[i*(m+1)+j] = table[(i+1)*(m+1)+j+1] + 1
			case table[(i+1)*(m+1)+j] >= table[i*(m+1)+j+1]:
				table[i*(m+1)+j] = table[(i+1)*(m+1)+j]
			default:
				table[i*(m+1)+j] = table[i*(m+1)+j+1]
			}
		}
	}

	for i, j := 0, 0; i < n && j < m; {
		switch {
		case base[i] == other[j]:
			into[i] = j + off
			i, j = i+1, j+1
		case table[(i+1)*(m+1)+j] >= table[i*(m+1)+j+1]:
			i++
		default:
			j++
		}
	}
}

// textual reports whether a file is lines of text, and says no whenever it is not sure.
//
// Valid UTF-8, nothing that a terminal would take as a control byte, and no line so long that
// calling it a line means nothing. A SQLite file fails on the byte after its header, which is the
// whole point of asking.
func textual(raw []byte) bool {
	if !utf8.Valid(raw) {
		return false
	}

	since := 0
	for _, b := range raw {
		if b == '\n' {
			since = 0
			continue
		}
		if b < 0x20 && b != '\t' && b != '\r' || b == 0x7f {
			return false
		}
		since++
		if since > maxLine {
			return false
		}
	}
	return true
}

// whose is what to call the person who signed with a key: what this machine calls them, the comment
// on the key, or the key itself in as few characters as still tell it from another.
func whose(author string, named func(string) string) string {
	if named != nil {
		if name := oneLine(named(author)); name != "" {
			return name
		}
	}

	key, comment, _, _, err := ssh.ParseAuthorizedKey([]byte(author))
	if err != nil {
		return "somebody"
	}
	if name := oneLine(comment); name != "" {
		return name
	}

	_, print, _ := strings.Cut(user.Fingerprint(key), ":")
	if len(print) > 8 {
		print = print[:8]
	}
	return "key " + print
}

// oneLine is a name as it can be written into a file that is read line by line.
func oneLine(name string) string {
	name = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' {
			return ' '
		}
		return r
	}, name)
	return strings.TrimSpace(name)
}

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
