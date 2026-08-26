package cmd

import (
	"fmt"

	"github.com/bresilla/drop/src/pkg/arch"
	"github.com/bresilla/drop/src/pkg/arch/chat"
	"github.com/bresilla/drop/src/pkg/arch/files"
	"github.com/bresilla/drop/src/pkg/arch/link"
	"github.com/bresilla/drop/src/pkg/arch/share"
	"github.com/bresilla/drop/src/pkg/arch/stream"
	"github.com/bresilla/drop/src/pkg/arch/tty"
	"github.com/bresilla/drop/src/pkg/book"
	"github.com/bresilla/drop/src/pkg/cast"
	"github.com/bresilla/drop/src/pkg/conf"
	"github.com/bresilla/drop/src/pkg/convo"
	"github.com/bresilla/drop/src/pkg/node"
)

// What a process can be asked for.
//
// Which archetypes a process registers is a property of that process: the daemon answers everything
// a config can name, `drop chat` answers one, and a cast answers the terminal it is showing. So a
// registry is built where it is needed and handed in, rather than kept as a package variable.

// doings is what the archetypes a process registers report back to.
type doings struct {
	pinned *book.Book
	// cfg is the config once it has been read. Archetypes are registered before that, because
	// reading a config is what needs them.
	cfg *conf.Config
	// notes, when set, prints one line about something that happened.
	notes func(text string)
	// bar, when set, prints transfers as they go.
	bar *progress
	// said, when set, is told about a message that was stored.
	said func(from node.ID, m convo.Message)
	// noticed, when set, is nudged whenever anything lands, for an interface that redraws.
	noticed func()
	// took, when set, is told that something arrived in a share namespace, for a dropbox that is
	// up for one transfer.
	took func()
	// shown, when set, answers whether a path is a screen this process is already running rather
	// than a shell to start.
	shown func(path string) (*cast.Caster, bool)
	// shells is the terminals this process is running, held so they can all be ended at once.
	shells *tty.TTY
}

// reading registers every archetype for its settings alone.
//
// A command that only wants to read a config still needs each mount understood by the thing that
// declared it, and nothing at all of what happens when one is opened.
func reading() *arch.Registry {
	return (&doings{}).serving()
}

// serving registers everything a config can name.
func (d *doings) serving() *arch.Registry {
	known := arch.NewRegistry()
	known.Register(share.New(share.Into{Progress: d.moving, Landed: d.dropped}))
	known.Register(files.New(files.Into{Progress: d.moving, Landed: d.landed}))
	known.Register(chat.New(chat.Into{Store: d.store}))
	known.Register(link.New(link.Into{Store: d.store}))
	known.Register(stream.New(stream.Into{Opened: d.opened}))
	known.Register(d.terminals())
	return known
}

// talking registers the one archetype a chat answers while it is open.
func (d *doings) talking() *arch.Registry {
	known := arch.NewRegistry()
	known.Register(chat.New(chat.Into{Store: d.store}))
	return known
}

// watching registers the terminal a cast is shown at, and nothing else.
func (d *doings) watching() *arch.Registry {
	known := arch.NewRegistry()
	known.Register(d.terminals())
	return known
}

// terminals is this process's shells, made once so that two watchers of a path meet on one.
func (d *doings) terminals() *tty.TTY {
	if d.shells == nil {
		d.shells = tty.New(tty.Into{Watched: d.watched, Showing: d.showing})
	}
	return d.shells
}

// stop ends whatever the archetypes are holding open.
func (d *doings) stop() {
	if d.shells != nil {
		d.shells.Stop()
	}
}

func (d *doings) showing(path string) (*cast.Caster, bool) {
	if d.shown == nil {
		return nil, false
	}
	return d.shown(path)
}

func (d *doings) moving(name string, done, total int64) {
	if d.bar != nil {
		d.bar.update(name, done, total)
	}
}

// landed records a file that arrived, wherever it arrived.
func (d *doings) landed(from node.ID, name string, size int64) {
	d.note(fmt.Sprintf("received %s (%s)", name, bytes(size)))
	noteFile(from, convo.In, name, size)
	if d.cfg != nil {
		d.cfg.FireFile(conf.File{From: nameFor(d.pinned, from), Name: name, Size: size})
	}
	d.knock()
}

// dropped records a file that arrived in a share namespace, which is the one kind of arrival a
// dropbox is put up for.
func (d *doings) dropped(from node.ID, name string, size int64) {
	d.landed(from, name, size)
	if d.took != nil {
		d.took()
	}
}

// store puts an arriving message away, and acts on the kinds that ask for it.
func (d *doings) store(from node.ID, m convo.Message) error {
	openLinks := d.cfg != nil && d.cfg.OpenLinks

	return receiving(d.pinned, openLinks, func(from node.ID, m convo.Message) {
		if d.said != nil {
			d.said(from, m)
		}
		d.knock()
	})(from, m)
}

func (d *doings) watched(path string, from node.ID, watching int) {
	d.note(fmt.Sprintf("%s is watching %s (%d total)", nameFor(d.pinned, from), path, watching))
}

func (d *doings) opened(path string, from node.ID) {
	d.note(fmt.Sprintf("%s opened %s", nameFor(d.pinned, from), path))
}

func (d *doings) note(text string) {
	if d.notes != nil {
		d.notes(text)
	}
}

func (d *doings) knock() {
	if d.noticed != nil {
		d.noticed()
	}
}
