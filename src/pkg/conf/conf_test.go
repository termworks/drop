package conf

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	rt "github.com/arnodel/golua/runtime"

	"github.com/bresilla/drop/src/pkg/ns"
)

func write(t *testing.T, body string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "init.lua")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("writing the config: %v", err)
	}
	t.Setenv("DROP_CONFIG", path)
	return path
}

func load(t *testing.T, body string) *Config {
	t.Helper()

	write(t, body)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load(): %v", err)
	}
	t.Cleanup(cfg.Close)
	return cfg
}

func TestSettingsAreAssigned(t *testing.T) {
	cfg := load(t, `
		local drop = require("drop")
		drop.name = "workstation"
		drop.open_links = true
		drop.relays = { "/ip4/1.2.3.4/tcp/1/p2p/x", "/ip4/5.6.7.8/tcp/2/p2p/y" }
		drop.mount("/chat", { type = "chat" })
	`)

	if cfg.Name != "workstation" {
		t.Errorf("Name = %q", cfg.Name)
	}
	if !cfg.OpenLinks {
		t.Error("OpenLinks is false")
	}
	if len(cfg.Relays) != 2 {
		t.Errorf("Relays = %v", cfg.Relays)
	}
}

func TestMountsAreRegistered(t *testing.T) {
	cfg := load(t, `
		local drop = require("drop")
		drop.mount("/inbox", { type = "files", dir = "/tmp/in" })
		drop.mount("/logs",  { type = "stream", command = "tail -f /var/log/x" })
		drop.mount("/term",  { type = "tty", input = true })
	`)

	m, _, ok := cfg.Mounts.Lookup("/inbox")
	if !ok || m.Kind != ns.KindFiles || m.Dir != "/tmp/in" {
		t.Fatalf("/inbox = %+v ok %v", m, ok)
	}
	m, _, _ = cfg.Mounts.Lookup("/term")
	if !m.Input {
		t.Error("/term did not keep input = true")
	}
}

// The file is a program, so it can decide what this machine offers.
func TestConfigCanBranch(t *testing.T) {
	t.Setenv("DROP_TEST_DEV", "1")

	cfg := load(t, `
		local drop = require("drop")
		drop.mount("/chat", { type = "chat" })
		if os.getenv("DROP_TEST_DEV") then
			drop.mount("/build", { type = "stream", command = "tail -f /tmp/b.log" })
		end
		for i = 1, 3 do
			drop.mount("/stream/" .. i, { type = "stream", command = "echo " .. i })
		end
	`)

	if _, _, ok := cfg.Mounts.Lookup("/build"); !ok {
		t.Error("the conditional mount is missing")
	}
	for _, path := range []string{"/stream/1", "/stream/2", "/stream/3"} {
		if _, _, ok := cfg.Mounts.Lookup(path); !ok {
			t.Errorf("%s is missing", path)
		}
	}
}

// A config that does not parse must not start with half a table.
func TestBrokenConfigIsFatalAndNamesTheFile(t *testing.T) {
	path := write(t, "this is not lua at all ((((")

	_, err := Load()
	if err == nil {
		t.Fatal("Load() accepted a config that does not parse")
	}
	if !strings.Contains(err.Error(), filepath.Base(path)) {
		t.Errorf("the error does not name the file: %v", err)
	}
}

func TestMountWithoutATypeIsRefused(t *testing.T) {
	write(t, `
		local drop = require("drop")
		drop.mount("/nowhere", { dir = "/tmp" })
	`)

	if _, err := Load(); err == nil {
		t.Fatal("Load() accepted a mount with no type")
	}
}

func TestFilesMountWithoutADirIsRefused(t *testing.T) {
	write(t, `
		local drop = require("drop")
		drop.mount("/inbox", { type = "files" })
	`)

	if _, err := Load(); err == nil {
		t.Fatal("Load() accepted a files namespace with no dir")
	}
}

