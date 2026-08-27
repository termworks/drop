package lua

import (
	"fmt"
	shown "github.com/bresilla/drop/src/pkg/plain"
	"io"
	"os"
	"path/filepath"
	"sync"

	"github.com/arnodel/golua/code"
	"github.com/arnodel/golua/lib"
	"github.com/arnodel/golua/lib/base"
	"github.com/arnodel/golua/lib/mathlib"
	"github.com/arnodel/golua/lib/packagelib"
	"github.com/arnodel/golua/lib/stringlib"
	"github.com/arnodel/golua/lib/tablelib"
	"github.com/arnodel/golua/lib/utf8lib"
	rt "github.com/arnodel/golua/runtime"

	"github.com/bresilla/drop/src/pkg/arch"
)

// What one session of a plugin may spend.
//
// One budget for the whole session and not one per frame: a value a plugin makes before it waits is
// released after it, and a budget that came and went in between would be asked to give back memory
// it never took.
//
// Both are totals over the life of the session rather than a level it stays under, so a session is
// a thing with an end. Fifty million steps and sixty-four megabytes is a plugin working a frame at
// a time for as long as anybody would sit in front of one; a loop that never ends is half a second.
const (
	sessionSteps = 50_000_000
	sessionBytes = 64 << 20
)

// What a session spends on the work a host function does outside lua.
//
// The budget counts steps and bytes, and neither is what starting a process, opening a file or
// filling a disk actually costs — all three are one step to the runtime and none of them is memory
// the runtime gives back. Each is charged what it is worth in steps instead, so the one budget
// bounds all of it: a session gets about twelve hundred processes, fifty thousand opens and fifty
// megabytes written, or any mixture adding up to the same.
const (
	costRun   = 40_000
	costOpen  = 1_000
	costWrite = 1
)

// What working out a mount's settings and the line said about it may spend. Both run while a config
// is being read, where the answer is a fact about a file and not a conversation with anybody.
const (
	thinkingSteps = 2_000_000
	thinkingBytes = 8 << 20
)

// safe is what every host function declares. They are quick and they allocate nothing a budget
// cannot see. None of them claims to do no input or output, or to take no time, because some of
// them open files and start processes.
const safe = rt.ComplyCpuSafe | rt.ComplyMemSafe

// loading holds one library load at a time.
//
// Loading a library writes to values the library itself keeps — the compliance flags on two
// functions the base library holds — so two sessions starting at the same moment would be writing
// the same words at the same time.
var loading sync.Mutex

// allowed is everything a plugin's world is made of.
//
// Built by adding rather than by taking away. A sandbox made by removal is one forgotten entry away
// from being no sandbox at all, and the forgotten entry is always something nobody remembered was
// reachable. What is absent here is absent because it was never put in: io, os, debug, package,
// require, load, loadfile, dofile, print and the global table itself.
var allowed = []string{
	"assert", "error", "getmetatable", "ipairs", "next", "pairs", "pcall", "rawequal", "rawget",
	"rawlen", "rawset", "select", "setmetatable", "tonumber", "tostring", "type", "xpcall",
	"_VERSION", "string", "table", "math", "utf8",
}

// world is one runtime a plugin runs in, and what it declared while it was there.
//
// One per call and never shared: a runtime is not safe from two goroutines, and two sessions of one
// plugin that met in a runtime would be two conversations in one head.
type world struct {
	lua     *rt.Runtime
	release func()
	// env is the whole of what the chunk can see.
	env *rt.Table
	// said is every archetype the file declared, by the name it gave.
	said map[string]*rt.Table
	// order is those names as the file wrote them.
	order []string
	// from is the file, and who is what a line the plugin writes is attributed to.
	from string
	who  string
}

// newWorld builds a runtime with nothing in it but the allowlist and the drop table.
func newWorld(from, who string) *world {
	loading.Lock()
	machine := rt.New(io.Discard)
	release := lib.LoadLibs(machine,
		base.LibLoader,
		packagelib.LibLoader,
		stringlib.LibLoader,
		tablelib.LibLoader,
		mathlib.LibLoader,
		utf8lib.LibLoader,
	)
	loading.Unlock()

	w := &world{lua: machine, release: release, env: rt.NewTable(), said: map[string]*rt.Table{}, from: from, who: who}

	global := machine.GlobalEnv()
	for _, name := range allowed {
		machine.SetEnv(w.env, name, global.Get(rt.StringValue(name)))
	}

	drop := rt.NewTable()
	machine.SetEnv(w.env, "drop", rt.TableValue(drop))
	machine.SetEnvGoFunc(drop, "archetype", guarded("drop.archetype", w.archetype), 1, false).
		SolemnlyDeclareCompliance(safe)
	machine.SetEnvGoFunc(drop, "log", guarded("drop.log", w.log), 1, true).
		SolemnlyDeclareCompliance(safe)

	return w
}

func (w *world) close() {
	if w.release != nil {
		w.release()
		w.release = nil
	}
	w.lua = nil
}

