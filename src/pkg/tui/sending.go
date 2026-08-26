package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/bresilla/drop/src/pkg/book"
	"github.com/bresilla/drop/src/pkg/convo"
)

// Putting something into a namespace: a file into a files path, a URL into a link path.
//
// One interaction for both, because from where a person sits they are the same act — name a thing,
// press enter, watch it go — and the only difference is what counts as a name.

// moving is a transfer in flight. The sending goroutine writes it and the interface reads it, so
// every field goes through the lock.
type moving struct {
	mu   sync.Mutex
	what string
	done int64
	size int64
}

func (m *moving) update(name string, done, size int64) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.what, m.done, m.size = name, done, size
}

func (m *moving) read() (string, int64, int64) {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.what, m.done, m.size
}

type putDone struct {
	what string
	err  error
}

// tick asks for a redraw while something is moving, because the progress is written by a goroutine
// and nothing else would wake the interface to show it.
type tick struct{}

func ticking() tea.Cmd {
	return tea.Tick(120*time.Millisecond, func(time.Time) tea.Msg { return tick{} })
}

// putFile sends one file to a path on another device.
func putFile(back Backend, to book.Entry, path, file string, into *moving) tea.Cmd {
	return func() tea.Msg {
		ctx, stop := context.WithTimeout(context.Background(), 30*time.Minute)
		defer stop()

		err := back.Send(ctx, to, path, []string{file}, into.update)
		return putDone{what: filepath.Base(file), err: err}
	}
}

// putLink sends a URL to a path on another device.
func putLink(back Backend, to book.Entry, path, archetype, url string) tea.Cmd {
	return func() tea.Msg {
		ctx, stop := context.WithTimeout(context.Background(), 2*time.Minute)
		defer stop()

		err := back.Post(ctx, to, path, archetype, convo.KindLink, url)
		return putDone{what: url, err: err}
	}
}

// expand turns what somebody typed into a path this process can open.
func expand(typed string) string {
	typed = strings.TrimSpace(typed)
	typed = strings.Trim(typed, `"'`)

	if typed == "~" || strings.HasPrefix(typed, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			typed = filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(typed, "~"), "/"))
		}
	}
	if abs, err := filepath.Abs(typed); err == nil {
		return abs
	}
	return typed
}

// complete finishes a path the way a shell does: the longest prefix every candidate shares.
//
// Returned rather than applied, so the caller decides whether a completion that changes nothing is
// worth showing the candidates for.
func complete(typed string) (string, []string) {
	if typed == "" {
		typed = "./"
	}

	at := expand(typed)
	dir, prefix := filepath.Dir(at), filepath.Base(at)

	// A trailing separator means the directory itself, not something inside its parent.
	if strings.HasSuffix(typed, string(filepath.Separator)) || strings.HasSuffix(typed, "/") {
		dir, prefix = at, ""
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return typed, nil
	}

	var found []string
	for _, e := range entries {
		name := e.Name()
		if prefix != "" && !strings.HasPrefix(name, prefix) {
			continue
		}
		if prefix == "" && strings.HasPrefix(name, ".") {
			continue // a bare listing should not open with the dotfiles
		}
		if e.IsDir() {
			name += "/"
		}
		found = append(found, name)
	}
	sort.Strings(found)

	if len(found) == 0 {
		return typed, nil
	}
	return filepath.Join(dir, shared(found)), found
}

// shared is the longest prefix all of these have in common.
func shared(names []string) string {
	if len(names) == 0 {
		return ""
	}

	out := names[0]
	for _, name := range names[1:] {
		for !strings.HasPrefix(name, out) {
			out = out[:len(out)-1]
			if out == "" {
				return ""
			}
		}
	}
	return out
}

// sizeOf prints a byte count the way a person reads one.
func sizeOf(n int64) string {
	const step = 1024

	if n < step {
		return fmt.Sprintf("%d B", n)
	}

	div, unit := int64(step), 0
	for n/div >= step && unit < 3 {
		div *= step
		unit++
	}
	return fmt.Sprintf("%.1f %s", float64(n)/float64(div), [...]string{"KB", "MB", "GB", "TB"}[unit])
}
