package arch_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/bresilla/drop/src/pkg/arch"
	"github.com/bresilla/drop/src/pkg/conf"
	"github.com/bresilla/drop/src/pkg/made"
	"github.com/bresilla/drop/src/pkg/ns"
)

// The same proof from the other end.
//
// The camera is still written in one file and nowhere else. This time it is not declared in a
// config: it is written down in paths.json by hand, the way `drop path create --keep` writes it,
// and it comes back out of the merge holding a cameraConfig — with made, conf, ns and the command
// line containing not one line that knows the word camera.
func TestAnArchetypeWrittenOutsideDropIsCreatedAndServed(t *testing.T) {
	known := arch.NewRegistry()
	known.Register(camera{})

	// Two files under one config directory: the one somebody wrote by hand, and the one a command
	// wrote for them.
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	if err := os.MkdirAll(filepath.Join(dir, "drop"), 0o700); err != nil {
		t.Fatal(err)
	}

	written := `{
		"/film": {
			"type": "camera", "version": 3,
			"settings": { "device": "/dev/video0", "colour": true },
			"access": { "named": ["bob", "carol@laptop"] }
		}
	}`
	if err := os.WriteFile(filepath.Join(dir, "drop", made.File), []byte(written), 0o600); err != nil {
		t.Fatalf("writing the created namespaces: %v", err)
	}

	config := filepath.Join(dir, "init.lua")
	if err := os.WriteFile(config, []byte(`require("drop").mount("/chat", { type = "camera", device = "/dev/video1", access = "paired" })`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DROP_CONFIG", config)

	cfg, err := conf.Load(known)
	if err != nil {
		t.Fatalf("Load(): %v", err)
	}
	defer cfg.Close()

	store, err := made.Load()
	if err != nil {
		t.Fatalf("made.Load(): %v", err)
	}
	skipped, err := cfg.Created(store)
	if err != nil {
		t.Fatalf("Created(): %v", err)
	}
	if len(skipped) != 0 {
		t.Fatalf("skipped %v", skipped)
	}

	mount, _, ok := cfg.Mounts.Lookup("/film")
	if !ok || mount.Archetype != "camera" || mount.Version != 3 {
		t.Fatalf("/film = %+v ok %v", mount, ok)
	}
	if got, ok := mount.Config.(cameraConfig); !ok || got.Device != "/dev/video0" || !got.Colour {
		t.Fatalf("the camera's settings came back as %#v", mount.Config)
	}
	if mount.Source != ns.Written {
		t.Errorf("/film came from %s", mount.Source)
	}

	// And the rule it was written down with, in the words the config uses for the same thing.
	if got := mount.Access.Named; len(got) != 2 || got[0] != "bob" || got[1] != "carol@laptop" {
		t.Errorf("/film admits %v", got)
	}
	if ok, why := mount.Access.Admits(ns.Caller{ID: "aaaa", Paired: true, UserName: "bob"}); !ok {
		t.Errorf("bob cannot reach it: %s", why)
	}
}
