package tui

import (
	"github.com/bresilla/drop/src/pkg/convo"
	"github.com/bresilla/drop/src/pkg/proto"
)

// What a namespace means to this interface.
//
// The archetype registry says what a namespace is; this says what the interface does with one, and
// it is the same shape on purpose. A kind of namespace is one value here, written down in one
// place, rather than a case in every switch that draws a row, opens a path or offers a key.
//
// Only what the interface itself decides lives here. What the far end said about a namespace — its
// archetype, its version, whether it is writable, whether it is locked, and the line about what it
// is for — travels on the wire and is read from there.

// shows is what an open namespace of an archetype is on screen.
type shows byte

const (
	// showsAbout has nothing of its own: what an interface built before an archetype existed draws
	// for one, and what one of this machine's own namespaces draws when it keeps nothing here.
	showsAbout shows = iota
	// showsTalk is a conversation.
	showsTalk
	// showsPut is what has gone to a namespace, and the line for sending the next thing.
	showsPut
	// showsLive is a screen the far end is painting.
	showsLive
	// showsWalk is a directory, walked at a level of its own.
	showsWalk
)

// A view is how this interface behaves for one archetype.
type view struct {
	// glyph is one character, for a list with no room for a word.
	glyph string
	// shows is what somebody else's namespace of this archetype draws, and mine what one of this
	// machine's own draws.
	shows shows
	mine  shows
	// sends is the word for what may be put into it, and is empty when nothing may be. onDisk says
	// that word names something on this machine, so what is typed can be completed against it.
	sends  string
	onDisk bool
	// kind is how something sent to this namespace is written down in the conversation, which is
	// also how the record of it is read back.
	kind byte
}

// views is which view answers to which archetype name. Adding a kind of namespace to this interface
// is adding a line here.
var views = map[string]view{
	"chat":  {glyph: "▤", shows: showsTalk},
	"share": {glyph: "▣", shows: showsPut, mine: showsWalk, sends: "a file", onDisk: true, kind: convo.KindFile},
	"files": {glyph: "▦", shows: showsWalk, mine: showsWalk, sends: "a file", onDisk: true, kind: convo.KindFile},
	"link":  {glyph: "◈", shows: showsPut, sends: "a link", kind: convo.KindLink},
	// A note is a file in somebody's own editor, so there is nothing here to press: the interface
	// says what it is, and joining it is what puts it on this disk.
	"note":   {glyph: "▩"},
	"tty":    {glyph: "▮", shows: showsLive},
	"stream": {glyph: "▶", shows: showsLive},
	// A branch is the absence of an archetype: a path holding others and serving nothing itself.
	"": {glyph: "▸"},
}

// viewOf is how the interface behaves for a namespace.
//
// An archetype it has never heard of falls back to the one the far end says that archetype speaks
// the protocol of, because a kind of namespace written in lua lives in a file the machine serving
// it has and this one may not. Failing that, honestly: the name, whatever the far end said it is
// for, and nothing to press.
func viewOf(at proto.Served) view {
	if of, ok := views[at.Archetype]; ok {
		return of
	}
	if of, ok := views[at.Shape]; ok {
		return of
	}
	return view{glyph: "·"}
}

// showing is what the open namespace draws. One of this machine's own is not a conversation with
// anybody, so it is asked about separately.
func (m Model) showing(at proto.Served) shows {
	of := viewOf(at)
	if m.onSelf {
		return of.mine
	}
	return of.shows
}
