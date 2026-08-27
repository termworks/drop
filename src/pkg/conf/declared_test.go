package conf

import (
	"context"
	"fmt"
	"testing"

	"github.com/bresilla/drop/src/pkg/arch"
	"github.com/bresilla/drop/src/pkg/made"
)

// One namespace must not behave differently depending on which file it was declared in. There are
// two implementations of the same three accessors — the Lua table a config wrote, and the settings
// a command wrote down — so both are asked the same questions and the answers are compared.
//
// The quirks are the point. A leading ~ is a path a person typed and is resolved. On or off is
// truth rather than a type test: a setting that is there and is not false is on, which is why the
// word "true" as text and the flag true are different things.

// asked is everything a declaration says about one key, in all three ways of asking.
func asked(d arch.Declared, key string) string {
	text, said := d.String(key)
	on, mentioned := d.Bool(key)
	list, listed := d.Strings(key)
	return fmt.Sprintf("%s | %q %v | %v %v | %v %v", key, text, said, on, mentioned, list, listed)
}

// probe is an archetype that serves nothing and remembers what it was handed.
type probe struct {
	keys []string
	said *[]string
}

func (probe) Name() string { return "probe" }
func (probe) Version() int { return 1 }

func (p probe) Read(d arch.Declared) (arch.Config, error) {
	for _, key := range p.keys {
		*p.said = append(*p.said, asked(d, key))
	}
	return nil, nil
}

func (probe) Note(arch.Config) arch.Note                { return arch.Note{} }
func (probe) Serve(context.Context, arch.Session) error { return nil }

func TestADeclarationReadsTheSameInLuaAndInAFile(t *testing.T) {
	keys := []string{"dir", "writable", "off", "word", "hide", "empty", "missing"}

	var fromLua []string
	registry := arch.NewRegistry()
	registry.Register(probe{keys: keys, said: &fromLua})

	write(t, `
		local drop = require("drop")
		drop.mount("/probe", {
			type = "probe",
			access = "paired",
			dir = "~/notes",
			writable = true,
			off = false,
			word = "true",
			hide = { "a", "b" },
			empty = {},
		})
	`)
	cfg, err := Load(registry)
	if err != nil {
		t.Fatalf("Load(): %v", err)
	}
	defer cfg.Close()

	settings := made.Settings{
		"dir":      "~/notes",
		"writable": true,
		"off":      false,
		"word":     "true",
		"hide":     []string{"a", "b"},
		"empty":    []string{},
	}

	from := made.Declared(settings)
	for i, key := range keys {
		got := asked(from, key)
		if got != fromLua[i] {
			t.Errorf("%s\n  lua:  %s\n  file: %s", key, fromLua[i], got)
		}
	}
}
