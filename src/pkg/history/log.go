package history

import (
	"bytes"
	"container/heap"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/bresilla/drop/src/pkg/convo"
)

// What one thing's whole record may weigh.
//
// A history is a means rather than a feature: it is folded once everybody holding the thing has
// caught up, and these are how far it may run before that happens. Past them a change is refused
// rather than written, because a record nobody bounded is a peer's disk quota rather than a
// history — and a fold is taken whatever they say, since it is what makes the record smaller.
const (
	// MaxHeld is how many changes one log holds.
	MaxHeld = 1 << 14
	// MaxNamed is how many times, in all, those changes may name one another. Ordering walks every
	// one of those, so it is the number that decides what reading a history costs.
	MaxNamed = 1 << 18
	// MaxLog is how many bytes the file may reach.
	MaxLog = 1 << 25
)

// mark begins every record on disk, so a walk that loses its place can find the next one rather
// than reading the rest of the file as rubble.
var mark = []byte("drop")

// maxRecord is the longest a record can honestly claim to be.
const maxRecord = maxCipher + 3*binaryHead + 1

// Log is one thing's record on disk.
//
// Append-only, except when it is folded: a record is written once and never rewritten, so a crash
// midway can truncate the tail but cannot spoil what came before, and a fold replaces the whole
// file at once through a rename. Every change is also held in memory, because ordering a history
// means walking all of it and there is no useful answer that reads only part of one.
type Log struct {
	mu   sync.Mutex
	at   string
	dir  string
	file string
	seen string
	// changes is everything the log holds, by id. size is how long the file was when that was
	// built, so another drop appending to the same log is noticed without rereading it every time.
	changes map[ID]Change
	// folded says which change stands in place of one that was folded away, tips are the changes
	// nothing here comes after, and named is how many times a held change names another.
	folded map[ID]ID
	tips   map[ID]bool
	named  int
	size   int64
	read   bool
}

// open holds one Log per directory, so everything in this process that touches one thing shares
// its lock and the changes it has already read.
var open struct {
	sync.Mutex
	logs map[string]*Log
}

// Open prepares one thing's record, named by the id of the thing.
//
// It lives in the data directory beside the conversations, because a history is what happened
// rather than a setting: it is the thing itself, and losing it loses the thing.
func Open(at string) (*Log, error) {
	if err := nameable(at); err != nil {
		return nil, err
	}

	base, err := convo.DataDir()
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(base, "history", at)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("creating %s: %w", dir, err)
	}

	open.Lock()
	defer open.Unlock()

	if l, has := open.logs[dir]; has {
		return l, nil
	}
	l := &Log{
		at:   at,
		dir:  dir,
		file: filepath.Join(dir, "log"),
		seen: filepath.Join(dir, "seen"),
	}
	if open.logs == nil {
		open.logs = map[string]*Log{}
	}
	open.logs[dir] = l
	return l, nil
}

// At is the thing this record is about. A change bound to anything else does not belong here.
func (l *Log) At() string { return l.at }

// Dir is where this log lives, for whoever reads it and has one more thing to remember about the
// thing it is about. It exists already, and nothing else is in it that this package did not put
// there.
func (l *Log) Dir() string { return l.dir }

