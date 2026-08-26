package cmd

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/bresilla/drop/src/pkg/made"
	"github.com/bresilla/drop/src/pkg/ns"
)

// declaring is a config file and a config directory of this test's own.
func declaring(t *testing.T, body string) string {
	t.Helper()

	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "config"))

	config := filepath.Join(dir, "init.lua")
	if err := os.WriteFile(config, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DROP_CONFIG", config)
	return config
}

// A path in the config carries a rule somebody wrote by hand over something of theirs. A command
// must not stand in for it, and must say where to go instead -- the same answer a handoff gives.
func TestCreatingOverADeclaredPathIsRefusedAndNamesTheConfig(t *testing.T) {
	config := declaring(t, `require("drop").mount("/work", { type = "chat", access = "paired" })`)

	entry := made.Entry{Archetype: "chat", Access: made.Access{Paired: true}}
	err := runCreate(context.Background(), reading(), "/work", entry, false)
	if err == nil {
		t.Fatal("it was created over the config")
	}
	if !strings.Contains(err.Error(), config) {
		t.Errorf("the refusal does not name the config: %v", err)
	}
}

func TestRemovingADeclaredPathIsRefusedAndNamesTheConfig(t *testing.T) {
	config := declaring(t, `require("drop").mount("/work", { type = "chat", access = "paired" })`)

	err := runRemove("/work")
	if err == nil {
		t.Fatal("a path in the config was removed")
	}
	if !strings.Contains(err.Error(), config) {
		t.Errorf("the refusal does not name the config: %v", err)
	}
}

// A namespace put up without saying who may reach it is one anybody paired could open, and a
// setting may name a command to run.
func TestCreatingWithNoAccessIsRefused(t *testing.T) {
	if _, err := admitting("", ""); err == nil {
		t.Fatal("a namespace was created open to whoever the default is")
	}
}

func TestTheThreeSetFlagsSayThreeDifferentThings(t *testing.T) {
	got, err := settings([]string{"dir=~/notes", "word=true"}, []string{"writable", "hidden=false"}, []string{"only=a, b"})
	if err != nil {
		t.Fatalf("reading the flags: %v", err)
	}

	want := made.Settings{
		"dir":      "~/notes",
		"word":     "true",
		"writable": true,
		"hidden":   false,
		"only":     []string{"a", "b"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("came out %#v", got)
	}
}

func TestAFlagThatIsNeitherOnNorOffIsRefused(t *testing.T) {
	if _, err := settings(nil, []string{"writable=maybe"}, nil); err == nil {
		t.Fatal("a flag was set to something that is not on or off")
	}
}

func TestAnAccessRuleReadsTheWordsAConfigUses(t *testing.T) {
	for rule, want := range map[string]made.Access{
		"paired":         {Paired: true},
		"trusted":        {Trusted: true},
		"anyone":         {Anyone: true},
		"bob,carol@work": {Named: []string{"bob", "carol@work"}},
	} {
		got, err := admitting(rule, "")
		if err != nil {
			t.Fatalf("--access %s: %v", rule, err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("--access %s came out %#v", rule, got)
		}
	}
}

// A namespace taken off the list has to stop being served, not merely stop being written down.
// Somebody who removes a path is trying to stop sharing something, and a node that goes on
// answering for it until a restart is the one failure that matters here.
func TestWhatIsRemovedStopsBeingServed(t *testing.T) {
	mounts := ns.NewTable()
	host := newMountHost(mounts, reading())

	up := made.Line{Path: "/notes", Entry: made.Entry{
		Archetype: "files",
		Settings:  map[string]any{"dir": t.TempDir()},
		Access:    made.Access{Paired: true},
	}, Keep: true}

	if err := host.begin(up); err != nil {
		t.Fatalf("putting /notes up: %v", err)
	}
	if _, _, ok := mounts.Lookup("/notes"); !ok {
		t.Fatal("/notes was not put up at all")
	}
	if !host.mine("/notes") {
		t.Fatal("the node does not know it put /notes up")
	}

	host.end("/notes")

	if _, _, ok := mounts.Lookup("/notes"); ok {
		t.Fatal("/notes is still served after being taken off the list")
	}
	if host.mine("/notes") {
		t.Error("the node still counts /notes as one of its own")
	}
	// Ending it twice is what a second `drop path rm` does, and it must not take down whatever
	// happens to be at that path by then.
	host.end("/notes")
}