// within runs the file and then f, both inside one budget.
//
// A raise comes back as an error, and so does a budget spent: golua ends a chunk that has run out
// by panicking, and the call this is wrapped in catches that. Anything else that panics is caught
// here, because a plugin is one namespace and taking the daemon down with it is not one of the
// things it is allowed to do.
func (w *world) within(unit *code.Unit, limits rt.RuntimeResources, f func() error) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("%s: %v", w.from, r)
		}
	}()

	_, err = w.lua.MainThread().CallContext(rt.RuntimeContextDef{HardLimits: limits}, func() error {
		chunk := w.lua.LoadLuaUnit(unit, rt.TableValue(w.env))
		if _, err := rt.Call1(w.lua.MainThread(), rt.FunctionValue(chunk)); err != nil {
			return err
		}
		return f()
	})
	if err != nil {
		// Said rather than wrapped. A raise is a lua error, and anything holding one that goes back
		// into lua is unwrapped again the moment it arrives — with everything said around it on the
		// way thrown away, which is the file name and the mount that this is about.
		return fmt.Errorf("%s: %s", w.from, err)
	}
	return nil
}

// declared is what one archetype in this file said about itself.
func (w *world) declared(name string) (*rt.Table, error) {
	said, ok := w.said[name]
	if !ok {
		return nil, fmt.Errorf("%s no longer declares %q", w.from, name)
	}
	return said, nil
}

// archetype is `drop.archetype{ name = ..., read = ..., note = ..., serve = ... }`.
//
// Registration rather than a returned value, the way a config registers a handler: a file may
// declare as many archetypes as it likes and still be a file that says what it does and returns
// nothing.
func (w *world) archetype(t *rt.Thread, c *rt.GoCont) (rt.Cont, error) {
	if err := c.CheckNArgs(1); err != nil {
		return nil, err
	}
	said, err := c.TableArg(0)
	if err != nil {
		return nil, err
	}

	name, _ := said.Get(rt.StringValue("name")).TryString()
	if name == "" {
		return nil, fmt.Errorf("an archetype needs a name")
	}
	if _, again := w.said[name]; again {
		return nil, fmt.Errorf("%q is declared twice in one file", name)
	}
	for _, needed := range []string{"read", "note", "serve"} {
		if _, ok := said.Get(rt.StringValue(needed)).TryCallable(); !ok {
			return nil, fmt.Errorf("the archetype %q needs a %s function", name, needed)
		}
	}

	w.said[name] = said
	w.order = append(w.order, name)
	return c.Next(), nil
}

// log is `drop.log(text)`: one line in the daemon's output, said by the plugin and marked as such.
func (w *world) log(t *rt.Thread, c *rt.GoCont) (rt.Cont, error) {
	text := ""
	for i := range c.NArgs() {
		s, _ := c.Arg(i).TryString()
		if i > 0 {
			text += " "
		}
		text += s
	}

	// Charged like any other writing, and cut like any other message.
	//
	// A plugin runs under a quota it cannot exceed, and this was the one way out: writing cost it
	// nothing, so a loop that only logged could turn a bounded plugin into as much of somebody's
	// terminal, and somebody's disk if the log is kept, as it cared to produce.
	if len(text) > MaxSaid {
		text = text[:MaxSaid]
	}
	t.RequireCPU(costWrite * uint64(len(text)))
	t.RequireBytes(len(text))

	fmt.Fprintf(os.Stderr, "drop: %s: %s\n", w.who, shown.Text(text, MaxSaid))
	return c.Next(), nil
}

// guarded turns a panic in a host function into an error the plugin sees.
//
// A quota spent is let through: that one is not ours to catch, and the driver on the other side of
// the yield is waiting for exactly it. Everything else stops here, because a host function that
// panics on a coroutine's own goroutine panics where nobody can reach it.
func guarded(name string, f rt.GoFunctionFunc) rt.GoFunctionFunc {
	return func(t *rt.Thread, c *rt.GoCont) (next rt.Cont, err error) {
		defer func() {
			r := recover()
			if r == nil {
				return
			}
			if _, spent := r.(rt.ContextTerminationError); spent {
				panic(r)
			}
			next, err = nil, fmt.Errorf("%s: %v", name, r)
		}()
		return f(t, c)
	}
}

// asked is the declaration as a plugin reads it: a name gives back what the config wrote there, and
// nothing at all for a setting it never wrote.
func asked(machine *rt.Runtime, d arch.Declared) rt.Value {
	meta := rt.NewTable()
	machine.SetEnvGoFunc(meta, "__index", guarded("the declaration", func(t *rt.Thread, c *rt.GoCont) (rt.Cont, error) {
		key, err := c.StringArg(1)
		if err != nil {
			return nil, err
		}
		return c.PushingNext1(t.Runtime, setting(d, key)), nil
	}), 2, false).SolemnlyDeclareCompliance(safe)

	t := rt.NewTable()
	t.SetMetatable(meta)
	return rt.TableValue(t)
}

// setting is one thing a config wrote, as the nearest Lua value to it: a word, a list of words, or
// a flag.
func setting(d arch.Declared, key string) rt.Value {
	if word, ok := d.String(key); ok {
		return rt.StringValue(word)
	}
	if list, ok := d.Strings(key); ok {
		t := rt.NewTable()
		for i, item := range list {
			t.Set(rt.IntValue(int64(i+1)), rt.StringValue(item))
		}
		return rt.TableValue(t)
	}
	if on, ok := d.Bool(key); ok {
		return rt.BoolValue(on)
	}
	return rt.NilValue
}

// named is what a file is called without the directory or the suffix, which is what a line from a
// plugin is attributed to before it has said what it is.
func named(file string) string {
	base := filepath.Base(file)
	return base[:len(base)-len(filepath.Ext(base))]
}