func TestConfigServingNothingIsRefused(t *testing.T) {
	write(t, `local drop = require("drop")`)

	if _, err := Load(); err == nil {
		t.Fatal("Load() accepted a config that declares no namespaces")
	}
}

func TestDefaultsWhenThereIsNoFile(t *testing.T) {
	t.Setenv("DROP_CONFIG", filepath.Join(t.TempDir(), "absent.lua"))

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load(): %v", err)
	}
	if cfg.Path != "" {
		t.Errorf("Path = %q, want empty for the defaults", cfg.Path)
	}
	if _, _, ok := cfg.Mounts.Lookup("/inbox"); !ok {
		t.Error("the defaults do not serve /inbox")
	}
	// The defaults must not run a command or share a terminal; those are decisions.
	for _, m := range cfg.Mounts.All() {
		if m.Kind == ns.KindStream || m.Kind == ns.KindTTY {
			t.Errorf("the defaults serve a %s namespace at %s", m.Kind, m.Path)
		}
	}
}

// Handlers accumulate, and every one of them runs.
func TestMessageHandlersAllRun(t *testing.T) {
	cfg := load(t, `
		local drop = require("drop")
		drop.mount("/chat", { type = "chat" })
		seen = {}
		drop.on.message(function(m) seen[#seen + 1] = "first:" .. m.body end)
		drop.on.message(function(m) seen[#seen + 1] = "second:" .. m.from end)
	`)

	cfg.FireMessage(Message{From: "laptop", Kind: "text", Body: "hello"})

	got := luaStrings(t, cfg, "seen")
	if len(got) != 2 || got[0] != "first:hello" || got[1] != "second:laptop" {
		t.Fatalf("handlers produced %v", got)
	}
}

// One handler raising must not stop the others.
func TestARaisingHandlerDoesNotStopTheRest(t *testing.T) {
	cfg := load(t, `
		local drop = require("drop")
		drop.mount("/chat", { type = "chat" })
		seen = {}
		drop.on.message(function(m) error("deliberate") end)
		drop.on.message(function(m) seen[#seen + 1] = m.body end)
	`)

	cfg.FireMessage(Message{From: "laptop", Kind: "text", Body: "still delivered"})

	got := luaStrings(t, cfg, "seen")
	if len(got) != 1 || got[0] != "still delivered" {
		t.Fatalf("the second handler did not run: %v", got)
	}
}

func TestFileHandlersReceiveTheDetails(t *testing.T) {
	cfg := load(t, `
		local drop = require("drop")
		drop.mount("/inbox", { type = "files", dir = "/tmp/x" })
		seen = {}
		drop.on.file(function(f) seen[#seen + 1] = f.name .. ":" .. tostring(f.size) end)
	`)

	cfg.FireFile(File{From: "laptop", Name: "report.pdf", Size: 4096})

	got := luaStrings(t, cfg, "seen")
	if len(got) != 1 || got[0] != "report.pdf:4096" {
		t.Fatalf("the file handler saw %v", got)
	}
}

// Firing with no handlers, and after Close, must not panic.
func TestFiringIsSafeWhenNothingIsRegistered(t *testing.T) {
	cfg := load(t, `
		local drop = require("drop")
		drop.mount("/chat", { type = "chat" })
	`)

	cfg.FireMessage(Message{Body: "nobody is listening"})
	cfg.FireFile(File{Name: "x"})

	cfg.Close()
	cfg.FireMessage(Message{Body: "after close"})
	cfg.Close()
}

func TestTildeIsExpanded(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory")
	}

	cfg := load(t, `
		local drop = require("drop")
		drop.mount("/inbox", { type = "files", dir = "~/Downloads" })
	`)

	m, _, _ := cfg.Mounts.Lookup("/inbox")
	if m.Dir != filepath.Join(home, "Downloads") {
		t.Fatalf("Dir = %q, want it expanded", m.Dir)
	}
}

