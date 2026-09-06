package conf

import (
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/arnodel/golua/lib"
	rt "github.com/arnodel/golua/runtime"

	"github.com/bresilla/drop/src/pkg/arch"
	"github.com/bresilla/drop/src/pkg/keep"
	"github.com/bresilla/drop/src/pkg/ns"
	"github.com/bresilla/drop/src/pkg/passwd"
	"github.com/bresilla/drop/src/pkg/user"
)

// runtime is the Lua state a config left behind.
//
// It stays alive because handlers registered in the config are called later, from whichever
// goroutine a message or a file arrived on. A Lua runtime is not safe for concurrent use, so every
// call through it takes the lock.
type runtime struct {
	mu      sync.Mutex
	lua     *rt.Runtime
	release func()

	// handlers is the Lua table holding one list per event.
	handlers *rt.Table
}

var luaLoading sync.Mutex

// MaxHandlers is how many callbacks one configuration event may run.
const MaxHandlers = 64

const (
	configSteps = 2_000_000
	configBytes = 8 << 20
	configSafe  = rt.ComplyCpuSafe | rt.ComplyMemSafe
)

func (r *runtime) close() {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.release != nil {
		r.release()
		r.release = nil
	}
	r.lua = nil
	r.handlers = nil
}

// within runs one Lua call under the configuration resource limits.
func (r *runtime) within(call func() error) (err error) {
	defer func() {
		if caught := recover(); caught != nil {
			err = fmt.Errorf("lua runtime: %v", caught)
		}
	}()

	_, err = r.lua.MainThread().CallContext(rt.RuntimeContextDef{
		HardLimits: rt.RuntimeResources{Cpu: configSteps, Memory: configBytes},
	}, call)
	return err
}

// fire runs every handler registered for an event, in registration order.
//
// A raise is reported with the event and position, so the config author knows which handler to
// look at, and the rest still run — a mistake in the third is not a reason to skip the fourth.
//
// The list is read at call time rather than cached, so a config that assigned drop.handlers
// outright is honoured too.
func (r *runtime) fire(event string, arg rt.Value) {
	if r.handlers == nil || r.lua == nil {
		return
	}
	list, ok := r.handlers.Get(rt.StringValue(event)).TryTable()
	if !ok {
		return
	}

	for i := int64(1); i <= MaxHandlers; i++ {
		fn := list.Get(rt.IntValue(i))
		if fn.IsNil() {
			return
		}
		if err := r.within(func() error {
			_, err := rt.Call1(r.lua.MainThread(), fn, arg)
			return err
		}); err != nil {
			fmt.Fprintf(os.Stderr, "drop: on.%s handler #%d: %v\n", event, i, err)
		}
	}
	fmt.Fprintf(os.Stderr, "drop: on.%s has more than %d handlers; the rest were not run\n", event, MaxHandlers)
}

// Message is what a config's on.message handlers are given.
type Message struct {
	From string
	Kind string
	Body string
	Path string
}

// File is what a config's on.file handlers are given.
type File struct {
	From string
	Name string
	Size int64
	Dir  string
	Path string
}