// nameable reports whether a thing's id can be a directory of its own. It becomes a path, so one
// that would climb out of the history directory or name nothing at all is refused rather than
// joined.
func nameable(at string) error {
	if at == "" {
		return errors.New("a history needs the id of the thing it is about")
	}
	if at == "." || at == ".." || strings.ContainsAny(at, `/\`) || strings.ContainsRune(at, 0) {
		return fmt.Errorf("%q is not a thing's id", at)
	}
	return nil
}

// Add takes one change: checks it was made about this thing, that the person it names really
// signed it, that everything it names is here or folded into something that is, and writes it down.
//
// Adding one that is already here is not a failure. It writes nothing and answers the same id,
// which is what makes delivering a change twice harmless.
//
// A change naming something this log has never seen is refused. It cannot be placed in any order,
// so taking it would be taking something that can never be replayed.
func (l *Log) Add(c Change) (ID, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if err := l.load(); err != nil {
		return ID{}, err
	}
	return l.take(c)
}

// take is Add with the log already read and locked.
func (l *Log) take(c Change) (ID, error) {
	id := c.ID()
	if _, has := l.changes[id]; has {
		return id, nil
	}

	plain := record(c)
	if err := l.takeable(c, len(plain)+sealCost); err != nil {
		return ID{}, fmt.Errorf("taking change %s: %w", id, err)
	}
	body, err := stored(plain, l.at, id)
	if err != nil {
		return ID{}, err
	}
	if err := l.write(body); err != nil {
		return ID{}, err
	}

	l.changes[id] = c
	if c.Whole() {
		return id, l.fold(c)
	}
	for _, was := range l.after(c) {
		delete(l.tips, was)
	}
	l.tips[id] = true
	l.named += len(c.Heads)
	return id, nil
}

// takeable is everything that must be true of a change before it is written down.
func (l *Log) takeable(c Change, weight int) error {
	if c.About != l.at {
		return fmt.Errorf("it was made about %s, and this is the history of %s", brief(c.About), brief(l.at))
	}
	switch {
	case len(c.Body) > MaxBody:
		return fmt.Errorf("its body is %d bytes, over the %d limit", len(c.Body), MaxBody)
	case len(c.Heads) > MaxHeads:
		return fmt.Errorf("it names %d changes, over the %d limit", len(c.Heads), MaxHeads)
	case len(c.Fold) > MaxHeld:
		return fmt.Errorf("it stands for %d changes, over the %d limit", len(c.Fold), MaxHeld)
	case !tidied(c.Heads) || !tidied(c.Fold):
		return errors.New("the changes it names are out of order, or one of them twice")
	}

	// A fold stands in place of the changes it covers, and every change that named one of those is
	// read as naming the fold instead. So a fold that names as a head something it does not cover
	// is a fold that a covered change gets placed behind — while the fold is placed behind it. That
	// is a circle, ordering refuses a history with one in it, and the history is on a disk: the
	// namespace would be unreadable from then on, on every machine that took the change, for good.
	//
	// A fold made here cannot look like that. Its heads are the tips and it covers everything, so
	// every head is inside its own cover. One that is not was not made by folding.
	if c.Whole() {
		for _, head := range c.Heads {
			if !names(c.Fold, head) {
				return fmt.Errorf("it stands for what came before it and yet names %s, which it does not stand for", brief(head.String()))
			}
		}
	}

	// A fold is what makes a full log smaller, so what a full log refuses does not apply to it.
	if !c.Whole() {
		tips := l.tipping(c)
		switch {
		case len(l.changes) >= MaxHeld:
			return fmt.Errorf("this history already holds %d changes, and that is the limit", MaxHeld)
		case l.named+len(c.Heads) > MaxNamed:
			return fmt.Errorf("this history already names %d changes, and %d is the limit", l.named, MaxNamed)
		case l.size+int64(weight) > MaxLog:
			return fmt.Errorf("this history is %d bytes, and %d is the limit", l.size, MaxLog)
		case tips > MaxHeads:
			return fmt.Errorf("this history is being changed %d ways at once, and %d is the limit", tips, MaxHeads)
		}
	}

	if err := verify(c); err != nil {
		return err
	}
	for _, head := range c.Heads {
		if _, has := l.changes[head]; has {
			continue
		}
		if _, folded := l.folded[head]; folded || names(c.Fold, head) {
			continue
		}
		return fmt.Errorf("it names %s, which is not here", head)
	}
	return nil
}

// tipping is how many ways the thing would be being changed at once if this change were taken.
func (l *Log) tipping(c Change) int {
	covered := map[ID]bool{}
	for _, was := range l.after(c) {
		if l.tips[was] {
			covered[was] = true
		}
	}
	return len(l.tips) + 1 - len(covered)
}

// brief is a thing's id as it is said in an error.
func brief(at string) string {
	if len(at) > 12 {
		return at[:12]
	}
	return at
}

// Heads is the changes nothing else comes after: everything this log holds, said in as few ids as
// it can be said. A log that cannot be read has none, and the call that goes on to add or order
// says why.
func (l *Log) Heads() []ID {
	l.mu.Lock()
	defer l.mu.Unlock()

	if err := l.load(); err != nil {
		return nil
	}

	out := make([]ID, 0, len(l.tips))
	for id := range l.tips {
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return bytes.Compare(out[i][:], out[j][:]) < 0 })
	return out
}

// Ordered is every change, in the order every machine holding this thing derives from it.
//
// Topological, so a change is never placed before one it names, with ties broken on id. That one
// property is what makes two machines given the same changes in different orders read the same
// history. At has no part in it.
func (l *Log) Ordered() ([]Change, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if err := l.load(); err != nil {
		return nil, err
	}

	order, err := l.sorted()
	if err != nil {
		return nil, err
	}
	out := make([]Change, 0, len(order))
	for _, id := range order {
		out = append(out, l.changes[id])
	}
	return out, nil
}

// Since is what somebody at those heads has not seen, in the order they can be taken: everything
// here that is neither one of those changes nor behind one.
//
// A head this log has never heard of stands for changes it does not hold, so it covers nothing
// here and is passed over. What it stands for arrives the other way round, when that peer sends
// this one what it is missing — and a head that was folded away here is the same thing said
// differently, so what that peer gets back is the fold rather than the log it missed.
func (l *Log) Since(heads []ID) ([]Change, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if err := l.load(); err != nil {
		return nil, err
	}

	theirs := l.behind(heads)
	order, err := l.sorted()
	if err != nil {
		return nil, err
	}

	out := make([]Change, 0, len(order))
	for _, id := range order {
		if !theirs[id] {
			out = append(out, l.changes[id])
		}
	}
	return out, nil
}

// Has reports whether a change is already here.
func (l *Log) Has(id ID) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	if err := l.load(); err != nil {
		return false
	}
	_, has := l.changes[id]
	return has
}

// behind is those heads and everything they were made after, of what is held here.
func (l *Log) behind(heads []ID) map[ID]bool {
	seen := make(map[ID]bool, len(l.changes))

	var walk []ID
	for _, head := range heads {
		if _, has := l.changes[head]; has && !seen[head] {
			seen[head] = true
			walk = append(walk, head)
		}
	}
	for len(walk) > 0 {
		at := walk[len(walk)-1]
		walk = walk[:len(walk)-1]
		for _, head := range l.changes[at].Heads {
			if _, has := l.changes[head]; has && !seen[head] {
				seen[head] = true
				walk = append(walk, head)
			}
		}
	}
	return seen
}

// after is what a change is placed behind here.
//
// Usually the changes it names. One that was folded away is placed behind the fold that stands in
// its place instead, so a machine that folded and one that did not still read the same order — and
// a fold is never placed behind another fold covering the same ground, because neither came first.
func (l *Log) after(c Change) []ID {
	id := c.ID()
	out := make([]ID, 0, len(c.Heads))
	for _, head := range c.Heads {
		if _, has := l.changes[head]; has {
			out = append(out, head)
			continue
		}
		fold, folded := l.folded[head]
		if folded && fold != id && !names(c.Fold, head) {
			out = append(out, fold)
		}
	}
	return out
}

// sorted is the ids of every change, in the one order they have.
//
// A change is ready once everything it is placed behind has been placed, and of the changes that
// are ready the smallest id goes next. Which changes were ready first depends on the order they
// arrived in; which of them is chosen does not.
func (l *Log) sorted() ([]ID, error) {
	waiting := make(map[ID]int, len(l.changes))
	next := make(map[ID][]ID, len(l.changes))

	var ready ids
	for id, c := range l.changes {
		behind := l.after(c)
		waiting[id] = len(behind)
		for _, was := range behind {
			next[was] = append(next[was], id)
		}
		if len(behind) == 0 {
			ready = append(ready, id)
		}
	}
	heap.Init(&ready)

	out := make([]ID, 0, len(l.changes))
	for ready.Len() > 0 {
		id := heap.Pop(&ready).(ID)
		out = append(out, id)
		for _, after := range next[id] {
			waiting[after]--
			if waiting[after] == 0 {
				heap.Push(&ready, after)
			}
		}
	}

	if len(out) != len(l.changes) {
		return nil, fmt.Errorf("ordering %s: %d changes name each other in a circle", l.at, len(l.changes)-len(out))
	}
	return out, nil
}

// ids is a heap of change ids, smallest first.
type ids []ID

func (s ids) Len() int           { return len(s) }
func (s ids) Less(i, j int) bool { return bytes.Compare(s[i][:], s[j][:]) < 0 }
func (s ids) Swap(i, j int)      { s[i], s[j] = s[j], s[i] }

func (s *ids) Push(v any) { *s = append(*s, v.(ID)) }

func (s *ids) Pop() any {
	has := *s
	last := has[len(has)-1]
	*s = has[:len(has)-1]
	return last
}

// framed is one record as it lies in the file: what says a record starts here, then how long it is,
// then the record.
func framed(body []byte) []byte {
	var head [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(head[:], uint64(len(body)))

	out := make([]byte, 0, len(mark)+n+len(body))
	out = append(out, mark...)
	out = append(out, head[:n]...)
	return append(out, body...)
}

// write appends one record and keeps track of how long the file now is.
//
// The length is taken from what was written rather than from the file afterwards: another drop
// appending to the same log between the two would otherwise be counted as already read, and its
// changes would never be picked up. When the numbers do not add up the log is simply read again.
func (l *Log) write(body []byte) error {
	raw := framed(body)

	was, err := l.length()
	if err != nil {
		return err
	}
	if err := l.append(raw); err != nil {
		return err
	}

	now, err := l.length()
	if err == nil && was == l.size && now == was+int64(len(raw)) {
		l.size = now
	} else {
		l.read = false
	}
	return nil
}

// length is how long the file is, and zero when there is none.
func (l *Log) length() (int64, error) {
	info, err := os.Stat(l.file)
	switch {
	case err == nil:
		return info.Size(), nil
	case errors.Is(err, os.ErrNotExist):
		return 0, nil
	default:
		return 0, fmt.Errorf("reading %s: %w", l.file, err)
	}
}

// append writes one record and flushes it, so a change reported taken is on the disk rather than in
// a buffer.
func (l *Log) append(raw []byte) error {
	file, err := os.OpenFile(l.file, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("opening %s: %w", l.file, err)
	}
	defer file.Close()

	if _, err := file.Write(raw); err != nil {
		return fmt.Errorf("writing %s: %w", l.file, err)
	}
	return file.Sync()
}

// rewrite puts the whole log back on disk, which is what folding it away needs.
//
// Through a temporary file and a rename, so an interruption leaves the log as it was rather than
// half of each. It is the one time a record already written is written again.
func (l *Log) rewrite() error {
	order, err := l.sorted()
	if err != nil {
		return err
	}

	var raw []byte
	for _, id := range order {
		body, err := stored(record(l.changes[id]), l.at, id)
		if err != nil {
			return err
		}
		raw = append(raw, framed(body)...)
	}

	scratch := l.file + ".new"
	if err := os.WriteFile(scratch, raw, 0o600); err != nil {
		return fmt.Errorf("writing %s: %w", scratch, err)
	}
	if err := os.Rename(scratch, l.file); err != nil {
		return fmt.Errorf("replacing %s: %w", l.file, err)
	}

	l.size, l.read = int64(len(raw)), true
	return nil
}

// load reads the log, and only when it has changed since it was last read.
//
// Every record starts with the same few bytes, so a walk that arrives somewhere that is not a
// record — a truncated tail from a crash mid-write, or a record damaged since — looks for where the
// next one starts rather than trusting a length it has no reason to believe. One damaged record
// costs one change; it does not cost every change written after it.
//
// A change nothing here can place is then dropped. Nothing is ever written down before what it
// names, so the only way one appears is a damaged record earlier in the file, and a change that
// cannot be placed in an order cannot be part of a history.
func (l *Log) load() error {
	size, err := l.length()
	if err != nil {
		return err
	}
	if l.read && size == l.size {
		return nil
	}

	raw, err := os.ReadFile(l.file)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("reading %s: %w", l.file, err)
	}

	changes := map[ID]Change{}
	for at := 0; at < len(raw); {
		start := bytes.Index(raw[at:], mark)
		if start < 0 {
			break
		}
		from := at + start + len(mark)
		at += start + 1

		width, used := binary.Uvarint(raw[from:])
		if used <= 0 || width > uint64(maxRecord) || from+used+int(width) > len(raw) {
			continue
		}
		c, err := unstored(raw[from+used:from+used+int(width)], l.at)
		if errors.Is(err, ErrLocked) {
			return err
		}
		if err != nil || c.About != l.at {
			continue
		}
		changes[c.ID()] = c
		at = from + used + int(width)
	}

	l.changes, l.size, l.read = changes, size, true
	l.index()
	return nil
}

// index is everything the changes held say about each other: what stands in place of what was
// folded away, which changes nothing comes after, and how many times one names another.
//
// A change nothing can place is dropped, and dropping one can leave another with nowhere to go, so
// it is asked again until nothing more falls out.
func (l *Log) index() {
	for {
		l.folded = map[ID]ID{}
		for id, c := range l.changes {
			for _, was := range c.Fold {
				if under, folded := l.folded[was]; !folded || bytes.Compare(id[:], under[:]) < 0 {
					l.folded[was] = id
				}
			}
		}

		back := make(map[ID][]ID, len(l.changes))
		var walk []ID
		for id, c := range l.changes {
			for _, head := range c.Heads {
				back[head] = append(back[head], id)
			}
			if !l.placeable(c) {
				walk = append(walk, id)
			}
		}

		again := false
		for len(walk) > 0 {
			id := walk[len(walk)-1]
			walk = walk[:len(walk)-1]

			c, has := l.changes[id]
			if !has || l.placeable(c) {
				continue
			}
			delete(l.changes, id)
			again = again || c.Whole()
			walk = append(walk, back[id]...)
		}
		if !again {
			break
		}
	}

	named := map[ID]bool{}
	l.named = 0
	for _, c := range l.changes {
		l.named += len(c.Heads)
		for _, was := range l.after(c) {
			named[was] = true
		}
	}

	l.tips = make(map[ID]bool, len(l.changes))
	for id := range l.changes {
		if !named[id] {
			l.tips[id] = true
		}
	}
}

// placeable reports whether everything a change names is here, or folded into something that is.
func (l *Log) placeable(c Change) bool {
	for _, head := range c.Heads {
		if _, has := l.changes[head]; has {
			continue
		}
		if _, folded := l.folded[head]; folded || names(c.Fold, head) {
			continue
		}
		return false
	}
	return true
}
