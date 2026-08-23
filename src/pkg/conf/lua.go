package conf

import (
	"fmt"
	"os"
	"sync"

	"github.com/arnodel/golua/lib"
	rt "github.com/arnodel/golua/runtime"

	"github.com/bresilla/drop/src/pkg/ns"
	"github.com/bresilla/drop/src/pkg/passwd"
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

	for i := int64(1); ; i++ {
		fn := list.Get(rt.IntValue(i))
		if fn.IsNil() {
			return
		}
		if _, err := rt.Call1(r.lua.MainThread(), fn, arg); err != nil {
			fmt.Fprintf(os.Stderr, "drop: on.%s handler #%d: %v\n", event, i, err)
		}
	}
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
	source, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reading %s: %w", path, err)
	}

	// The config's own print goes to stderr, so it cannot be mistaken for the output of whatever
	// command is running.
	machine := rt.New(os.Stderr)
	state := &runtime{lua: machine, release: lib.LoadAll(machine)}
	cfg.rt = state

	fail := func(err error) error {
		state.close()
		cfg.rt = nil
		return fmt.Errorf("%s: %w", path, err)
	}

	module := rt.NewTable()
	machine.SetEnv(machine.GlobalEnv(), "drop", rt.TableValue(module))
	machine.SetEnvGoFunc(module, "mount", func(t *rt.Thread, c *rt.GoCont) (rt.Cont, error) {
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
	machine.SetEnvGoFunc(on, "message", func(t *rt.Thread, c *rt.GoCont) (rt.Cont, error) {
		return register(c, handlers, "message")
	}, 1, false)
	machine.SetEnvGoFunc(on, "file", func(t *rt.Thread, c *rt.GoCont) (rt.Cont, error) {
		return register(c, handlers, "file")
	}, 1, false)

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
	if _, err := rt.Call1(machine.MainThread(), rt.FunctionValue(chunk)); err != nil {
		return fail(err)
	}

	readSettings(cfg, module)
	return nil
}

// readSettings takes what the config assigned off the module table.
//
// A key the config never mentioned is left unset rather than read as zero, so it does not silently
// overwrite the environment with a blank.
func readSettings(cfg *Config, module *rt.Table) {
	if name, ok := optString(module, "name"); ok {
		cfg.Name, cfg.HasName = name, true
	}
	if open, ok := optBool(module, "open_links"); ok {
		cfg.OpenLinks, cfg.HasOpenLinks = open, true
	}
	if list, ok := optStrings(module, "bootstrap"); ok {
		cfg.Bootstrap = list
	}
	if on, ok := optBool(module, "rendezvous"); ok {
		cfg.Rendezvous, cfg.HasRendezvous = on, true
	}
	if list, ok := optStrings(module, "relays"); ok {
		cfg.Relays = list
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
	list.Set(rt.IntValue(int64(listLen(list)+1)), rt.FunctionValue(fn))

	return c.Next(), nil
}

// listLen counts a Lua list, stopping at the first hole.
func listLen(t *rt.Table) int {
	n := 0
	for !t.Get(rt.IntValue(int64(n + 1))).IsNil() {
		n++
	}
	return n
}

// mount is `drop.mount("/path", { type = "...", ... })`.
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

	// A mount with no type is a branch: it serves nothing and exists to carry an access rule for the
	// paths under it. The table refuses one that is neither, so a typo is still caught.
	var kind ns.Kind
	if typeName := fieldString(opts, "type"); typeName != "" {
		parsed, err := ns.ParseKind(typeName)
		if err != nil {
			return nil, fmt.Errorf("drop.mount(%q): %w", path, err)
		}
		kind = parsed
	} else if !access.Declared() {
		return nil, fmt.Errorf("drop.mount(%q): needs a type, or an access rule if it is a branch", path)
	}

	// A password written in plain is one a config leak hands over. Say so at load time rather than
	// letting it never match and look like a broken rule.
	if access.Password != "" && !passwd.Looks(access.Password) {
		return nil, fmt.Errorf("drop.mount(%q): the password must be a hash from `drop passwd`, not the word itself", path)
	}

	m := ns.Mount{
		Path:    path,
		Kind:    kind,
		Dir:     expand(fieldString(opts, "dir")),
		Command: fieldString(opts, "command"),
		Shell:   fieldString(opts, "shell"),
		Input:   fieldBool(opts, "input"),
		Action:  fieldString(opts, "action"),
		Access:  access,
	}

	if err := check(m); err != nil {
		return nil, fmt.Errorf("drop.mount(%q): %w", path, err)
	}
	if err := cfg.Mounts.Add(m); err != nil {
		return nil, fmt.Errorf("drop.mount(%q): %w", path, err)
	}
	return c.Next(), nil
}

// check catches a namespace that cannot work at load time, where the error names the file and the
// line, rather than when somebody opens it and gets silence.
func check(m ns.Mount) error {
	switch m.Kind {
	case ns.KindFiles:
		if m.Dir == "" {
			return fmt.Errorf("a files namespace needs a dir")
		}
	case ns.KindStream:
		if m.Command == "" {
			return fmt.Errorf("a stream namespace needs a command")
		}
	}
	return nil
}

func fieldString(t *rt.Table, key string) string {
	s, _ := t.Get(rt.StringValue(key)).TryString()
	return s
}

func fieldBool(t *rt.Table, key string) bool {
	return rt.Truth(t.Get(rt.StringValue(key)))
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
// Two shorthands, because almost every path wants one of them: a list of names is a list of paired
// devices, and the bare word "paired" is anyone in the address book. The long form is for a path
// that needs a key or a password.
func readAccess(opts *rt.Table) ns.Access {
	value := opts.Get(rt.StringValue("access"))

	if word, ok := value.TryString(); ok {
		return ns.Access{AnyPaired: word == "paired"}
	}

	table, ok := value.TryTable()
	if !ok {
		return ns.Access{}
	}

	// A list of names, rather than a table of rules.
	if names := listOfStrings(table); len(names) > 0 {
		return ns.Access{Named: names}
	}

	out := ns.Access{
		Named:    fieldStrings(table, "paired"),
		Keys:     fieldStrings(table, "keys"),
		Password: fieldString(table, "password"),
		All:      fieldString(table, "require") == "all",
	}
	if word, ok := table.Get(rt.StringValue("paired")).TryString(); ok && word == "paired" {
		out.AnyPaired = true
		out.Named = nil
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