// luaStrings reads a global list back out, which is how a test sees what a handler did.
func luaStrings(t *testing.T, cfg *Config, global string) []string {
	t.Helper()

	cfg.rt.mu.Lock()
	defer cfg.rt.mu.Unlock()

	list, ok := cfg.rt.lua.GlobalEnv().Get(rt.StringValue(global)).TryTable()
	if !ok {
		t.Fatalf("global %q is not a table", global)
	}

	var out []string

	for i := int64(1); ; i++ {
		s, ok := list.Get(rt.IntValue(i)).TryString()
		if !ok {
			return out
		}
		out = append(out, s)
	}
}

// A setting the config never mentions must be left alone, not read back as a zero that then
// overwrites the environment.
func TestUnmentionedSettingsStayUnset(t *testing.T) {
	cfg := load(t, `
		local drop = require("drop")
		drop.mount("/chat", { type = "chat" })
	`)

	if cfg.HasName {
		t.Error("HasName is true for a config that never assigned a name")
	}
	if cfg.HasOpenLinks {
		t.Error("HasOpenLinks is true for a config that never assigned it")
	}
	if cfg.Bootstrap != nil || cfg.Relays != nil {
		t.Errorf("Bootstrap/Relays came back as %v/%v, want nil", cfg.Bootstrap, cfg.Relays)
	}
}

// Assigning false has to be distinguishable from not assigning at all.
func TestOpenLinksFalseIsStillMentioned(t *testing.T) {
	cfg := load(t, `
		local drop = require("drop")
		drop.open_links = false
		drop.mount("/chat", { type = "chat" })
	`)

	if !cfg.HasOpenLinks {
		t.Fatal("assigning false left HasOpenLinks unset")
	}
	if cfg.OpenLinks {
		t.Fatal("OpenLinks is true after being assigned false")
	}
}

// The handler lists live in Lua, so a config can read them back.
func TestHandlerListIsVisibleToTheConfig(t *testing.T) {
	cfg := load(t, `
		local drop = require("drop")
		drop.mount("/chat", { type = "chat" })
		drop.on.message(function(m) end)
		drop.on.message(function(m) end)
		count = { tostring(#drop.handlers.message) }
	`)

	got := luaStrings(t, cfg, "count")
	if len(got) != 1 || got[0] != "2" {
		t.Fatalf("the config saw %v handlers, want 2", got)
	}
}

// Assigning the list outright replaces what was registered, which is how a config overrides a
// shared fragment.
func TestHandlerListCanBeAssignedOutright(t *testing.T) {
	cfg := load(t, `
		local drop = require("drop")
		drop.mount("/chat", { type = "chat" })
		seen = {}
		drop.on.message(function(m) seen[#seen + 1] = "registered" end)
		drop.handlers.message = { function(m) seen[#seen + 1] = "replaced" end }
	`)

	cfg.FireMessage(Message{Body: "x"})

	got := luaStrings(t, cfg, "seen")
	if len(got) != 1 || got[0] != "replaced" {
		t.Fatalf("handlers produced %v, want only the replacement", got)
	}
}

// require and the global must be the same table, or a config written either way gets a different
// object and half its registrations vanish.
func TestRequireAndGlobalAreTheSameTable(t *testing.T) {
	cfg := load(t, `
		local required = require("drop")
		same = { tostring(required == drop) }
		required.mount("/chat", { type = "chat" })
		drop.mount("/inbox", { type = "files", dir = "/tmp/x" })
	`)

	got := luaStrings(t, cfg, "same")
	if len(got) != 1 || got[0] != "true" {
		t.Fatalf("require(\"drop\") == drop is %v", got)
	}
	if cfg.Mounts.Len() != 2 {
		t.Fatalf("Len() = %d, want both mounts", cfg.Mounts.Len())
	}
}

// A load-time raise must name the file and the line, or the user is told only that something
// somewhere is wrong.
func TestLoadTimeRaiseNamesFileAndLine(t *testing.T) {
	path := write(t, "local drop = require(\"drop\")\ndrop.mount(\"/x\", { type = \"nonsense\" })\n")

	_, err := Load()
	if err == nil {
		t.Fatal("Load() accepted a mount with an unknown type")
	}
	if !strings.Contains(err.Error(), filepath.Base(path)) {
		t.Errorf("the error does not name the file: %v", err)
	}
	if !strings.Contains(err.Error(), "nonsense") {
		t.Errorf("the error does not say what was wrong: %v", err)
	}
}

