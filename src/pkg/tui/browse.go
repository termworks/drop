package tui

import (
	"context"
	"path/filepath"
	"time"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/bresilla/drop/src/pkg/book"
)

// A namespace that is a directory is walked at a level of its own.
//
// A level rather than a screen drawn inside an open path: the list is what carries the arrows, the
// paging and the filtering, and a browser drawn anywhere else would have to invent all three.

// Held is one thing in a namespace that is a directory.
type Held struct {
	Name string
	Size int64
	// At is when it was last written.
	At time.Time
	// Dir marks something that can be walked into.
	Dir bool
}

// heldLoaded is one directory listing.
//
// The directory travels with the path. One namespace has as many listings as it has directories,
// and an answer matched on the path alone would land on whichever one is on screen by then.
type heldLoaded struct {
	path string
	dir  string
	held []Held
	err  error
}

// loadHeld reads one directory of a namespace of this machine's own.
func loadHeld(back Backend, path, dir string) tea.Cmd {
	return func() tea.Msg {
		held, err := back.Holding(path, dir)
		return heldLoaded{path: path, dir: dir, held: held, err: err}
	}
}

// loadListing reads one directory of a files namespace on another device.
func loadListing(back Backend, on book.Entry, path, dir string) tea.Cmd {
	return func() tea.Msg {
		ctx, stop := context.WithTimeout(context.Background(), askFor)
		defer stop()

		held, err := back.Listing(ctx, on, path, dir)
		return heldLoaded{path: path, dir: dir, held: held, err: err}
	}
}

// listing asks for the directory the browse level is standing in, on whichever machine it is on.
func (m Model) listing() tea.Cmd {
	at, ok := m.path()
	if !ok {
		return nil
	}
	if m.onSelf {
		return loadHeld(m.back, at.Path, m.dir)
	}

	with, ok := m.peer()
	if !ok {
		return nil
	}
	return loadListing(m.back, with, at.Path, m.dir)
}

// fetched is one thing copied out of a namespace, and where it landed on this disk.
type fetched struct {
	what string
	into string
	err  error
}

// fetch copies one thing out of a namespace onto this disk.
func fetch(back Backend, from book.Entry, path, dir, name string, into *moving) tea.Cmd {
	return func() tea.Msg {
		ctx, stop := context.WithTimeout(context.Background(), 30*time.Minute)
		defer stop()

		landed, err := back.Fetch(ctx, from, path, dir, name, into.update)
		return fetched{what: name, into: landed, err: err}
	}
}

// upload copies one thing from this disk into a namespace on another device.
func upload(back Backend, to book.Entry, path, dir, from string, into *moving) tea.Cmd {
	return func() tea.Msg {
		ctx, stop := context.WithTimeout(context.Background(), 30*time.Minute)
		defer stop()

		err := back.Put(ctx, to, path, dir, from, into.update)
		return putDone{what: filepath.Base(from), err: err}
	}
}

// removed is one thing taken off another machine.
type removed struct {
	what string
	err  error
}

// remove deletes one thing from a namespace on another device.
func remove(back Backend, on book.Entry, path, dir, name string) tea.Cmd {
	return func() tea.Msg {
		ctx, stop := context.WithTimeout(context.Background(), 2*time.Minute)
		defer stop()

		return removed{what: name, err: back.Remove(ctx, on, path, dir, name)}
	}
}

// heldItem is one line of a directory listing.
type heldItem struct {
	held Held
	// at is the whole path this stands for, on the device it is on.
	at string
	on string
}

func (h heldItem) FilterValue() string { return h.held.Name }

// showBrowse puts the directory the level is standing in into the list.
func (m *Model) showBrowse() {
	at, ok := m.path()
	if !ok {
		return
	}

	on := m.me.Name
	if with, there := m.peer(); there && !m.onSelf {
		on = with.Name
	}

	items := make([]list.Item, 0, len(m.held))
	for _, one := range m.held {
		items = append(items, heldItem{held: one, at: folder(at.Path) + deeper(m.dir, one.Name), on: on})
	}
	m.list.SetItems(items)
	m.list.Select(0)
	m.list.SetSize(m.listWidth(), m.listHeight())
}

// walkInto goes into the directory under the cursor.
func (m Model) walkInto() (tea.Model, tea.Cmd) {
	it, ok := m.list.SelectedItem().(heldItem)
	if !ok || !it.held.Dir {
		return m, nil
	}

	m.dir, m.held, m.loading, m.trouble = deeper(m.dir, it.held.Name), nil, true, ""
	m.showBrowse()
	return m, m.listing()
}

// walkOut comes up a directory, and out of the level from the root of the namespace.
func (m Model) walkOut() (tea.Model, tea.Cmd) {
	if m.dir != "" {
		m.dir, m.held, m.loading, m.trouble = above(m.dir), nil, true, ""
		m.showBrowse()
		return m, m.listing()
	}

	m.at, m.held, m.trouble, m.said = levelPaths, nil, "", ""
	m.showPaths()
	return m, nil
}

// browsing takes what the browse level does to one thing: a copy out, a copy in, or a removal.
func (m Model) browsing(key string) (tea.Model, tea.Cmd) {
	at, okPath := m.path()
	with, okPeer := m.peer()
	if !okPath || !okPeer || m.onSelf {
		return m, nil
	}

	if key == "s" {
		if !at.Writable {
			return m, nil
		}
		m.putting, m.typing, m.options, m.said, m.trouble = true, "", nil, "", ""
		return m, nil
	}

	it, ok := m.list.SelectedItem().(heldItem)
	if !ok || it.held.Dir {
		return m, nil
	}

	// Taking something off another machine is the one act here that cannot be undone by pressing
	// the key again, so it is asked about rather than done.
	if key == "x" {
		if !at.Writable {
			return m, nil
		}
		m.removing, m.trouble, m.said = it.held.Name, "", ""
		return m, nil
	}

	m.offering, m.trouble, m.said = &moving{}, "", ""
	return m, tea.Batch(fetch(m.back, with, at.Path, m.dir, it.held.Name, m.offering), ticking())
}

// answering takes the yes or no a removal is waiting for.
func (m Model) answering(key string) (tea.Model, tea.Cmd) {
	name := m.removing
	m.removing = ""

	if key != "y" {
		m.said = "left " + name + " where it is"
		return m, nil
	}

	at, okPath := m.path()
	with, okPeer := m.peer()
	if !okPath || !okPeer {
		return m, nil
	}
	return m, remove(m.back, with, at.Path, m.dir, name)
}

// deeper is the directory reached by walking into a name from where the level is standing.
func deeper(dir, name string) string {
	if dir == "" {
		return name
	}
	return folder(dir) + name
}

// above is the directory over this one, the empty one being the root of the namespace.
func above(dir string) string {
	if out := up(dir); out != "/" {
		return out
	}
	return ""
}
