package conf

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	rt "github.com/arnodel/golua/runtime"

	"github.com/bresilla/drop/src/pkg/arch"
	"github.com/bresilla/drop/src/pkg/arch/chat"
	"github.com/bresilla/drop/src/pkg/arch/files"
	"github.com/bresilla/drop/src/pkg/arch/link"
	"github.com/bresilla/drop/src/pkg/arch/share"
	"github.com/bresilla/drop/src/pkg/arch/stream"
	"github.com/bresilla/drop/src/pkg/arch/tty"
	"github.com/bresilla/drop/src/pkg/node"
	"github.com/bresilla/drop/src/pkg/ns"
)

// known is what a config may name here: the archetypes drop ships, which is what the reader is
// handed by every command that serves.
func known() *arch.Registry {
	r := arch.NewRegistry()
	r.Register(share.New(share.Into{}))
	r.Register(files.New(files.Into{}))
	r.Register(chat.New(chat.Into{}))
	r.Register(link.New(link.Into{}))
	r.Register(stream.New(stream.Into{}))
	r.Register(tty.New(tty.Into{}))
	return r
}

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
	cfg, err := Load(known())
	if err != nil {
		t.Fatalf("Load(known()): %v", err)
	}
	t.Cleanup(cfg.Close)
	return cfg
}