// A path anybody may reach has to be asked for in as many words, and it must not be reachable by
// mistake: every other spelling of access leaves it shut.
func TestAPublicPathIsAskedForByName(t *testing.T) {
	cfg := load(t, `
		local drop = require("drop")
		drop.mount("/wide",   { type = "chat", access = "anyone" })
		drop.mount("/table",  { type = "chat", access = { anyone = true } })
		drop.mount("/paired", { type = "chat", access = "paired" })
		drop.mount("/named",  { type = "chat", access = { "bob" } })
	`)

	for path, want := range map[string]bool{
		"/wide": true, "/table": true, "/paired": false, "/named": false,
	} {
		mount, _, ok := cfg.Mounts.Lookup(path)
		if !ok {
			t.Fatalf("%s is not mounted", path)
		}
		if mount.Access.Anyone != want {
			t.Errorf("%s: public is %v, wanted %v", path, mount.Access.Anyone, want)
		}
	}
}

// A vault is one recipient or several, and both spellings mean the same thing.
func TestAVaultIsOneRecipientOrSeveral(t *testing.T) {
	one := load(t, `
		local drop = require("drop")
		drop.vault = "~/.config/drop/vault.key"
		drop.mount("/chat", { type = "chat" })
	`)
	if len(one.Vault) != 1 || one.Vault[0] != "~/.config/drop/vault.key" {
		t.Errorf("one recipient came out as %+v", one.Vault)
	}

	many := load(t, `
		local drop = require("drop")
		drop.vault = { "age1yubikey1abc", "age1def" }
		drop.mount("/chat", { type = "chat" })
	`)
	if len(many.Vault) != 2 {
		t.Errorf("two recipients came out as %+v", many.Vault)
	}

	none := load(t, `
		local drop = require("drop")
		drop.mount("/chat", { type = "chat" })
	`)
	if len(none.Vault) != 0 {
		t.Errorf("a config with no vault came out as %+v", none.Vault)
	}
}

// Visible is its own option, because it answers a different question from access: access says who
// gets in, visible says who is told there is a door.
func TestVisibleIsReadSeparatelyFromAccess(t *testing.T) {
	cfg := load(t, `
		local drop = require("drop")
		drop.mount("/shared", { type = "chat", access = { "bob" }, visible = { "carol" } })
		drop.mount("/asked",  { type = "chat", visible = "paired" })
		drop.mount("/secret", { type = "chat", access = { "bob" } })
	`)

	shared, _, _ := cfg.Mounts.Lookup("/shared")
	if len(shared.Access.Named) != 1 || shared.Access.Named[0] != "bob" {
		t.Errorf("access = %v", shared.Access.Named)
	}
	if len(shared.Access.Visible) != 1 || shared.Access.Visible[0] != "carol" {
		t.Errorf("visible = %v", shared.Access.Visible)
	}

	asked, _, _ := cfg.Mounts.Lookup("/asked")
	if !asked.Access.AnyVisible {
		t.Error("visible = \"paired\" did not take")
	}
	if asked.Access.Declared() {
		t.Error("a path that is only visible was read as being shared with somebody")
	}

	secret, _, _ := cfg.Mounts.Lookup("/secret")
	if secret.Access.Shows() {
		t.Error("a path with no visible option came out visible")
	}

	// And a path that is only visible still governs itself, rather than falling through to nothing.
	carol := ns.Caller{ID: "abc", Name: "laptop", UserName: "carol", Paired: true}
	if !cfg.Mounts.Sees("/asked", carol) {
		t.Error("a visible-only path could not be seen by anybody")
	}
	if ok, _ := cfg.Mounts.Admits("/asked", carol); ok {
		t.Error("a visible-only path let somebody in")
	}
}
