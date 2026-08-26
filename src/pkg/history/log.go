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

// Log is one thing's record on disk.
//
// Append-only: a record is written once and never rewritten, so a crash midway can truncate the
// tail but cannot spoil what came before. Every change is also held in memory, because ordering a
// history means walking all of it and there is no useful answer that reads only part of one.
type Log struct {
	mu   sync.Mutex
	at   string
	dir  string
	file string
	// changes is everything the log holds, by id. size is how long the file was when that was
	// built, so another drop appending to the same log is noticed without rereading it every time.
	changes map[ID]Change
	size    int64
	read    bool
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

	if l, held := open.logs[dir]; held {
		return l, nil
	}
	l := &Log{at: at, dir: dir, file: filepath.Join(dir, "log")}
	if open.logs == nil {
		open.logs = map[string]*Log{}
	}
	open.logs[dir] = l
	return l, nil
}

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

// Add takes one change: checks it was signed by the person it names, that everything it names is
// already here, and writes it down.
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

	id := c.ID()
	if _, held := l.changes[id]; held {
		return id, nil
	}

	if err := l.takeable(c); err != nil {
		return ID{}, fmt.Errorf("taking change %s: %w", id, err)
	}
	if err := l.append(record(c)); err != nil {
		return ID{}, err
	}

	l.changes[id] = c
	if info, err := os.Stat(l.file); err == nil {
		l.size = info.Size()
	} else {
		l.read = false
	}
	return id, nil
}

// takeable is everything that must be true of a change before it is written down.
func (l *Log) takeable(c Change) error {
	switch {
	case len(c.Body) > MaxBody:
		return fmt.Errorf("its body is %d bytes, over the %d limit", len(c.Body), MaxBody)
	case len(c.Heads) > MaxHeads:
		return fmt.Errorf("it names %d changes, over the %d limit", len(c.Heads), MaxHeads)
	case !tidied(c.Heads):
		return errors.New("the changes it names are out of order, or one of them twice")
	}

	if err := verify(c); err != nil {
		return err
	}
	for _, head := range c.Heads {
		if _, held := l.changes[head]; !held {
			return fmt.Errorf("it names %s, which is not here", head)
		}
	}
	return nil
}

// Heads is the changes nothing else names: everything this log holds, said in as few ids as it can
// be said. A log that cannot be read has none, and the call that goes on to add or order says why.
func (l *Log) Heads() []ID {
	l.mu.Lock()
	defer l.mu.Unlock()

	if err := l.load(); err != nil {
		return nil
	}

	named := make(map[ID]bool, len(l.changes))
	for _, c := range l.changes {
		for _, head := range c.Heads {
			named[head] = true
		}
	}

	var out []ID
	for id := range l.changes {
		if !named[id] {
			out = append(out, id)
		}
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
// this one what it is missing.
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
	_, held := l.changes[id]
	return held
}

// behind is those heads and everything they were made after.
func (l *Log) behind(heads []ID) map[ID]bool {
	seen := make(map[ID]bool, len(l.changes))

	var walk []ID
	for _, head := range heads {
		if _, held := l.changes[head]; held && !seen[head] {
			seen[head] = true
			walk = append(walk, head)
		}
	}
	for len(walk) > 0 {
		at := walk[len(walk)-1]
		walk = walk[:len(walk)-1]
		for _, head := range l.changes[at].Heads {
			if !seen[head] {
				seen[head] = true
				walk = append(walk, head)
			}
		}
	}
	return seen
}

// sorted is the ids of every change, in the one order they have.
//
// A change is ready once everything it names has been placed, and of the changes that are ready
// the smallest id goes next. Which changes were ready first depends on the order they arrived in;
// which of them is chosen does not.
func (l *Log) sorted() ([]ID, error) {
	waiting := make(map[ID]int, len(l.changes))
	after := make(map[ID][]ID, len(l.changes))

	var ready ids
	for id, c := range l.changes {
		waiting[id] = len(c.Heads)
		for _, head := range c.Heads {
			after[head] = append(after[head], id)
		}
		if len(c.Heads) == 0 {
			ready = append(ready, id)
		}
	}
	heap.Init(&ready)

	out := make([]ID, 0, len(l.changes))
	for ready.Len() > 0 {
		id := heap.Pop(&ready).(ID)
		out = append(out, id)
		for _, next := range after[id] {
			waiting[next]--
			if waiting[next] == 0 {
				heap.Push(&ready, next)
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
	held := *s
	last := held[len(held)-1]
	*s = held[:len(held)-1]
	return last
}

// append writes one length-prefixed record and flushes it, so a change reported taken is on the
// disk rather than in a buffer.
func (l *Log) append(body []byte) error {
	file, err := os.OpenFile(l.file, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("opening %s: %w", l.file, err)
	}
	defer file.Close()

	var head [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(head[:], uint64(len(body)))
	if _, err := file.Write(append(head[:n], body...)); err != nil {
		return fmt.Errorf("writing %s: %w", l.file, err)
	}
	return file.Sync()
}

// load reads the log, and only when it has changed since it was last read.
//
// A truncated tail — a crash mid-write — ends the walk rather than failing it, because the records
// before it are still good and losing them would be the worse outcome. A record that is whole and
// still will not read is stepped over instead: its length prefix says where the next one starts,
// so one damaged record costs one change rather than every change written after it.
//
// A change whose heads are then missing is stepped over too. Nothing is ever written down before
// what it names, so the only way one appears is a damaged record earlier in the file, and a change
// that cannot be placed in an order cannot be part of a history.
func (l *Log) load() error {
	var size int64
	info, err := os.Stat(l.file)
	switch {
	case err == nil:
		size = info.Size()
	case !errors.Is(err, os.ErrNotExist):
		return fmt.Errorf("reading %s: %w", l.file, err)
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
		width, used := binary.Uvarint(raw[at:])
		if used <= 0 || at+used+int(width) > len(raw) {
			break
		}
		at += used
		c, err := unrecord(raw[at : at+int(width)])
		at += int(width)
		if err != nil || !held(changes, c.Heads) {
			continue
		}
		changes[c.ID()] = c
	}

	l.changes, l.size, l.read = changes, size, true
	return nil
}

// held reports whether every one of those changes is already in the set.
func held(changes map[ID]Change, heads []ID) bool {
	for _, head := range heads {
		if _, there := changes[head]; !there {
			return false
		}
	}
	return true
}
