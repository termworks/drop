package lua

import (
	"fmt"
	"os"
	"path/filepath"

	rt "github.com/arnodel/golua/runtime"

	"github.com/bresilla/drop/src/pkg/arch"
	"github.com/bresilla/drop/src/pkg/convo"
)

// Beside is the directory a config keeps its own archetypes in, next to init.lua.
const Beside = "archetypes"

// Load compiles every archetype written beside a config and registers it.
//
// A file that will not compile is an error naming the file and the line, said here, while the
// config is being read — the same place and the same moment a mount that names a setting wrongly is
// refused. A directory that is not there is not a mistake: most machines have no archetypes of
// their own.
func Load(dir string, into *arch.Registry) error {
	if into == nil {
		return nil
	}

	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("reading %s: %w", dir, err)
	}

	// Nowhere to keep files is not a reason to refuse to load: a plugin that never asks for one
	// works, and one that does is told where it stands when it asks.
	keeps, _ := keeping()

	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".lua" {
			continue
		}
		written, err := compile(filepath.Join(dir, entry.Name()), keeps)
		if err != nil {
			return err
		}
		for _, p := range written {
			if err := spare(into, p); err != nil {
				return err
			}
			into.Register(p)
		}
	}
	return nil
}

// compile reads one file, compiles it once, and runs it to find out what it declares.
//
// Once, and here: a chunk loads into a runtime in less time than it takes to read this sentence,
// but compiling it is where a syntax error still has a file and a line attached to it.
func compile(file, keeps string) ([]*Plugin, error) {
	source, err := os.ReadFile(file)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", file, err)
	}

	w := newWorld(file, named(file))
	defer w.close()

	unit, _, err := w.lua.CompileLuaChunk(file, source)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", file, err)
	}
	limits := rt.RuntimeResources{Cpu: thinkingSteps, Memory: thinkingBytes}
	if err := w.within(unit, limits, func() error { return nil }); err != nil {
		return nil, err
	}
	if len(w.order) == 0 {
		return nil, fmt.Errorf("%s declares no archetype", file)
	}

	out := make([]*Plugin, 0, len(w.order))
	for _, name := range w.order {
		if !plainly(name) {
			return nil, fmt.Errorf("%s: %q is not a name for a kind of namespace: letters, digits, - and _", file, name)
		}
		said := w.said[name]

		version := 1
		if n, ok := said.Get(rt.StringValue("version")).TryInt(); ok && n > 0 {
			version = int(n)
		}
		shape, _ := said.Get(rt.StringValue("shape")).TryString()

		out = append(out, &Plugin{file: file, name: name, version: version, shape: shape, unit: unit, keeps: keeps})
	}
	return out, nil
}

// spare refuses a plugin that would take the name of an archetype this build was made with.
//
// Registering replaces, so a file called chat.lua declaring a chat would quietly become the chat
// everybody's messages go to. Another plugin under the same name is a config read twice and is
// left alone.
func spare(into *arch.Registry, p *Plugin) error {
	was, taken := into.Lookup(p.name, p.version)
	if !taken {
		return nil
	}
	if _, written := was.(*Plugin); written {
		return nil
	}
	return fmt.Errorf("%s: %s is what this build already calls a kind of namespace", p.file, p.name)
}

// keeping is where namespaces of archetypes written in lua keep their files: beside the
// conversations, because what a namespace holds is data and not settings.
func keeping() (string, error) {
	base, err := convo.DataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, Beside), nil
}

// plainly says whether a name is one a path, a listing and a directory can all hold.
func plainly(name string) bool {
	if name == "" {
		return false
	}
	for _, c := range name {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-', c == '_':
		default:
			return false
		}
	}
	return true
}
