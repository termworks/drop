package cmd

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/bresilla/drop/src/pkg/ns"
	"github.com/bresilla/drop/src/pkg/proto"
)

// offers is a far end that says it serves these, without a wire between here and it.
func offers(serves ...proto.Served) []proto.Served { return serves }

// typed is an address the way somebody writes one.
func typed(t *testing.T, text string) ns.Address {
	t.Helper()

	got, err := ns.ParseAddress(text)
	if err != nil {
		t.Fatalf("ParseAddress(%q): %v", text, err)
	}
	return got
}

// Each kind of namespace gets the opener registered for it, and none of them gets another's.
func TestConnectOpensEachArchetypeItsOwnWay(t *testing.T) {
	far := offers(
		proto.Served{Path: "/chat", Archetype: "chat"},
		proto.Served{Path: "/term", Archetype: "tty"},
		proto.Served{Path: "/logs", Archetype: "stream"},
		proto.Served{Path: "/work", Archetype: "files"},
		proto.Served{Path: "/open", Archetype: "link"},
		proto.Served{Path: "/inbox", Archetype: "share"},
	)

	cases := []struct {
		text string
		args []string
		want func(ctx context.Context, o opening) error
	}{
		{text: "orin:/chat", want: openChat},
		{text: "orin:/term", want: openTerminal},
		{text: "orin:/logs", want: openStream},
		{text: "orin:/work", want: openFiles},
		{text: "orin:/open", args: []string{"https://example.invalid"}, want: openLink},
		{text: "orin:/inbox", args: []string{"report.pdf"}, want: openShare},
	}

	for _, c := range cases {
		_, _, how, err := choose(far, typed(t, c.text), c.args)
		if err != nil {
			t.Errorf("choose(%q): %v", c.text, err)
			continue
		}
		if reflect.ValueOf(how.open).Pointer() != reflect.ValueOf(c.want).Pointer() {
			t.Errorf("%q was opened by the wrong opener", c.text)
		}
	}
}

// A path inside a namespace opens the namespace, and says what was left below it.
func TestConnectOpensTheNamespaceAPathLandsIn(t *testing.T) {
	far := offers(
		proto.Served{Path: "/work", Archetype: "files"},
		proto.Served{Path: "/work/deep", Archetype: "files"},
	)

	served, rest, _, err := choose(far, typed(t, "orin:/work/deep/further"), nil)
	if err != nil {
		t.Fatalf("choose(): %v", err)
	}
	if served.Path != "/work/deep" {
		t.Errorf("landed on %q rather than the deepest namespace covering the path", served.Path)
	}
	if rest != "further" {
		t.Errorf("what was left below it is %q", rest)
	}
}

// An archetype this build has never heard of is not a crash: it is named, and so is the reason —
// which is that the file it is written in is on the other machine and not on this one.
func TestConnectRefusesAnArchetypeItCannotOpen(t *testing.T) {
	far := offers(proto.Served{Path: "/eye", Archetype: "camera"})

	_, _, _, err := choose(far, typed(t, "orin:/eye"), nil)
	if err == nil {
		t.Fatal("an archetype with no opener was opened anyway")
	}
	for _, want := range []string{"camera", "no such thing", "lua"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("%q is not in %q", want, err)
		}
	}
}

// One that says what protocol it speaks is opened by that, because that is the whole of what a
// caller ever needed to know about it.
func TestConnectOpensAnUnknownArchetypeByItsShape(t *testing.T) {
	far := offers(proto.Served{Path: "/eye", Archetype: "camera", Shape: "note"})

	served, _, how, err := choose(far, typed(t, "orin:/eye"), nil)
	if err != nil {
		t.Fatalf("choose(): %v", err)
	}
	if served.Archetype != "camera" {
		t.Errorf("it landed on a %q", served.Archetype)
	}
	if how.does != openers["note"].does {
		t.Errorf("it was opened with something other than the note opener")
	}
}

// The other three refusals, each saying what is there instead of what was wanted.
func TestConnectSaysWhyItWillNotOpenSomething(t *testing.T) {
	cases := []struct {
		what    string
		serving []proto.Served
		text    string
		args    []string
		says    string
	}{
		{
			what:    "a machine with no path on it",
			serving: offers(proto.Served{Path: "/chat", Archetype: "chat"}),
			text:    "orin",
			says:    "drop path ls",
		},
		{
			what:    "a path that is visible but locked",
			serving: offers(proto.Served{Path: "/vault", Archetype: "files", Locked: true}),
			text:    "orin:/vault",
			says:    "drop path ask",
		},
		{
			what:    "a branch, which is no namespace at all",
			serving: offers(proto.Served{Path: "/work"}),
			text:    "orin:/work",
			says:    "drop path ls",
		},
		{
			what:    "words given to something that takes none",
			serving: offers(proto.Served{Path: "/logs", Archetype: "stream"}),
			text:    "orin:/logs",
			args:    []string{"say", "something"},
			says:    "takes nothing",
		},
	}

	for _, c := range cases {
		_, _, _, err := choose(c.serving, typed(t, c.text), c.args)
		if err == nil {
			t.Errorf("%s was opened anyway", c.what)
			continue
		}
		if !strings.Contains(err.Error(), c.says) {
			t.Errorf("%s: %q does not say %q", c.what, err, c.says)
		}
	}
}

// A far end that has not said what is there — it is off, or it shows this caller nothing — is not
// a reason to stop. What was typed says what this is, and the far end still gets to refuse.
func TestConnectFallsBackToWhatWasTyped(t *testing.T) {
	said := offers()

	cases := []struct {
		args []string
		want func(ctx context.Context, o opening) error
	}{
		{args: nil, want: openTerminal},
		{args: []string{"https://example.invalid"}, want: openLink},
		{args: []string{"on", "my", "way"}, want: openChat},
		{args: []string{"-"}, want: openShare},
	}

	for _, c := range cases {
		served, _, how, err := choose(said, typed(t, "orin:/whatever"), c.args)
		if err != nil {
			t.Errorf("choose(%v): %v", c.args, err)
			continue
		}
		if served.Archetype != "" {
			t.Errorf("choose(%v) named %q, which the far end never said", c.args, served.Archetype)
		}
		if reflect.ValueOf(how.open).Pointer() != reflect.ValueOf(c.want).Pointer() {
			t.Errorf("choose(%v) picked the wrong opener", c.args)
		}
	}
}

// The help is the registry read back, so a kind of namespace this build opens is one it mentions.
func TestConnectHelpNamesEveryArchetypeItOpens(t *testing.T) {
	long := connectLong()

	for name := range openers {
		if !strings.Contains(long, name) {
			t.Errorf("connect's help does not mention %s", name)
		}
	}
}
