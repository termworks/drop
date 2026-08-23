// Package conf reads drop's Lua configuration.
package conf

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/bresilla/drop/src/pkg/node"
	"github.com/bresilla/drop/src/pkg/ns"
)

// Config is what a node was told to be.
type Config struct {
	// Name is what this node calls itself; empty leaves the hostname.
	Name string
	// Bootstrap and Relays override the defaults when set.
	Bootstrap []string
	Relays    []string
	// HasName and HasOpenLinks say whether the config mentioned the setting at all, so one it never
	// named leaves the environment alone.
	HasName      bool
	HasOpenLinks bool
	// OpenLinks lets a link namespace act rather than only record.
	OpenLinks bool
	// Mounts is every namespace this node serves.
	Mounts *ns.Table
	// rt holds the Lua state, kept alive for the handlers a config registered.
	rt *runtime
	// Path is the file this came from, empty when nothing was found.
	Path string
}

// Default is what a node serves with no configuration file: somewhere to put files, somewhere to
// talk, and nothing that runs a command or shares a terminal, because those are decisions.
func Default() *Config {
	table := ns.NewTable()
	_ = table.Add(ns.Mount{Path: "/inbox", Kind: ns.KindFiles, Dir: "."})
	_ = table.Add(ns.Mount{Path: "/chat", Kind: ns.KindChat})
	_ = table.Add(ns.Mount{Path: "/open", Kind: ns.KindLink})

	return &Config{Mounts: table}
}

// FilePath is where the configuration lives: $DROP_CONFIG, else $XDG_CONFIG_HOME/drop/init.lua.
func FilePath() (string, error) {
	if custom := os.Getenv("DROP_CONFIG"); custom != "" {
		return custom, nil
	}
	dir, err := node.ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "init.lua"), nil
}

// Load reads the configuration, falling back to the defaults when there is no file.
//
// A file that exists and does not parse is fatal rather than ignored: a typo that silently drops
// half the namespaces is worse than not starting.
func Load() (*Config, error) {
	path, err := FilePath()
	if err != nil {
		return nil, err
	}

	_, err = os.Stat(path)
	if os.IsNotExist(err) {
		return Default(), nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}

	cfg := &Config{Mounts: ns.NewTable(), Path: path}
	if err := run(cfg, path); err != nil {
		return nil, err
	}
	if cfg.Mounts.Len() == 0 {
		return nil, fmt.Errorf("%s declares no namespaces, so this node would serve nothing", path)
	}
	return cfg, nil
}

// expand resolves ~ in a configured path, because a config is written by a person.
func expand(path string) string {
	if !strings.HasPrefix(path, "~") {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	if path == "~" {
		return home
	}
	return filepath.Join(home, strings.TrimPrefix(path, "~/"))
}

// Apply hands the settings the config named to the parts of drop that act on them.
//
// Only what the config actually mentioned is applied. A setting it never named is left alone, so
// the environment or a flag still decides it.
func (c *Config) Apply() {
	if c.HasName && c.Name != "" {
		node.SetName(c.Name)
	}
	if len(c.Bootstrap) > 0 {
		node.SetBootstrap(c.Bootstrap)
	}
	if len(c.Relays) > 0 {
		node.SetRelays(c.Relays)
	}
}