func TestSettingsAreAssigned(t *testing.T) {
	cfg := load(t, `
		local drop = require("drop")
		drop.name = "workstation"
		drop.open_links = true
		drop.relays = { "https://one.example./", "https://two.example./" }
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

func TestUnsupportedBootstrapNodesAreRefused(t *testing.T) {
	path := write(t, `
		local drop = require("drop")
		drop.bootstrap = { "old-dht-node" }
		drop.mount("/chat", { type = "chat" })
	`)

	if _, err := Load(known()); err == nil || !strings.Contains(err.Error(), "drop.bootstrap") {
		t.Fatalf("Load(%s) = %v", path, err)
	}
}

func TestInvalidConfigCannotBeIgnoredByDiallingCommands(t *testing.T) {
	path := write(t, `this is not lua`)

	if err := ApplySettings(known()); err == nil {
		t.Fatalf("ApplySettings ignored invalid config at %s", path)
	}
}

func TestSettingsCanBeAppliedWithoutServingNamespaces(t *testing.T) {
	write(t, `
		local drop = require("drop")
		drop.name = "workstation"
	`)

	if err := ApplySettings(known()); err != nil {
		t.Fatalf("ApplySettings(): %v", err)
	}
}

func TestBooleanSettingsMustBeBoolean(t *testing.T) {
	for _, setting := range []string{"open_links", "rendezvous", "direct"} {
		path := write(t, `
			local drop = require("drop")
			drop.mount("/chat", { type = "chat" })
			drop.`+setting+` = "false"
		`)
		if _, err := Load(known()); err == nil || !strings.Contains(err.Error(), setting) {
			t.Errorf("%s with a string at %s returned %v", setting, path, err)
		}
	}
}

func TestListSettingsMustContainOnlyStrings(t *testing.T) {
	for _, value := range []string{`"https://relay.example./"`, `{ "https://relay.example./", 7 }`} {
		path := write(t, `
			local drop = require("drop")
			drop.mount("/chat", { type = "chat" })
			drop.relays = `+value+`
		`)
		if _, err := Load(known()); err == nil || !strings.Contains(err.Error(), "relays") {
			t.Errorf("relays = %s at %s returned %v", value, path, err)
		}
	}
}

func TestAStringFalseDoesNotOpenAccessToAnyone(t *testing.T) {
	cfg := load(t, `
		local drop = require("drop")
		drop.mount("/private", {
			type = "chat",
			access = { anyone = "false" },
		})
	`)

	if allowed, _ := cfg.Mounts.Admits("/private", ns.Caller{ID: "stranger"}); allowed {
		t.Fatal("the string false opened a path to anyone")
	}
}

func TestMountsAreRegistered(t *testing.T) {
	cfg := load(t, `
		local drop = require("drop")
		drop.mount("/inbox", { type = "share", dir = "/tmp/in" })
		drop.mount("/logs",  { type = "stream", command = "tail -f /var/log/x" })
		drop.mount("/term",  { type = "tty", input = true })
		drop.mount("/work",  { type = "files", dir = "/tmp/work", writable = true })
	`)

	m, _, ok := cfg.Mounts.Lookup("/inbox")
	shared, sharedOK := m.Config.(share.Config)
	if !ok || m.Archetype != "share" || !sharedOK || shared.Dir != "/tmp/in" {
		t.Fatalf("/inbox = %+v ok %v", m, ok)
	}
	m, _, _ = cfg.Mounts.Lookup("/term")
	if m.Config != (tty.Config{Input: true}) {
		t.Errorf("/term = %+v", m.Config)
	}
	m, _, ok = cfg.Mounts.Lookup("/work")
	if !ok || m.Archetype != "files" || m.Config != (files.Config{Dir: "/tmp/work", Writable: true}) {
		t.Errorf("/work = %+v ok %v", m, ok)
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

func TestConfigCanRequireLocalModulesWithinLimits(t *testing.T) {
	dir := t.TempDir()
	module := filepath.Join(dir, "helper.lua")
	if err := os.WriteFile(module, []byte(`return { path = "/chat" }`), 0o600); err != nil {
		t.Fatalf("writing module: %v", err)
	}

	cfg := load(t, fmt.Sprintf(`
		package.path = %q
		package.preload.extra = function() return { path = "/inbox" } end
		local drop = require("drop")
		local found, problem = package.searchpath("helper", package.path)
		if not found then error(problem) end
		local helper = require("helper")
		local extra = require("extra")
		drop.mount(helper.path, { type = "chat" })
		drop.mount(extra.path, { type = "share", dir = "/tmp/in" })
	`, module))

	if _, _, ok := cfg.Mounts.Lookup("/chat"); !ok {
		t.Fatal("the module-provided mount is missing")
	}
	if _, _, ok := cfg.Mounts.Lookup("/inbox"); !ok {
		t.Fatal("the preloaded mount is missing")
	}
}

func TestRequiredModulesUseTheConfigBudget(t *testing.T) {
	dir := t.TempDir()
	module := filepath.Join(dir, "stuck.lua")
	if err := os.WriteFile(module, []byte(`while true do end`), 0o600); err != nil {
		t.Fatalf("writing module: %v", err)
	}
	path := write(t, fmt.Sprintf(`
		package.path = %q
		require("stuck")
	`, module))

	done := make(chan error, 1)
	go func() {
		cfg, err := Load(known())
		if cfg != nil {
			cfg.Close()
		}
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "CPU limit") || !strings.Contains(err.Error(), filepath.Base(path)) {
			t.Fatalf("Load(%s) returned %v", path, err)
		}
	case <-time.After(time.Second):
		t.Fatal("required module did not stop at the config CPU limit")
	}
}

func TestConfigRuntimesCanLoadConcurrently(t *testing.T) {
	dir := t.TempDir()
	const count = 8
	paths := make([]string, count)
	for i := range paths {
		paths[i] = filepath.Join(dir, fmt.Sprintf("init-%d.lua", i))
		if err := os.WriteFile(paths[i], []byte(`
			local drop = require("drop")
			drop.mount("/chat", { type = "chat" })
		`), 0o600); err != nil {
			t.Fatalf("writing config: %v", err)
		}
	}

	start := make(chan struct{})
	errs := make(chan error, count)
	for _, path := range paths {
		go func() {
			<-start
			cfg := &Config{Mounts: ns.NewTable(), known: known(), Path: path}
			err := run(cfg, path)
			cfg.Close()
			errs <- err
		}()
	}
	close(start)
	for range paths {
		if err := <-errs; err != nil {
			t.Fatalf("concurrent run(): %v", err)
		}
	}
}

func TestTrustedConfigLibrariesWorkWithinLimits(t *testing.T) {
	cfg := load(t, `
		local drop = require("drop")
		drop.mount("/chat", { type = "chat" })
		local pipe = io.popen("printf ready")
		startup = { pipe:read("a"), tostring(collectgarbage("count") > 0), os.setlocale("C") }
		pipe:close()
		ran = 0
		drop.on.message(function()
			local ok = os.execute("true")
			if not ok then error("command failed") end
			ran = ran + 1
		end)
	`)

	startup := luaStrings(t, cfg, "startup")
	if len(startup) != 3 || startup[0] != "ready" || startup[1] != "true" || startup[2] != "C" {
		t.Fatalf("trusted libraries produced %v", startup)
	}
	cfg.FireMessage(Message{})
	cfg.rt.mu.Lock()
	ran, ok := cfg.rt.lua.GlobalEnv().Get(rt.StringValue("ran")).TryInt()
	cfg.rt.mu.Unlock()
	if !ok || ran != 1 {
		t.Fatalf("os.execute handler ran %d times", ran)
	}
}

// A config that does not parse must not start with half a table.
func TestBrokenConfigIsFatalAndNamesTheFile(t *testing.T) {
	path := write(t, "this is not lua at all ((((")

	_, err := Load(known())
	if err == nil {
		t.Fatal("Load(known()) accepted a config that does not parse")
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

	if _, err := Load(known()); err == nil {
		t.Fatal("Load(known()) accepted a mount with no type")
	}
}

func TestAShareMountWithoutADirIsRefused(t *testing.T) {
	write(t, `
		local drop = require("drop")
		drop.mount("/inbox", { type = "share" })
	`)

	if _, err := Load(known()); err == nil {
		t.Fatal("Load(known()) accepted a share namespace with no dir")
	}
}

func TestAFilesMountWithoutADirIsRefused(t *testing.T) {
	write(t, `
		local drop = require("drop")
		drop.mount("/work", { type = "files" })
	`)

	if _, err := Load(known()); err == nil {
		t.Fatal("Load(known()) accepted a files namespace with no dir")
	}
}

// A type nobody registered is a typo, and the answer says what this build does have.
func TestAnUnknownTypeIsRefusedAtLoad(t *testing.T) {
	write(t, `
		local drop = require("drop")
		drop.mount("/camera", { type = "camera" })
	`)

	_, err := Load(known())
	if err == nil {
		t.Fatal("Load() accepted a namespace type that does not exist")
	}
	for _, word := range []string{"camera", "chat", "share", "tty"} {
		if !strings.Contains(err.Error(), word) {
			t.Errorf("the refusal does not mention %q: %v", word, err)
		}
	}
}

// A stream with nothing to run cannot work, and the archetype says so where it is written.
func TestAStreamMountWithoutACommandIsRefused(t *testing.T) {
	write(t, `
		local drop = require("drop")
		drop.mount("/logs", { type = "stream" })
	`)

	if _, err := Load(known()); err == nil {
		t.Fatal("Load() accepted a stream namespace with no command")
	}
}

func TestConfigSettingOnlyServesTheDefaults(t *testing.T) {
	write(t, `local drop = require("drop")
drop.name = "desk"`)

	cfg, err := Load(known())
	if err != nil {
		t.Fatalf("Load(known()) refused a config that only sets things: %v", err)
	}
	defer cfg.Close()
	if cfg.Name != "desk" {
		t.Errorf("Name = %q, want desk", cfg.Name)
	}
	if m, _, ok := cfg.Mounts.Lookup("/chat"); !ok || m.Path != "/chat" {
		t.Error("a config that declares no namespaces does not serve /chat")
	}
}

func TestDefaultsWhenThereIsNoFile(t *testing.T) {
	t.Setenv("DROP_CONFIG", filepath.Join(t.TempDir(), "absent.lua"))

	cfg, err := Load(known())
	if err != nil {
		t.Fatalf("Load(known()): %v", err)
	}
	if cfg.Path != "" {
		t.Errorf("Path = %q, want empty for the defaults", cfg.Path)
	}
	if _, _, ok := cfg.Mounts.Lookup("/inbox"); !ok {
		t.Error("the defaults do not serve /inbox")
	}
	// The defaults must not run a command or share a terminal; those are decisions.
	for _, m := range cfg.Mounts.All() {
		if m.Archetype == "stream" || m.Archetype == "tty" {
			t.Errorf("the defaults serve a %s namespace at %s", m.Archetype, m.Path)
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

func TestConfigEvaluationHasACPUCostLimit(t *testing.T) {
	path := write(t, `while true do end`)
	done := make(chan error, 1)
	go func() {
		cfg, err := Load(known())
		if cfg != nil {
			cfg.Close()
		}
		done <- err
	}()

	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "CPU limit") || !strings.Contains(err.Error(), filepath.Base(path)) {
			t.Fatalf("Load(%s) returned %v", path, err)
		}
	case <-time.After(time.Second):
		t.Fatal("config evaluation did not stop at its CPU limit")
	}
}

func TestConfigEvaluationHasAMemoryCostLimit(t *testing.T) {
	path := write(t, `
		local held = {}
		while true do held[#held + 1] = string.rep("x", 4096) end
	`)
	done := make(chan error, 1)
	go func() {
		cfg, err := Load(known())
		if cfg != nil {
			cfg.Close()
		}
		done <- err
	}()

	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "memory limit") || !strings.Contains(err.Error(), filepath.Base(path)) {
			t.Fatalf("Load(%s) returned %v", path, err)
		}
	case <-time.After(time.Second):
		t.Fatal("config evaluation did not stop at its memory limit")
	}
}

func TestConfigBoundaryRecoversPanics(t *testing.T) {
	runtime := &runtime{lua: rt.New(os.Stderr)}
	defer runtime.close()

	err := runtime.within(func() error { panic("deliberate") })
	if err == nil || !strings.Contains(err.Error(), "deliberate") {
		t.Fatalf("within() returned %v", err)
	}
	if err := runtime.within(func() error { return nil }); err != nil {
		t.Fatalf("the runtime was not reusable: %v", err)
	}
}

func TestAHandlerCostLimitDoesNotStopLaterEvents(t *testing.T) {
	cfg := load(t, `
		local drop = require("drop")
		drop.mount("/chat", { type = "chat" })
		seen = {}
		drop.on.message(function(m) while true do end end)
		drop.on.message(function(m) seen[#seen + 1] = m.body end)
	`)

	for _, body := range []string{"first", "second"} {
		done := make(chan struct{})
		go func() {
			cfg.FireMessage(Message{Body: body})
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatalf("handler for %q did not stop at its CPU limit", body)
		}
	}

	got := luaStrings(t, cfg, "seen")
	if len(got) != 2 || got[0] != "first" || got[1] != "second" {
		t.Fatalf("later handlers and events produced %v", got)
	}
}

func TestHandlerRegistrationIsBounded(t *testing.T) {
	path := write(t, fmt.Sprintf(`
		local drop = require("drop")
		drop.mount("/chat", { type = "chat" })
		for i = 1, %d do
			drop.on.message(function(m) end)
		end
	`, MaxHandlers+1))

	if _, err := Load(known()); err == nil || !strings.Contains(err.Error(), fmt.Sprint(MaxHandlers)) {
		t.Fatalf("Load(%s) accepted too many handlers: %v", path, err)
	}
}

func TestAssignedHandlerListIsBoundedWhenFired(t *testing.T) {
	cfg := load(t, fmt.Sprintf(`
		local drop = require("drop")
		drop.mount("/chat", { type = "chat" })
		seen = 0
		local handlers = {}
		for i = 1, %d do
			handlers[i] = function(m) seen = seen + 1 end
		end
		drop.handlers.message = handlers
	`, MaxHandlers+1))

	cfg.FireMessage(Message{Body: "bounded"})
	cfg.rt.mu.Lock()
	seen, ok := cfg.rt.lua.GlobalEnv().Get(rt.StringValue("seen")).TryInt()
	cfg.rt.mu.Unlock()
	if !ok || seen != int64(MaxHandlers) {
		t.Fatalf("ran %d handlers, want %d", seen, MaxHandlers)
	}
}

func TestFileHandlersReceiveTheDetails(t *testing.T) {
	cfg := load(t, `
		local drop = require("drop")
		drop.mount("/inbox", { type = "share", dir = "/tmp/x" })
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
		drop.mount("/inbox", { type = "share", dir = "~/Downloads" })
	`)

	m, _, _ := cfg.Mounts.Lookup("/inbox")
	shared, ok := m.Config.(share.Config)
	if !ok || shared.Dir != filepath.Join(home, "Downloads") {
		t.Fatalf("dir = %+v, want it expanded", m.Config)
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
		drop.mount("/inbox", { type = "share", dir = "/tmp/x" })
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

	_, err := Load(known())
	if err == nil {
		t.Fatal("Load(known()) accepted a mount with an unknown type")
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

// A config may pin which revision of an archetype it means, and is answered plainly when this
// build has no such revision.
func TestAMountCanPinAVersion(t *testing.T) {
	cfg := load(t, `
		local drop = require("drop")
		drop.mount("/chat", { type = "chat", version = 1 })
	`)

	if m, _, _ := cfg.Mounts.Lookup("/chat"); m.Version != 1 {
		t.Errorf("/chat = %+v", m)
	}

	write(t, `
		local drop = require("drop")
		drop.mount("/chat", { type = "chat", version = 4 })
	`)

	_, err := Load(known())
	if err == nil {
		t.Fatal("Load() accepted a revision this build does not have")
	}
	if !strings.Contains(err.Error(), "chat/4") {
		t.Errorf("the refusal does not name the revision: %v", err)
	}
}

// Publishing the addresses this machine is on is what lets two devices on one wire reach each
// other without a relay, and it is also what tells a reader which networks this machine is on. A
// config that turns it off must be obeyed.
func TestDirectCanBeTurnedOff(t *testing.T) {
	write(t, `
		local drop = require("drop")
		drop.direct = false
		drop.mount("/chat", { type = "chat", access = "paired" })
	`)

	cfg, err := Load(known())
	if err != nil {
		t.Fatalf("Load(): %v", err)
	}
	if !cfg.HasDirect || cfg.Direct {
		t.Fatalf("direct = %v, said = %v", cfg.Direct, cfg.HasDirect)
	}

	cfg.Apply()
	if node.Direct() {
		t.Error("the config said not to publish this machine's own addresses, and it does")
	}
	node.SetDirect(true)
}

// A bare word that is none of the three shorthands is one name, the same as a list of one: "me"
// written as a string was a path nobody at all could open.
func TestABareNameIsOneName(t *testing.T) {
	cfg := load(t, `
		local drop = require("drop")
		drop.mount("/mine", { type = "chat", access = "me" })
		drop.mount("/bobs", { type = "chat", access = "bob" })
	`)

	mine := ns.Caller{ID: "a", Name: "laptop", UserName: "me", Paired: true, Trusted: true}
	bob := ns.Caller{ID: "b", Name: "bob", UserName: "bob", Paired: true}
	if ok, why := cfg.Mounts.Admits("/mine", mine); !ok {
		t.Fatalf("access = \"me\" refused a machine of mine: %s", why)
	}
	if ok, _ := cfg.Mounts.Admits("/mine", bob); ok {
		t.Fatal("access = \"me\" let somebody else in")
	}
	if ok, why := cfg.Mounts.Admits("/bobs", bob); !ok {
		t.Fatalf("access = \"bob\" refused bob: %s", why)
	}
}
