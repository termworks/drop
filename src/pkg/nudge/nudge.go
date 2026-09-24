// Package nudge says when something under a directory changed, so a loop that would have waited for
// its next turn can take it now.
//
// It is a nudge and not an answer. Whoever is told still goes and looks: the kernel says a directory
// moved, not what a file now holds, and the thing that decides what changed is the same code that
// decides it when nothing was heard at all. So a missed event costs a second and not a correctness
// bug — which matters, because events are missed. A watch has a per-user limit, a network filesystem
// reports nothing, and a directory replaced wholesale takes its watch with it.
//
// That is also why whoever uses this keeps their timer. This makes the common case quick; the timer
// is what makes every case eventually right.
package nudge

import (
	"context"
	"encoding/binary"
	"fmt"
	"sort"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

// Settle is how long the events for one save are let run together before anybody is told.
//
// An editor writing a file makes several: a temporary file created, written, renamed over the real
// one, the old one removed. Telling somebody four times about one save would have them scan four
// times, and the fourth scan is the only one that would have seen the whole of it anyway.
const Settle = 50 * time.Millisecond

// Watched is what one directory's watch cost, so a caller can be told it is at the limit rather
// than quietly hearing nothing.
type Ear struct {
	fd    int
	wake  [2]int
	heard chan struct{}

	mu sync.Mutex
	// by is the watch descriptor for each directory, and at is the reverse.
	by    map[string]int
	at    map[int]string
	dirty map[string]struct{}
	all   bool
	shut  bool
}

// Listen starts hearing. It returns an error on a system with no inotify, and a caller that gets one
// carries on with its timer.
func Listen(ctx context.Context) (*Ear, error) {
	fd, err := unix.InotifyInit1(unix.IN_CLOEXEC | unix.IN_NONBLOCK)
	if err != nil {
		return nil, fmt.Errorf("listening for changes: %w", err)
	}

	e := &Ear{
		fd:    fd,
		heard: make(chan struct{}, 1),
		by:    map[string]int{},
		at:    map[int]string{},
		dirty: map[string]struct{}{},
	}
	if err := unix.Pipe2(e.wake[:], unix.O_CLOEXEC|unix.O_NONBLOCK); err != nil {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("listening for changes: %w", err)
	}

	go e.read()
	go func() {
		<-ctx.Done()
		e.stop()
	}()
	return e, nil
}

// Heard is told whenever something under a watched directory changed, at most once per settling.
func (e *Ear) Heard() <-chan struct{} { return e.heard }

// Dirty returns the watched directories changed since the preceding call. All is true when the
// event queue could not identify every changed directory.
func (e *Ear) Dirty() (dirs []string, all bool) {
	e.mu.Lock()
	defer e.mu.Unlock()

	dirs = make([]string, 0, len(e.dirty))
	for dir := range e.dirty {
		dirs = append(dirs, dir)
	}
	sort.Strings(dirs)
	all = e.all
	e.dirty = map[string]struct{}{}
	e.all = false
	return dirs, all
}

// Mind watches exactly these directories and stops watching any other.
//
// Called every round with whatever is there now, so a namespace that appeared is heard and one that
// went takes its watch with it. A directory that cannot be watched is passed over: it is a directory
// that will be noticed by the timer, which is what the timer is for.
func (e *Ear) Mind(dirs []string) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.shut {
		return
	}

	want := make(map[string]bool, len(dirs))
	for _, dir := range dirs {
		want[dir] = true
	}
	for dir, wd := range e.by {
		if want[dir] {
			continue
		}
		_, _ = unix.InotifyRmWatch(e.fd, uint32(wd))
		delete(e.by, dir)
		delete(e.at, wd)
	}

	for dir := range want {
		if _, held := e.by[dir]; held {
			continue
		}
		wd, err := unix.InotifyAddWatch(e.fd, dir, mask)
		if err != nil {
			continue
		}
		e.by[dir], e.at[wd] = wd, dir
	}
}

// mask is every way a file under a directory can become different: written and closed, moved in or
// out, made, removed — and the directory itself going, which is how a watch is lost.
const mask = unix.IN_CLOSE_WRITE | unix.IN_MOVED_TO | unix.IN_MOVED_FROM |
	unix.IN_CREATE | unix.IN_DELETE | unix.IN_MOVE_SELF | unix.IN_DELETE_SELF

// read turns kernel events into nudges until it is stopped.
//
// The events themselves are thrown away after being read: what they say is that something changed
// somewhere being watched, and that is the whole of what is passed on. Reading them is still
// necessary, because an unread queue overflows and a queue that has overflowed says nothing more.
func (e *Ear) read() {
	defer func() {
		e.mu.Lock()
		e.shut = true
		e.mu.Unlock()
		_ = unix.Close(e.fd)
		_ = unix.Close(e.wake[0])
		close(e.heard)
	}()

	raw := make([]byte, 64*1024)
	for {
		fds := []unix.PollFd{
			{Fd: int32(e.fd), Events: unix.POLLIN},
			{Fd: int32(e.wake[0]), Events: unix.POLLIN},
		}
		if _, err := unix.Poll(fds, -1); err != nil {
			if err == unix.EINTR {
				continue
			}
			return
		}
		if fds[1].Revents != 0 {
			return
		}
		if fds[0].Revents&unix.POLLIN == 0 {
			continue
		}

		n, err := unix.Read(e.fd, raw)
		switch {
		case err == unix.EAGAIN || err == unix.EINTR:
			continue
		case err != nil, n <= 0:
			return
		}
		dirs, all := e.changes(raw[:n])
		e.say(dirs, all)
	}
}

func (e *Ear) changes(raw []byte) ([]string, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()

	dirty := map[string]struct{}{}
	all := false
	for len(raw) >= unix.SizeofInotifyEvent {
		wd := int(int32(binary.NativeEndian.Uint32(raw[0:4])))
		mask := binary.NativeEndian.Uint32(raw[4:8])
		nameLen := int(binary.NativeEndian.Uint32(raw[12:16]))
		size := unix.SizeofInotifyEvent + nameLen
		if size > len(raw) {
			all = true
			break
		}

		if mask&unix.IN_Q_OVERFLOW != 0 {
			all = true
		} else if dir, known := e.at[wd]; known {
			dirty[dir] = struct{}{}
			if mask&(unix.IN_MOVE_SELF|unix.IN_DELETE_SELF|unix.IN_IGNORED) != 0 {
				if mask&unix.IN_IGNORED == 0 {
					_, _ = unix.InotifyRmWatch(e.fd, uint32(wd))
				}
				delete(e.by, dir)
				delete(e.at, wd)
			}
		} else if mask&unix.IN_IGNORED == 0 {
			all = true
		}
		raw = raw[size:]
	}

	dirs := make([]string, 0, len(dirty))
	for dir := range dirty {
		dirs = append(dirs, dir)
	}
	return dirs, all
}

// say passes a nudge on, after letting the rest of one save arrive.
//
// The channel holds one. Changed directories accumulate until the receiver takes them with Dirty.
func (e *Ear) say(dirs []string, all bool) {
	time.Sleep(Settle)
	e.mu.Lock()
	for _, dir := range dirs {
		e.dirty[dir] = struct{}{}
	}
	e.all = e.all || all
	e.mu.Unlock()
	select {
	case e.heard <- struct{}{}:
	default:
	}
}

// stop wakes the reader so it can put everything down.
func (e *Ear) stop() {
	_, _ = unix.Write(e.wake[1], []byte{0})
	_ = unix.Close(e.wake[1])
}
