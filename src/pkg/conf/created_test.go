package conf

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bresilla/drop/src/pkg/made"
	"github.com/bresilla/drop/src/pkg/ns"
)

// created writes a paths.json by hand and loads it, the way a person who edited the file would.
func created(t *testing.T, body string) *made.Store {
	t.Helper()

	dir := filepath.Join(t.TempDir(), "config")
	t.Setenv("XDG_CONFIG_HOME", dir)

	file := filepath.Join(dir, "drop", made.File)
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	store, err := made.Load()
	if err != nil {
		t.Fatalf("made.Load(): %v", err)
	}
	return store
}

func TestACreatedNamespaceIsServedAndSaysWhereItCameFrom(t *testing.T) {
	store := created(t, `{"/notes": {"type": "chat", "access": {"paired": true}}}`)
	cfg := load(t, `
		local drop = require("drop")
		drop.mount("/work", { type = "chat", access = "paired" })
	`)

	skipped, err := cfg.Created(store)
	if err != nil {
		t.Fatalf("Created(): %v", err)
	}
	if len(skipped) != 0 {
		t.Fatalf("skipped %v", skipped)
	}

	mount, _, ok := cfg.Mounts.Lookup("/notes")
	if !ok || mount.Archetype != "chat" {
		t.Fatalf("/notes = %+v ok %v", mount, ok)
	}
	if mount.Source != ns.Written {
		t.Errorf("/notes came from %s", mount.Source)
	}
	if !mount.Access.AnyPaired {
		t.Errorf("/notes admits %+v", mount.Access)
	}
}

// The config is what somebody wrote by hand, so it wins. Explicitly, not by being added first: the
// table replaces whatever was at a path, and a merge that leant on ordering would swap the winner
// the next time somebody moved a call.
func TestTheConfigWinsACollision(t *testing.T) {
	store := created(t, `{"/notes": {"type": "chat", "access": {"anyone": true}}}`)
	cfg := load(t, `
		local drop = require("drop")
		drop.mount("/notes", { type = "files", dir = "/tmp", access = "paired" })
	`)

	skipped, err := cfg.Created(store)
	if err != nil {
		t.Fatalf("Created(): %v", err)
	}
	if len(skipped) != 1 || skipped[0].Path != "/notes" {
		t.Fatalf("skipped %v", skipped)
	}
	if said := skipped[0].String(); !strings.Contains(said, "the config declares it") {
		t.Errorf("it says %q", said)
	}

	mount, _, _ := cfg.Mounts.Lookup("/notes")
	if mount.Archetype != "files" || mount.Source != ns.Configured {
		t.Fatalf("/notes came out %+v", mount)
	}
	if mount.Access.Anyone {
		t.Error("the created rule replaced the one in the config")
	}
}

// A config that names a type this build does not have is refused where it is written. This file is
// data, so it follows the grants instead: fail closed on the one entry, and go on with the rest.
func TestAnUnknownTypeIsSkippedRatherThanFatal(t *testing.T) {
	store := created(t, `{
		"/film": {"type": "camera", "settings": {"device": "/dev/video0"}, "access": {"paired": true}},
		"/notes": {"type": "chat", "access": {"paired": true}}
	}`)
	cfg := load(t, `
		local drop = require("drop")
		drop.mount("/work", { type = "chat", access = "paired" })
	`)

	skipped, err := cfg.Created(store)
	if err != nil {
		t.Fatalf("Created(): %v", err)
	}
	if len(skipped) != 1 || skipped[0].Path != "/film" {
		t.Fatalf("skipped %v", skipped)
	}
	if said := skipped[0].String(); !strings.Contains(said, "camera") || !strings.Contains(said, made.File) {
		t.Errorf("it says %q", said)
	}
	if _, _, ok := cfg.Mounts.Lookup("/notes"); !ok {
		t.Error("the rest of the file was dropped with it")
	}
}

// A declaration the archetype refuses is one entry's problem, not the node's.
func TestADeclarationTheArchetypeRefusesIsSkipped(t *testing.T) {
	store := created(t, `{"/nowhere": {"type": "files", "access": {"paired": true}}}`)
	cfg := load(t, `
		local drop = require("drop")
		drop.mount("/work", { type = "chat", access = "paired" })
	`)

	skipped, err := cfg.Created(store)
	if err != nil {
		t.Fatalf("Created(): %v", err)
	}
	if len(skipped) != 1 || skipped[0].Path != "/nowhere" {
		t.Fatalf("skipped %v", skipped)
	}
}
