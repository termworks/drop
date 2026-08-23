package conf

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/bresilla/drop/src/pkg/node"
)

// A config must serve something, so every case here declares one namespace it does not otherwise use.
const mounted = "drop.mount(\"/chat\", { type = \"chat\" })\n"

func configured(t *testing.T, body string) *Config {
	t.Helper()

	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	if err := os.MkdirAll(filepath.Join(dir, "drop"), 0o755); err != nil {
		t.Fatalf("making the config directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "drop", "init.lua"), []byte(body), 0o644); err != nil {
		t.Fatalf("writing the config: %v", err)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("loading: %v", err)
	}
	t.Cleanup(cfg.Close)
	return cfg
}

// Publishing writes to a relay the user does not own. A config that never mentions it must leave
// it off, because the alternative is a device quietly announcing itself because a default said so.
func TestRendezvousIsOffUnlessAsked(t *testing.T) {
	cfg := configured(t, mounted+`drop.name = "here"`)

	if cfg.HasRendezvous {
		t.Fatal("a config that never mentioned rendezvous was read as mentioning it")
	}

	node.SetRendezvous(false)
	cfg.Apply()
	if node.Rendezvous() {
		t.Fatal("rendezvous came on by itself")
	}
}

func TestRendezvousTurnsOnWhenAsked(t *testing.T) {
	cfg := configured(t, mounted+`drop.rendezvous = true`)

	if !cfg.HasRendezvous || !cfg.Rendezvous {
		t.Fatalf("read as %v/%v", cfg.HasRendezvous, cfg.Rendezvous)
	}

	node.SetRendezvous(false)
	cfg.Apply()
	if !node.Rendezvous() {
		t.Fatal("the setting did not reach the node")
	}
	t.Cleanup(func() { node.SetRendezvous(false) })
}

// Turning it off explicitly has to win, or a config cannot undo it once something else set it.
func TestRendezvousCanBeTurnedOff(t *testing.T) {
	cfg := configured(t, mounted+`drop.rendezvous = false`)

	node.SetRendezvous(true)
	cfg.Apply()
	if node.Rendezvous() {
		t.Fatal("an explicit false did not turn it off")
	}
	t.Cleanup(func() { node.SetRendezvous(false) })
}
