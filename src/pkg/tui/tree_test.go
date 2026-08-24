package tui

import (
	"testing"

	"github.com/bresilla/drop/src/pkg/ns"
	"github.com/bresilla/drop/src/pkg/proto"
)

func served(paths ...string) []proto.Served {
	out := make([]proto.Served, 0, len(paths))
	for _, at := range paths {
		out = append(out, proto.Served{Path: at, Kind: ns.KindChat})
	}
	return out
}

// A flat list of every path a device serves is unreadable past a handful. What is wanted is what a
// filesystem gives: this level, and a way into the next.
func TestOnlyWhatIsAtThisLevelIsListed(t *testing.T) {
	paths := served("/chat", "/work/reports", "/work/notes", "/media/music/rock")

	at := walk(paths, "/")
	if len(at) != 3 {
		t.Fatalf("the top level lists %d things, want chat, work and media: %+v", len(at), at)
	}

	// Ways down come first, and know how much is under them.
	if at[0].name != "media" || at[0].below == 0 {
		t.Errorf("first is %+v, want media as a way down", at[0])
	}
	if at[1].name != "work" || at[1].below != 2 {
		t.Errorf("second is %+v, want work with two below", at[1])
	}
	if at[2].name != "chat" || !at[2].is {
		t.Errorf("third is %+v, want chat as a namespace", at[2])
	}
}

// Going into one shows what is under it, and nothing else.
func TestGoingIntoAFolderShowsWhatIsUnderIt(t *testing.T) {
	paths := served("/chat", "/work/reports", "/work/notes", "/media/music/rock")

	at := walk(paths, "/work")
	if len(at) != 2 {
		t.Fatalf("under /work: %+v", at)
	}
	for _, one := range at {
		if !one.is || one.below != 0 {
			t.Errorf("%+v should be a namespace and nothing below it", one)
		}
		if one.at != "/work/"+one.name {
			t.Errorf("%+v has the wrong whole path", one)
		}
	}
}

// A path can be a namespace and a way down at the same time: /stream serves what is under it, and
// /stream/logs is declared as well.
func TestAPathCanBeBothAPlaceAndAWayDown(t *testing.T) {
	at := walk(served("/stream", "/stream/logs"), "/")

	if len(at) != 1 {
		t.Fatalf("the top level lists %+v", at)
	}
	if !at[0].is || at[0].below != 1 {
		t.Errorf("%+v should be a namespace with one below it", at[0])
	}
}

// Coming back out again.
func TestOneLevelUp(t *testing.T) {
	for path, want := range map[string]string{
		"/media/music/rock": "/media/music",
		"/media/music":      "/media",
		"/media":            "/",
		"/":                 "/",
		"":                  "/",
	} {
		if got := up(path); got != want {
			t.Errorf("up(%q) = %q, want %q", path, got, want)
		}
	}
}
