package tui

import (
	"strings"
	"testing"

	"github.com/bresilla/drop/src/pkg/book"
	"github.com/bresilla/drop/src/pkg/proto"
)

// An interface built before an archetype existed still meets one. What it says then is the name and
// whatever the far end said it is for, which is more than a blank pane says.
func TestAnArchetypeWithNoViewSaysWhatTheFarEndSaid(t *testing.T) {
	back := &fake{
		peers: []book.Entry{
			{Name: "beta", ID: idFor(2), Secret: make([]byte, book.SecretBytes)},
		},
		serves: map[string][]proto.Served{
			"beta": {{Path: "/lens", Archetype: "camera", Writable: true, About: "a camera, to look through"}},
		},
	}

	m := openPath(t, back, "/lens")

	shown := m.View()
	for _, want := range []string{"a camera path", "a camera, to look through"} {
		if !strings.Contains(shown, want) {
			t.Errorf("the screen is missing %q:\n%s", want, shown)
		}
	}

	// Nothing is offered for it, because nothing here knows what to offer.
	for _, gone := range []string{"i  write", "s  send", "type into it"} {
		if strings.Contains(shown, gone) {
			t.Errorf("it offered %q for an archetype it does not know:\n%s", gone, shown)
		}
	}
}

// The row for one is drawn too, with the fallback glyph and the far end's own words.
func TestAnArchetypeWithNoViewStillGetsARow(t *testing.T) {
	back := &fake{
		peers: []book.Entry{
			{Name: "beta", ID: idFor(2), Secret: make([]byte, book.SecretBytes)},
		},
		serves: map[string][]proto.Served{
			"beta": {{Path: "/lens", Archetype: "camera", About: "a camera, to look through"}},
		},
	}

	shown := intoPeer(t, start(t, back), 0).View()
	for _, want := range []string{"lens", "camera", "a camera, to look through"} {
		if !strings.Contains(shown, want) {
			t.Errorf("the row is missing %q:\n%s", want, shown)
		}
	}
}

// Every registered view has to say something about a namespace, or a list of them has holes in it.
func TestEveryViewHasAGlyph(t *testing.T) {
	for name, at := range views {
		if at.glyph == "" {
			t.Errorf("the view for %q has no glyph", name)
		}
		if at.shows == showsPut && at.sends == "" {
			t.Errorf("the view for %q draws a send screen but names nothing to send", name)
		}
	}
	if viewOf(proto.Served{Archetype: "nothing anybody registered"}).glyph == "" {
		t.Error("an archetype with no view has no glyph either")
	}
}