// FireMessage runs the config's message handlers. They are pure side effect: what they return is
// ignored.
func (c *Config) FireMessage(m Message) {
	r := c.rt
	if r == nil {
		return
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.lua == nil {
		return
	}

	// Built once and reused, rather than rebuilt for each handler.
	t := rt.NewTable()
	t.Set(rt.StringValue("from"), rt.StringValue(m.From))
	t.Set(rt.StringValue("kind"), rt.StringValue(m.Kind))
	t.Set(rt.StringValue("body"), rt.StringValue(m.Body))
	t.Set(rt.StringValue("path"), rt.StringValue(m.Path))

	r.fire("message", rt.TableValue(t))
}

// FireFile runs the config's file handlers. They are pure side effect: what they return is
// ignored.
func (c *Config) FireFile(f File) {
	r := c.rt
	if r == nil {
		return
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.lua == nil {
		return
	}

	t := rt.NewTable()
	t.Set(rt.StringValue("from"), rt.StringValue(f.From))
	t.Set(rt.StringValue("name"), rt.StringValue(f.Name))
	t.Set(rt.StringValue("size"), rt.IntValue(f.Size))
	t.Set(rt.StringValue("dir"), rt.StringValue(f.Dir))
	t.Set(rt.StringValue("path"), rt.StringValue(f.Path))

	r.fire("file", rt.TableValue(t))
}

// Close releases the Lua runtime.
func (c *Config) Close() {
	c.rt.close()
}

// run executes the config file against the `drop` module.
//
// Settings are assigned, namespaces and handlers are registered, and the file returns nothing — so
// it can branch on the machine it is running on rather than describing one shape and hoping it
// fits everywhere.
func run(cfg *Config, path string) error {
	source, err := keep.ReadFile(path, keep.MaxState)
	if err != nil {
		return fmt.Errorf("reading %s: %w", path, err)
	}

	// The config's own print goes to stderr, so it cannot be mistaken for the output of whatever
	// command is running.
	machine := rt.New(os.Stderr)
	state := &runtime{lua: machine, release: loadConfigLibraries(machine)}
	cfg.rt = state

	fail := func(err error) error {
		state.close()
		cfg.rt = nil
		return fmt.Errorf("%s: %w", path, err)
	}

	module := rt.NewTable()
	machine.SetEnv(machine.GlobalEnv(), "drop", rt.TableValue(module))
	mountFunc := machine.SetEnvGoFunc(module, "mount", func(t *rt.Thread, c *rt.GoCont) (rt.Cont, error) {
		return mount(c, cfg)
	}, 2, false)

	// Behaviour is registered, and registration repeats: a config may add as many handlers as it
	// likes, and every one of them runs.
	//
	// The lists live in Lua on the module table rather than in Go, so a config can read them back,
	// or assign one outright to replace what a shared fragment registered.
	handlers := rt.NewTable()
	handlers.Set(rt.StringValue("message"), rt.TableValue(rt.NewTable()))
	handlers.Set(rt.StringValue("file"), rt.TableValue(rt.NewTable()))
	machine.SetEnv(module, "handlers", rt.TableValue(handlers))
	state.handlers = handlers

	on := rt.NewTable()
	machine.SetEnv(module, "on", rt.TableValue(on))
	messageFunc := machine.SetEnvGoFunc(on, "message", func(t *rt.Thread, c *rt.GoCont) (rt.Cont, error) {
		return register(c, handlers, "message")
	}, 1, false)
	fileFunc := machine.SetEnvGoFunc(on, "file", func(t *rt.Thread, c *rt.GoCont) (rt.Cont, error) {
		return register(c, handlers, "file")
	}, 1, false)
	if err := allowConfigFunctions(machine, mountFunc, messageFunc, fileFunc); err != nil {
		return fail(err)
	}

	// `require("drop")` and the global reach the same table, so a config may be written either way
	// without one of them being a different object.
	if err := preload(machine, module); err != nil {
		return fail(err)
	}

	// The chunk is named for the file, so a raise says which line of which config.
	chunk, err := machine.CompileAndLoadLuaChunk(path, source, rt.TableValue(machine.GlobalEnv()))
	if err != nil {
		return fail(err)
	}
	// Whatever the chunk evaluated to is discarded. It should be nothing.
	if err := state.within(func() error {
		_, err := rt.Call1(machine.MainThread(), rt.FunctionValue(chunk))
		return err
	}); err != nil {
		return fail(err)
	}

	if err := readSettings(cfg, module); err != nil {
		return fail(err)
	}
	if err := cfg.name(); err != nil {
		return fail(err)
	}
	return nil
}

func loadConfigLibraries(machine *rt.Runtime) func() {
	luaLoading.Lock()
	defer luaLoading.Unlock()
	return lib.LoadAll(machine)
}

// allowConfigFunctions marks the host calls available to trusted configuration code.
func allowConfigFunctions(machine *rt.Runtime, own ...*rt.GoFunction) error {
	functions := append([]*rt.GoFunction{}, own...)
	for _, entry := range []struct {
		table *rt.Table
		name  string
	}{
		{machine.GlobalEnv(), "require"},
		{machine.GlobalEnv(), "collectgarbage"},
	} {
		fn, err := configFunction(entry.table, entry.name)
		if err != nil {
			return err
		}
		functions = append(functions, fn)
	}
	packageTable, ok := machine.GlobalEnv().Get(rt.StringValue("package")).TryTable()
	if !ok {
		return fmt.Errorf("the package library is missing")
	}
	searchers, ok := packageTable.Get(rt.StringValue("searchers")).TryTable()
	if !ok {
		return fmt.Errorf("package.searchers is missing")
	}
	loader, err := configModuleLoader(machine)
	if err != nil {
		return err
	}
	preloadSearcher := rt.NewGoFunction(func(t *rt.Thread, c *rt.GoCont) (rt.Cont, error) {
		return searchConfigPreload(t, c, packageTable)
	}, "config preload searcher", 1, false)
	moduleSearcher := rt.NewGoFunction(func(t *rt.Thread, c *rt.GoCont) (rt.Cont, error) {
		return searchConfigModule(t, c, packageTable, loader)
	}, "config module searcher", 1, false)
	searchPath := machine.SetEnvGoFunc(packageTable, "searchpath", func(t *rt.Thread, c *rt.GoCont) (rt.Cont, error) {
		return searchConfigPath(t, c, packageTable)
	}, 4, false)
	rt.SolemnlyDeclareCompliance(configSafe, preloadSearcher, moduleSearcher, searchPath)
	machine.SetTable(searchers, rt.IntValue(1), rt.FunctionValue(preloadSearcher))
	machine.SetTable(searchers, rt.IntValue(2), rt.FunctionValue(moduleSearcher))

	osTable, ok := machine.GlobalEnv().Get(rt.StringValue("os")).TryTable()
	if !ok {
		return fmt.Errorf("the os library is missing")
	}
	for _, name := range []string{"execute", "exit", "setlocale"} {
		fn, err := configFunction(osTable, name)
		if err != nil {
			return err
		}
		functions = append(functions, fn)
	}

	rt.SolemnlyDeclareCompliance(configSafe, functions...)
	return nil
}

func configFunction(table *rt.Table, name string) (*rt.GoFunction, error) {
	return configFunctionValue(table.Get(rt.StringValue(name)), name)
}

func configFunctionValue(value rt.Value, name string) (*rt.GoFunction, error) {
	callable, ok := value.TryCallable()
	if !ok {
		return nil, fmt.Errorf("lua function %s is missing", name)
	}
	fn, ok := callable.(*rt.GoFunction)
	if !ok {
		return nil, fmt.Errorf("lua function %s is not a host function", name)
	}
	return fn, nil
}

func configModuleLoader(machine *rt.Runtime) (rt.Value, error) {
	chunk, err := machine.CompileAndLoadLuaChunk("config module loader", []byte(`
		local loadfile, error = loadfile, error
		return function(name, path)
			local module, problem = loadfile(path)
			if not module then error(problem) end
			return module(name, path)
		end
	`), rt.TableValue(machine.GlobalEnv()))
	if err != nil {
		return rt.NilValue, err
	}
	loader, err := rt.Call1(machine.MainThread(), rt.FunctionValue(chunk))
	if err != nil {
		return rt.NilValue, err
	}
	return loader, nil
}

func searchConfigPreload(t *rt.Thread, c *rt.GoCont, pkg *rt.Table) (rt.Cont, error) {
	if err := c.Check1Arg(); err != nil {
		return nil, err
	}
	name, err := c.StringArg(0)
	if err != nil {
		return nil, err
	}
	preload, ok := pkg.Get(rt.StringValue("preload")).TryTable()
	if !ok {
		return nil, fmt.Errorf("package.preload must be a table")
	}
	return c.PushingNext1(t.Runtime, preload.Get(rt.StringValue(name))), nil
}

func searchConfigModule(t *rt.Thread, c *rt.GoCont, pkg *rt.Table, loader rt.Value) (rt.Cont, error) {
	if err := c.Check1Arg(); err != nil {
		return nil, err
	}
	name, err := c.StringArg(0)
	if err != nil {
		return nil, err
	}
	path, ok := pkg.Get(rt.StringValue("path")).TryString()
	if !ok {
		return nil, fmt.Errorf("package.path must be a string")
	}
	dirSep, pathSep, placeholder := configSeparators(pkg)
	found := findConfigModule(t, name, path, ".", dirSep, pathSep, placeholder)
	if found == "" {
		message := fmt.Sprintf("no Lua file for package %q", name)
		t.RequireBytes(len(message))
		return c.PushingNext1(t.Runtime, rt.StringValue(message)), nil
	}
	return c.PushingNext(t.Runtime, loader, rt.StringValue(found)), nil
}

func searchConfigPath(t *rt.Thread, c *rt.GoCont, pkg *rt.Table) (rt.Cont, error) {
	if err := c.CheckNArgs(2); err != nil {
		return nil, err
	}
	name, err := c.StringArg(0)
	if err != nil {
		return nil, err
	}
	path, err := c.StringArg(1)
	if err != nil {
		return nil, err
	}
	dirSep, pathSep, placeholder := configSeparators(pkg)
	nameSep := "."
	if c.NArgs() >= 3 {
		nameSep, err = c.StringArg(2)
		if err != nil {
			return nil, err
		}
	}
	if c.NArgs() >= 4 {
		dirSep, err = c.StringArg(3)
		if err != nil {
			return nil, err
		}
	}
	found := findConfigModule(t, name, path, nameSep, dirSep, pathSep, placeholder)
	if found == "" {
		message := fmt.Sprintf("no file for %q", name)
		t.RequireBytes(len(message))
		return c.PushingNext(t.Runtime, rt.NilValue, rt.StringValue(message)), nil
	}
	return c.PushingNext1(t.Runtime, rt.StringValue(found)), nil
}

func configSeparators(pkg *rt.Table) (string, string, string) {
	dirSep, pathSep, placeholder := string(os.PathSeparator), ";", "?"
	value, ok := pkg.Get(rt.StringValue("config")).TryString()
	if !ok {
		return dirSep, pathSep, placeholder
	}
	parts := strings.SplitN(value, "\n", 4)
	if len(parts) > 0 && parts[0] != "" {
		dirSep = parts[0]
	}
	if len(parts) > 1 && parts[1] != "" {
		pathSep = parts[1]
	}
	if len(parts) > 2 && parts[2] != "" {
		placeholder = parts[2]
	}
	return dirSep, pathSep, placeholder
}

func findConfigModule(t *rt.Thread, name, path, nameSep, dirSep, pathSep, placeholder string) string {
	count := strings.Count(name, nameSep)
	nameBytes := uint64(len(name)) + uint64(count)*uint64(len(dirSep))
	nameBytes -= uint64(count * len(nameSep))
	t.RequireMem(nameBytes)
	t.RequireCPU(uint64(len(name)) + nameBytes + 1)
	moduleName := strings.ReplaceAll(name, nameSep, dirSep)
	defer t.ReleaseMem(nameBytes)

	for {
		template := path
		more := false
		if at := strings.Index(path, pathSep); at >= 0 {
			template, path, more = path[:at], path[at+len(pathSep):], true
		}
		count := strings.Count(template, placeholder)
		candidateBytes := uint64(len(template)) + uint64(count)*uint64(len(moduleName))
		candidateBytes -= uint64(count * len(placeholder))
		t.RequireCPU(candidateBytes + 1)
		t.RequireMem(candidateBytes)
		candidate := strings.ReplaceAll(template, placeholder, moduleName)
		file, err := os.Open(candidate)
		if file != nil {
			_ = file.Close()
		}
		if err == nil {
			return candidate
		}
		t.ReleaseMem(candidateBytes)
		if !more {
			return ""
		}
	}
}

// name works out what the namespaces declared as shared are called, now that the whole file has
// been read.
//
// The key this person signs with is one of the things a config may name, and the name of a shared
// namespace is derived from it. Doing this while the file was still running would name a namespace
// after whichever key happened to be in force at that line.
func (c *Config) name() error {
	if len(c.shares) == 0 {
		return nil
	}
	if c.UserKey != "" {
		user.Use(expand(c.UserKey))
	}

	key, err := user.Public()
	if err != nil {
		return fmt.Errorf("naming what this machine shares: %w", err)
	}
	creator := user.Text(key)

	for at, word := range c.shares {
		m, _, ok := c.Mounts.Lookup(at)
		if !ok || m.Path != at {
			continue
		}
		m.Shared = ns.Shared{Creator: creator, At: at, Nonce: word}
		if err := c.Mounts.Add(m); err != nil {
			return err
		}
	}
	return nil
}

// readSettings takes what the config assigned off the module table.
//
// A key the config never mentioned is left unset rather than read as zero, so it does not silently
// overwrite the environment with a blank.
func readSettings(cfg *Config, module *rt.Table) error {
	if name, ok, err := settingString(module, "name"); err != nil {
		return err
	} else if ok {
		cfg.Name, cfg.HasName = name, true
	}
	if open, ok, err := settingBool(module, "open_links"); err != nil {
		return err
	} else if ok {
		cfg.OpenLinks, cfg.HasOpenLinks = open, true
	}
	if list, ok, err := settingStrings(module, "bootstrap"); err != nil {
		return err
	} else if ok {
		cfg.Bootstrap = list
	}
	if on, ok, err := settingBool(module, "rendezvous"); err != nil {
		return err
	} else if ok {
		cfg.Rendezvous, cfg.HasRendezvous = on, true
	}
	if on, ok, err := settingBool(module, "direct"); err != nil {
		return err
	} else if ok {
		cfg.Direct, cfg.HasDirect = on, true
	}
	if list, ok, err := settingStrings(module, "relays"); err != nil {
		return err
	} else if ok {
		cfg.Relays = list
	}

	// A vault is one recipient or several. A bare string is the common case -- a key file beside
	// the config -- and writing it as a list of one is the sort of thing a config makes you do
	// once and resent afterwards.
	if key, ok, err := settingString(module, "user_key"); err != nil {
		return err
	} else if ok {
		cfg.UserKey = key
	}
	if command, ok, err := settingString(module, "user_sign"); err != nil {
		return err
	} else if ok {
		cfg.UserSign = command
	}
	vault := module.Get(rt.StringValue("vault"))
	if !vault.IsNil() {
		if one, ok := vault.TryString(); ok {
			cfg.Vault = []string{one}
		} else if list, ok, err := settingStrings(module, "vault"); err != nil {
			return err
		} else if ok {
			cfg.Vault = list
		}
	}
	return nil
}

func settingString(t *rt.Table, key string) (string, bool, error) {
	v := t.Get(rt.StringValue(key))
	if v.IsNil() {
		return "", false, nil
	}
	value, ok := v.TryString()
	if !ok {
		return "", false, fmt.Errorf("drop.%s must be a string", key)
	}
	return value, true, nil
}

func settingBool(t *rt.Table, key string) (bool, bool, error) {
	v := t.Get(rt.StringValue(key))
	if v.IsNil() {
		return false, false, nil
	}
	value, ok := v.TryBool()
	if !ok {
		return false, false, fmt.Errorf("drop.%s must be true or false", key)
	}
	return value, true, nil
}

func settingStrings(t *rt.Table, key string) ([]string, bool, error) {
	v := t.Get(rt.StringValue(key))
	if v.IsNil() {
		return nil, false, nil
	}
	list, ok := v.TryTable()
	if !ok {
		return nil, false, fmt.Errorf("drop.%s must be a list of strings", key)
	}

	var out []string
	for i := int64(1); ; i++ {
		item := list.Get(rt.IntValue(i))
		if item.IsNil() {
			return out, true, nil
		}
		value, ok := item.TryString()
		if !ok {
			return nil, false, fmt.Errorf("drop.%s item %d must be a string", key, i)
		}
		out = append(out, value)
	}
}

// preload puts the module in package.loaded, so `require("drop")` hands back the same table the
// global names rather than searching the filesystem for it.
func preload(machine *rt.Runtime, module *rt.Table) error {
	pkg, ok := machine.GlobalEnv().Get(rt.StringValue("package")).TryTable()
	if !ok {
		return fmt.Errorf("the package library is missing")
	}
	loaded, ok := pkg.Get(rt.StringValue("loaded")).TryTable()
	if !ok {
		return fmt.Errorf("package.loaded is missing")
	}

	loaded.Set(rt.StringValue("drop"), rt.TableValue(module))
	return nil
}

// register appends a handler to its list. Appending rather than replacing is what lets a config
// declare more than one for the same event.
func register(c *rt.GoCont, handlers *rt.Table, event string) (rt.Cont, error) {
	if err := c.Check1Arg(); err != nil {
		return nil, err
	}
	fn, err := c.CallableArg(0)
	if err != nil {
		return nil, err
	}

	list, ok := handlers.Get(rt.StringValue(event)).TryTable()
	if !ok {
		list = rt.NewTable()
		handlers.Set(rt.StringValue(event), rt.TableValue(list))
	}
	n := listLen(list, MaxHandlers)
	if n >= MaxHandlers {
		return nil, fmt.Errorf("drop.on.%s has more than %d handlers", event, MaxHandlers)
	}
	list.Set(rt.IntValue(int64(n+1)), rt.FunctionValue(fn))

	return c.Next(), nil
}

// listLen counts a Lua list, stopping at the first hole.
func listLen(t *rt.Table, most int) int {
	n := 0
	for n < most && !t.Get(rt.IntValue(int64(n+1))).IsNil() {
		n++
	}
	return n
}

// mount is `drop.mount("/path", { type = "...", ... })`.
//
// Nothing here reads a setting by name except the two the namespace layer owns: what kind of thing
// this is, and who may reach it. The rest of the table is handed to the archetype, which is the
// only thing that knows what any of those words mean.
func mount(c *rt.GoCont, cfg *Config) (rt.Cont, error) {
	if err := c.CheckNArgs(2); err != nil {
		return nil, err
	}
	path, err := c.StringArg(0)
	if err != nil {
		return nil, err
	}
	opts, ok := c.Arg(1).TryTable()
	if !ok {
		return nil, fmt.Errorf("drop.mount(%q): the second argument must be a table", path)
	}

	access := readAccess(opts)

	// A password written in plain is one a config leak hands over. Say so at load time rather than
	// letting it never match and look like a broken rule.
	if access.Password != "" && !passwd.Looks(access.Password) {
		return nil, fmt.Errorf("drop.mount(%q): the password must be a hash from `drop me passwd`, not the word itself", path)
	}

	m := ns.Mount{
		Path:      path,
		Archetype: fieldString(opts, "type"),
		Version:   fieldInt(opts, "version"),
		Access:    access,
	}

	word, wanted := sharedWord(opts)

	// A mount with no type is a branch: it serves nothing and exists to carry an access rule for
	// the paths under it. The table refuses one that is neither, so a typo is still caught.
	if !m.Branch() {
		answers, err := answering(cfg.known, m.Archetype, m.Version)
		if err != nil {
			return nil, fmt.Errorf("drop.mount(%q): %w", path, err)
		}
		settings, err := answers.Read(declared{opts})
		if err != nil {
			return nil, fmt.Errorf("drop.mount(%q): %w", path, err)
		}
		m.Config = settings

		if wanted && !answers.Note(settings).Shareable {
			return nil, fmt.Errorf("drop.mount(%q): a %s is one machine's own, so it cannot be shared", path, m.Archetype)
		}
	} else if !access.Declared() {
		return nil, fmt.Errorf("drop.mount(%q): needs a type, or an access rule if it is a branch", path)
	} else if wanted {
		return nil, fmt.Errorf("drop.mount(%q): a path that holds others and serves nothing has nothing to share", path)
	}

	if err := cfg.Mounts.Add(m); err != nil {
		return nil, fmt.Errorf("drop.mount(%q): %w", path, err)
	}

	// Named after the whole file has run rather than here, because who this machine signs as is
	// one of the things the file may say, and a name worked out before it was read would be a
	// different name from the one everybody else uses.
	if wanted {
		at, err := ns.Clean(path)
		if err != nil {
			return nil, fmt.Errorf("drop.mount(%q): %w", path, err)
		}
		if cfg.shares == nil {
			cfg.shares = map[string]string{}
		}
		cfg.shares[at] = word
	}
	return c.Next(), nil
}

// sharedWord reads whether a namespace is one several machines hold, and the word that tells it
// from another made at the same path.
//
//	shared = true       one thing at this path
//	shared = "second"   a different thing at the same path
func sharedWord(opts *rt.Table) (string, bool) {
	value := opts.Get(rt.StringValue("shared"))
	if value.IsNil() {
		return "", false
	}
	if word, ok := value.TryString(); ok {
		return word, word != ""
	}
	return "", rt.Truth(value)
}

// answering finds what a mount's type is, so a config that names one this build does not have is
// refused where it is written.
func answering(known *arch.Registry, name string, version int) (arch.Archetype, error) {
	if known == nil {
		return nil, fmt.Errorf("this build registered no namespace types")
	}
	answers, ok := known.Lookup(name, version)
	if !ok {
		return nil, known.Missing(name, version)
	}
	return answers, nil
}

// declared is one mount's table, read the way an archetype reads it: by name, and saying whether
// the config mentioned the setting at all, so "off" can be told from "unset".
type declared struct {
	t *rt.Table
}

// String reads a setting. A value beginning with ~ is a path somebody typed, and is resolved here:
// the archetype should not have to know that a config is written by a person.
func (d declared) String(key string) (string, bool) {
	s, ok := optString(d.t, key)
	return expand(s), ok
}

func (d declared) Bool(key string) (bool, bool) { return optBool(d.t, key) }

func (d declared) Strings(key string) ([]string, bool) { return optStrings(d.t, key) }

func fieldString(t *rt.Table, key string) string {
	s, _ := t.Get(rt.StringValue(key)).TryString()
	return s
}

// fieldInt reads a whole number a config wrote, which is nothing at all when it wrote none.
func fieldInt(t *rt.Table, key string) int {
	n, _ := t.Get(rt.StringValue(key)).TryInt()
	return int(n)
}

func fieldBool(t *rt.Table, key string) bool {
	b, _ := t.Get(rt.StringValue(key)).TryBool()
	return b
}

func fieldStrings(t *rt.Table, key string) []string {
	out, _ := optStrings(t, key)
	return out
}

// optString reads a setting, reporting whether the config mentioned it at all.
func optString(t *rt.Table, key string) (string, bool) {
	return t.Get(rt.StringValue(key)).TryString()
}

func optBool(t *rt.Table, key string) (bool, bool) {
	v := t.Get(rt.StringValue(key))
	if v.IsNil() {
		return false, false
	}
	return rt.Truth(v), true
}

func optStrings(t *rt.Table, key string) ([]string, bool) {
	list, ok := t.Get(rt.StringValue(key)).TryTable()
	if !ok {
		return nil, false
	}

	var out []string
	for i := int64(1); ; i++ {
		s, ok := list.Get(rt.IntValue(i)).TryString()
		if !ok {
			return out, true
		}
		out = append(out, s)
	}
}

// readAccess reads who a path is shared with.
//
// Three shorthands, because almost every path wants one of them: a list of names is a list of
// people and devices, the bare word "paired" is anyone in the address book, and the bare word
// "anyone" is exactly what it says. The long form is for a path that needs a key or a password.
func readAccess(opts *rt.Table) ns.Access {
	value := opts.Get(rt.StringValue("access"))

	if word, ok := value.TryString(); ok {
		return withVisible(opts, ns.Access{
			AnyPaired:  word == "paired",
			AnyTrusted: word == "trusted",
			Anyone:     word == "anyone",
		})
	}

	table, ok := value.TryTable()
	if !ok {
		return withVisible(opts, ns.Access{})
	}

	// A list of names, rather than a table of rules.
	if names := listOfStrings(table); len(names) > 0 {
		return withVisible(opts, ns.Access{Named: names})
	}

	out := ns.Access{
		Named:    fieldStrings(table, "paired"),
		Keys:     fieldStrings(table, "keys"),
		Password: fieldString(table, "password"),
		All:      fieldString(table, "require") == "all",
	}
	if word, ok := table.Get(rt.StringValue("paired")).TryString(); ok {
		switch word {
		case "paired":
			out.AnyPaired, out.Named = true, nil
		case "trusted":
			out.AnyTrusted, out.Named = true, nil
		}
	}
	out.AnyTrusted = out.AnyTrusted || fieldBool(table, "trusted")
	out.Anyone = fieldBool(table, "anyone")
	return withVisible(opts, out)
}

// withVisible reads who may see a path without being able to open it.
//
// Its own option rather than a rung inside access, because it is a different question: access says
// who gets in, visible says who is told there is a door. A path can have both -- shared with one
// person and merely visible to another, who then asks.
//
//	visible = "paired"        everybody in the address book
//	visible = { "carol" }     that person, and every machine of theirs
func withVisible(opts *rt.Table, out ns.Access) ns.Access {
	value := opts.Get(rt.StringValue("visible"))

	if word, ok := value.TryString(); ok {
		out.AnyVisible = word == "paired" || word == "anyone"
		out.TrustedVisible = word == "trusted"
		return out
	}
	if table, ok := value.TryTable(); ok {
		out.Visible = listOfStrings(table)
	}
	return out
}

// listOfStrings reads a table used as a list, and gives nothing back for one used as a map.
func listOfStrings(t *rt.Table) []string {
	var out []string
	for i := int64(1); ; i++ {
		item, ok := t.Get(rt.IntValue(i)).TryString()
		if !ok {
			return out
		}
		out = append(out, item)
	}
}
