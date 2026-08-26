// Package lua is an archetype somebody wrote rather than one drop was built with.
//
// A file beside the config declares a name, what its settings mean, what may be said about one of
// its namespaces, and what happens when somebody opens one. The registry does not know the
// difference: a namespace of a kind invented this morning is looked up, described and served
// through the same interface as a chat, and nothing in the namespace layer, the config reader or
// the wire gains a word.
//
//	drop.archetype{
//	  name  = "camera",
//	  shape = "note",
//	  read  = function(d) return { device = d.device } end,
//	  note  = function(c) return { detail = c.device, glyph = "◉" } end,
//	  serve = function(s, c) s:write("looking at " .. c.device) end,
//	}
//
// A plugin is first-party code — the person running drop wrote it — so it may open files and start
// processes. What it may not do is decide which files: a name it gives is resolved inside a
// directory this process holds open, and it never sees or supplies a path outside it.
package lua

import (
	"context"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/arnodel/golua/code"
	rt "github.com/arnodel/golua/runtime"
	"lukechampine.com/blake3"

	"github.com/bresilla/drop/src/pkg/arch"
)

// Plugin is one archetype a Lua file declared.
//
// Everything settled once is settled when the file is read: the chunk is compiled, and the name,
// the version and the shape are taken off the declaration. Everything else runs later, in a runtime
// of its own — a runtime is not safe from two goroutines, and two sessions of one plugin that met
// in one would be two conversations in one head.
type Plugin struct {
	// file is where this was written, so a mistake in it is reported with somewhere to look.
	file    string
	name    string
	version int
	shape   string
	// unit is the compiled chunk, loaded fresh into every runtime this archetype runs in.
	unit *code.Unit
	// keeps is where namespaces of this archetype keep their own files.
	keeps string
}

func (p *Plugin) Name() string { return p.name }
func (p *Plugin) Version() int { return p.version }

// Read hands the declaration to the plugin's own read and keeps what it makes of it.
//
// What comes back is copied out of the runtime that made it. It is kept on a mount and given to a
// session that has not started yet, in a runtime that does not exist yet, so it cannot stay a Lua
// value — and a plugin that tries to keep a function in its settings is told so here, where a
// config is being read, rather than the first time somebody opens the path.
func (p *Plugin) Read(d arch.Declared) (arch.Config, error) {
	w := newWorld(p.file, p.name)
	defer w.close()

	var settings arch.Config
	err := w.within(p.unit, rt.RuntimeResources{Cpu: thinkingSteps, Memory: thinkingBytes}, func() error {
		read, err := w.taking(p.name, "read")
		if err != nil {
			return err
		}
		made, err := rt.Call1(w.lua.MainThread(), read, asked(w.lua, d))
		if err != nil {
			return err
		}
		settings, err = plain(made, 0)
		if err != nil {
			return fmt.Errorf("read gave back %s", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return settings, nil
}

// Note is what the plugin says may be said about one of its namespaces.
//
// A plugin whose note raises still gets a line, because this is the one thing about a namespace
// that has to be answerable: a listing with a hole in it is worse than a listing that says the
// least it can.
func (p *Plugin) Note(c arch.Config) arch.Note {
	said := arch.Note{Shape: p.shape, Glyph: "·", About: "a " + p.name}

	w := newWorld(p.file, p.name)
	defer w.close()

	_ = w.within(p.unit, rt.RuntimeResources{Cpu: thinkingSteps, Memory: thinkingBytes}, func() error {
		note, err := w.taking(p.name, "note")
		if err != nil {
			return err
		}
		made, err := rt.Call1(w.lua.MainThread(), note, value(c))
		if err != nil {
			return err
		}
		t, ok := made.TryTable()
		if !ok {
			return fmt.Errorf("note gave back a %s and not a table", made.TypeName())
		}
		said = telling(t, said)
		return nil
	})
	return said
}

// Serve answers one session, with the plugin's serve running as a coroutine this drives.
func (p *Plugin) Serve(ctx context.Context, at arch.Session) error {
	w := newWorld(p.file, p.name)
	defer w.close()

	s := &session{ctx: ctx, at: at, where: filepath.Join(p.keeps, p.name, slug(at.Path))}
	defer s.shut()

	return w.within(p.unit, rt.RuntimeResources{Cpu: sessionSteps, Memory: sessionBytes}, func() error {
		serve, err := w.taking(p.name, "serve")
		if err != nil {
			return err
		}
		fn, _ := serve.TryCallable()
		return s.drive(w, fn)
	})
}

// taking is one of the three functions an archetype declared.
func (w *world) taking(name, what string) (rt.Value, error) {
	said, err := w.declared(name)
	if err != nil {
		return rt.NilValue, err
	}
	fn := said.Get(rt.StringValue(what))
	if _, ok := fn.TryCallable(); !ok {
		return rt.NilValue, fmt.Errorf("the archetype %q has no %s function", name, what)
	}
	return fn, nil
}

// telling reads what a plugin said about one of its namespaces, leaving alone whatever it did not
// mention.
func telling(t *rt.Table, said arch.Note) arch.Note {
	if detail, ok := t.Get(rt.StringValue("detail")).TryString(); ok {
		said.Detail = detail
	}
	if about, ok := t.Get(rt.StringValue("about")).TryString(); ok {
		said.About = about
	}
	if glyph, ok := t.Get(rt.StringValue("glyph")).TryString(); ok && glyph != "" {
		said.Glyph = glyph
	}
	said.Writable = rt.Truth(t.Get(rt.StringValue("writable")))
	said.Shareable = rt.Truth(t.Get(rt.StringValue("shareable")))
	return said
}

// slug is a namespace path as a directory name, so that two namespaces of one archetype keep their
// files apart.
//
// Readable enough to recognise, and ending in a digest of the path, because a path is a thousand
// arbitrary bytes and two of them that read the same afterwards would be two namespaces quietly
// sharing one directory.
func slug(path string) string {
	sum := blake3.Sum256([]byte(path))

	out := make([]rune, 0, len(path)+9)
	for _, c := range strings.Trim(path, "/") {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-', c == '_':
			out = append(out, c)
		default:
			out = append(out, '-')
		}
	}
	return string(out) + "-" + hex.EncodeToString(sum[:4])
}
