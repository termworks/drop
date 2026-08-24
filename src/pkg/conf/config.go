// Package conf reads drop's Lua configuration.
package conf

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/bresilla/drop/src/pkg/grant"
	"github.com/bresilla/drop/src/pkg/node"
	"github.com/bresilla/drop/src/pkg/ns"
	"github.com/bresilla/drop/src/pkg/user"
	"github.com/bresilla/drop/src/pkg/vault"
)

// Config is what a node was told to be.
type Config struct {
	// Name is what this node calls itself; empty leaves the hostname.
	Name string
	// UserKey is the key this person signs badges with: an SSH private key, or the public half of
	// one an agent holds. Empty leaves drop keeping a key of its own.
	UserKey string
	// UserSign is the command that signs badges, for a key drop cannot read: it takes the thing to
	// sign on standard input and writes the signature on standard output. Empty lets drop work it
	// out, which means ssh-keygen for a key held in hardware or an agent.
	UserSign string
	// Vault is who the data key is wrapped to: age recipients, or a path to a key file. Empty is a
	// node that keeps its history in the clear, which is the default and a decision.
	Vault []string
	// Bootstrap and Relays override the defaults when set.
	Bootstrap []string
	Relays    []string
	// HasName and HasOpenLinks say whether the config mentioned the setting at all, so one it never
	// named leaves the environment alone.
	HasName bool
	// Rendezvous turns on publishing this device's address for paired peers to find.
	Rendezvous    bool
	HasRendezvous bool
	HasOpenLinks  bool
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
// Default is what a node serves with no config at all.
//
// Open to anyone paired, because a default that serves nobody is not a default — it is a node
// that appears broken until its owner finds out a rule was needed. Pairing is already the
// deliberate act: nothing reaches these without one.
func Default() *Config {
	open := ns.Access{AnyPaired: true}

	table := ns.NewTable()
	_ = table.Add(ns.Mount{Path: "/inbox", Kind: ns.KindFiles, Dir: ".", Access: open})
	_ = table.Add(ns.Mount{Path: "/chat", Kind: ns.KindChat, Access: open})
	_ = table.Add(ns.Mount{Path: "/open", Kind: ns.KindLink, Access: open})

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
	if c.HasRendezvous {
		node.SetRendezvous(c.Rendezvous)
	}
	if len(c.Relays) > 0 {
		node.SetRelays(c.Relays)
	}
	if c.UserKey != "" {
		user.Use(expand(c.UserKey))
	}
	if c.UserSign != "" {
		user.SignWith(c.UserSign)
	}
}

// ApplySettings puts the config's settings in effect and nothing else.
//
// Every command needs the settings — a command that dials has to know whether a rendezvous is
// allowed — but only the ones that serve need the namespaces and handlers. An unreadable config is
// ignored here rather than reported, because the command that actually depends on it loads it
// again and says so properly.
func ApplySettings() {
	cfg, err := Load()
	if err != nil {
		return
	}
	defer cfg.Close()

	cfg.Apply()
}

// Vaulted opens the vault this config names, making a data key the first time.
//
// A locked device comes back as an error rather than as a node that quietly serves nothing: the
// history is there and unreadable, and a peer asking for it has to be told that rather than handed
// an empty answer that reads as the path being gone.
func (c *Config) Vaulted() (*vault.Vault, error) {
	return vault.Open(c.Vault)
}

// Grants attaches what the interface has allowed and refused to the namespaces this config
// declares, and hands the store back so the interface can write to the same one.
//
// It is done here because every command that serves anything loads a config, and a node that
// forgot to do it would honour a revocation everywhere except the one place it matters.
func (c *Config) Grants() (*grant.Store, error) {
	store, err := grant.Load()
	if err != nil {
		return nil, err
	}

	c.Mounts.Granted(store)
	return store, nil
}
