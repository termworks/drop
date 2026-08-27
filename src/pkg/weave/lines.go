package weave

import (
	"bytes"
	"math"
	"strings"
	"unicode/utf8"

	"golang.org/x/crypto/ssh"

	"github.com/bresilla/drop/src/pkg/user"
)

// Putting two versions of a file together, which is the one merge drop does.
//
// Line-wise for text and keep-both for everything else. The rule for which is which has to be
// written down and asked conservatively, because merging a database line by line destroys it and
// there is no undoing that.

// maxCells caps the table two versions are lined up in. A middle bigger than that is left as one
// changed region, which merges as a whole rather than line by line.
const maxCells = 1 << 21

// maxLine is the longest line a file may have and still be treated as lines.
const maxLine = 1 << 16

// Bytes is one three-way merge of two versions of a file against the one they came from.
//
// A file that is not text is not merged at all: the version whose change is ordered later becomes
// the file and the other is handed back to be kept beside it. Merging a database line by line
// destroys it, and there is no undoing that, so anything this is not sure about is kept both ways.
func Bytes(base, ours, theirs []byte, us, them string) ([]byte, []Aside) {
	switch {
	case bytes.Equal(ours, theirs):
		return ours, nil
	case bytes.Equal(base, ours):
		return theirs, nil
	case bytes.Equal(base, theirs):
		return ours, nil
	}

	if !Textual(base) || !Textual(ours) || !Textual(theirs) {
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

// Unsettled is who a file still names in a conflict nobody has taken out of it.
//
// A merge that could not choose says so in the file and nowhere else, so this reads the markers
// back: whoever holds the file can be told there is something in it to settle, rather than finding
// out when somebody notices the file has gone strange.
func Unsettled(raw []byte) []string {
	var out []string
	us, open := "", false

	for _, line := range split(raw) {
		text := strings.TrimRight(line, "\r\n")
		switch {
		case strings.HasPrefix(text, "<<<<<<< "):
			us, open = strings.TrimSpace(text[len("<<<<<<< "):]), false
		case us != "" && text == "=======":
			open = true
		case open && strings.HasPrefix(text, ">>>>>>> "):
			out = append(out, us, strings.TrimSpace(text[len(">>>>>>> "):]))
			us, open = "", false
		}
	}
	return tidied(out)
}

// tidied is a list of names with each said once, in the order they were met.
func tidied(names []string) []string {
	var out []string
	said := make(map[string]bool, len(names))
	for _, name := range names {
		if name == "" || said[name] {
			continue
		}
		said[name] = true
		out = append(out, name)
	}
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
// The lines that are left out of each side are worked out first, and what remains on the two sides
// then pairs off in order, because the lines nobody touched are the same lines in the same order.
func matched(base, other []string) []int {
	first, second := make([]bool, len(base)), make([]bool, len(other))
	theirs, at := heard(base, other, first)
	ours, to := heard(other, base, second)

	left, right := make([]bool, len(theirs)), make([]bool, len(ours))
	spread := len(theirs) + len(ours) + 3
	forth, back := make([]int, spread), make([]int, spread)
	trace(theirs, ours, span{0, len(theirs), 0, len(ours)}, left, right, forth, back, len(ours)+1)
	for i, gone := range left {
		first[at[i]] = gone
	}
	for i, gone := range right {
		second[to[i]] = gone
	}

	settle(base, other, first, second)
	settle(other, base, second, first)
	return lined(first, second)
}

// heard is the lines of a file the other one has somewhere, and marks the rest as changed.
//
// A line the other side does not have anywhere can only be a change, and taking it out before the
// two are walked against each other keeps it from pulling the walk off the lines that do line up.
func heard(rec, other []string, chg []bool) ([]string, []int) {
	has := make(map[string]struct{}, len(other))
	for _, line := range other {
		has[line] = struct{}{}
	}

	lines, at := make([]string, 0, len(rec)), make([]int, 0, len(rec))
	for i, line := range rec {
		if _, held := has[line]; !held {
			chg[i] = true
			continue
		}
		lines, at = append(lines, line), append(at, i)
	}
	return lines, at
}

// span is a stretch of one file against a stretch of the other, which is what a lining-up is worked
// out inside.
type span struct{ x0, x1, y0, y1 int }

// trace marks the lines the two stretches do not share.
//
// What both ends already agree on comes off first, so the usual edit — a few lines changed in a
// long file — is a short walk. What is left is cut in two at the point the shortest way through it
// passes through, and each half is a smaller one of the same question.
func trace(base, other []string, at span, first, second []bool, forth, back []int, off int) {
	for at.x0 < at.x1 && at.y0 < at.y1 && base[at.x0] == other[at.y0] {
		at.x0, at.y0 = at.x0+1, at.y0+1
	}
	for at.x0 < at.x1 && at.y0 < at.y1 && base[at.x1-1] == other[at.y1-1] {
		at.x1, at.y1 = at.x1-1, at.y1-1
	}

	if at.x0 < at.x1 && at.y0 < at.y1 && int64(at.x1-at.x0)*int64(at.y1-at.y0) <= maxCells {
		x, y := meet(base, other, at, forth, back, off)
		trace(base, other, span{at.x0, x, at.y0, y}, first, second, forth, back, off)
		trace(base, other, span{x, at.x1, y, at.y1}, first, second, forth, back, off)
		return
	}
	for i := at.x0; i < at.x1; i++ {
		first[i] = true
	}
	for i := at.y0; i < at.y1; i++ {
		second[i] = true
	}
}

// meet is where the shortest way through a stretch is cut in two: the point the walk from the front
// and the walk from the back first reach together.
func meet(base, other []string, at span, forth, back []int, off int) (int, int) {
	low, high := at.x0-at.y1, at.x1-at.y0
	front, rear := at.x0-at.y0, at.x1-at.y1
	odd := (front-rear)&1 != 0
	fmin, fmax, bmin, bmax := front, front, rear, rear

	forth[front+off], back[rear+off] = at.x0, at.x1
	for {
		if fmin > low {
			fmin--
			forth[fmin-1+off] = -1
		} else {
			fmin++
		}
		if fmax < high {
			fmax++
			forth[fmax+1+off] = -1
		} else {
			fmax--
		}
		for d := fmax; d >= fmin; d -= 2 {
			x := forth[d+1+off]
			if forth[d-1+off] >= x {
				x = forth[d-1+off] + 1
			}
			y := x - d
			for x < at.x1 && y < at.y1 && base[x] == other[y] {
				x, y = x+1, y+1
			}
			forth[d+off] = x
			if odd && bmin <= d && d <= bmax && back[d+off] <= x {
				return x, y
			}
		}

		if bmin > low {
			bmin--
			back[bmin-1+off] = math.MaxInt
		} else {
			bmin++
		}
		if bmax < high {
			bmax++
			back[bmax+1+off] = math.MaxInt
		} else {
			bmax--
		}
		for d := bmax; d >= bmin; d -= 2 {
			x := back[d+1+off] - 1
			if back[d-1+off] < back[d+1+off] {
				x = back[d-1+off]
			}
			y := x - d
			for x > at.x0 && y > at.y0 && base[x-1] == other[y-1] {
				x, y = x-1, y-1
			}
			back[d+off] = x
			if !odd && fmin <= d && d <= fmax && x <= forth[d+off] {
				return x, y
			}
		}
	}
}

// lined is the lining-up two sets of edited lines make: what is left of one side against what is
// left of the other, in the order both are in.
func lined(first, second []bool) []int {
	out := make([]int, len(first))
	at := 0
	for i, gone := range first {
		if gone {
			out[i] = -1
			continue
		}
		for second[at] {
			at++
		}
		out[i], at = at, at+1
	}
	return out
}

// settle puts every changed stretch where it would be if it were the only place it could go.
//
// A file with repeated lines — a blank line, a closing brace, a list item said twice — can be lined
// up against another in more than one way, and which of them the trims and the table happen to pick
// depends on what else is around it. The two sides of a merge are lined up separately, so one edit
// that settled differently in the two looks like two edits, or two look like one, and a line is
// doubled or dropped with nothing said. So each stretch is moved down as far as it goes, taking in
// any stretch it meets, and then back up to where it lines up with a stretch of the other side —
// which is the same place whatever the two files around it look like.
func settle(rec, other []string, chg, ochg []bool) {
	at := &stretch{rec: rec, chg: chg}
	with := &stretch{rec: other, chg: ochg}
	at.first()
	with.first()

	for {
		if at.end > at.start {
			earliest, lines := 0, -1
			for {
				size := at.end - at.start
				lines = -1
				for at.up() {
					with.back()
				}
				earliest = at.end
				if with.end > with.start {
					lines = at.end
				}
				for at.down() {
					with.on()
					if with.end > with.start {
						lines = at.end
					}
				}
				if size == at.end-at.start {
					break
				}
			}
			for at.end != earliest && lines >= 0 && with.end == with.start && at.up() {
				with.back()
			}
		}
		if !at.on() {
			return
		}
		with.on()
	}
}

// stretch is one run of changed lines in a file, and the file it is in.
type stretch struct {
	rec        []string
	chg        []bool
	start, end int
}

// first is the run a file opens with, which is empty when its first line is unchanged.
func (s *stretch) first() {
	for s.end < len(s.chg) && s.chg[s.end] {
		s.end++
	}
}

// on is the next run, one unchanged line along, and says no at the end of the file.
func (s *stretch) on() bool {
	if s.end == len(s.chg) {
		return false
	}
	s.start = s.end + 1
	for s.end = s.start; s.end < len(s.chg) && s.chg[s.end]; s.end++ {
	}
	return true
}

// back is the run before this one, and says no at the start of the file.
func (s *stretch) back() bool {
	if s.start == 0 {
		return false
	}
	s.end = s.start - 1
	for s.start = s.end; s.start > 0 && s.chg[s.start-1]; s.start-- {
	}
	return true
}

// up moves the run one line earlier, which it can do when the line above it is the line it ends
// with, and takes in the run above if it reaches it.
func (s *stretch) up() bool {
	if s.start == 0 || s.rec[s.start-1] != s.rec[s.end-1] {
		return false
	}
	s.start, s.end = s.start-1, s.end-1
	s.chg[s.start], s.chg[s.end] = true, false
	for s.start > 0 && s.chg[s.start-1] {
		s.start--
	}
	return true
}

// down moves the run one line later, which it can do when the line below it is the line it starts
// with, and takes in the run below if it reaches it.
func (s *stretch) down() bool {
	if s.end == len(s.chg) || s.rec[s.start] != s.rec[s.end] {
		return false
	}
	s.chg[s.start], s.chg[s.end] = false, true
	s.start, s.end = s.start+1, s.end+1
	for s.end < len(s.chg) && s.chg[s.end] {
		s.end++
	}
	return true
}

// Textual reports whether a file is lines of text, and says no whenever it is not sure.
//
// Valid UTF-8, nothing that a terminal would take as a control byte, and no line so long that
// calling it a line means nothing. A SQLite file fails on the byte after its header, which is the
// whole point of asking.
func Textual(raw []byte) bool {
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

// Whose is what to call the person who signed with a key: what this machine calls them, the comment
// on the key, or the key itself in as few characters as still tell it from another.
func Whose(author string, named func(string) string) string {
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

// Safe is a person's name as a piece of a file name.
func Safe(who string) string {
	out := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			return r
		case r == '.' || r == '_' || r == '-':
			return r
		}
		return '-'
	}, who)

	out = strings.Trim(out, "-.")
	if out == "" {
		return "somebody"
	}
	return out
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
