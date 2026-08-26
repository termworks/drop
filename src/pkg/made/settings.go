package made

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/bresilla/drop/src/pkg/arch"
)

// Settings is what an archetype will read out of a declaration.
//
// Three kinds and no more, because a declaration can hold exactly three: text, on or off, and a
// list of names. Anything else has nowhere to go — there is no accessor that would hand it over.
type Settings map[string]any

// settle normalises a decoded declaration and refuses anything a declaration cannot say.
//
// A JSON list decodes as a list of anything, and is turned into a list of names here so that
// nothing further down has to ask twice. A number is refused where it is written, naming the path
// and the key, rather than read as text and quietly meaning something else.
func (s Settings) settle(at string) error {
	for key, value := range s {
		switch v := value.(type) {
		case string, bool, []string:
		case []any:
			names := make([]string, 0, len(v))
			for _, item := range v {
				name, ok := item.(string)
				if !ok {
					return fmt.Errorf("%s: %s is a list holding %s, and a setting is text, on or off, or a list of names", at, key, whatIs(item))
				}
				names = append(names, name)
			}
			s[key] = names
		default:
			return fmt.Errorf("%s: %s is %s, and a setting is text, on or off, or a list of names", at, key, whatIs(value))
		}
	}
	return nil
}

func (s Settings) clone() Settings {
	if s == nil {
		return nil
	}

	out := make(Settings, len(s))
	for key, value := range s {
		if list, ok := value.([]string); ok {
			value = append([]string(nil), list...)
		}
		out[key] = value
	}
	return out
}

// whatIs names a value the way somebody reading their own file would.
func whatIs(value any) string {
	switch value.(type) {
	case nil:
		return "nothing"
	case float64:
		return "a number"
	case map[string]any:
		return "a table"
	case []any:
		return "a list"
	}
	return fmt.Sprintf("%T", value)
}

// Declared reads settings the way an archetype reads them.
//
// The same answers a config written in Lua gives, down to the two quirks: a value beginning with ~
// is a path a person typed and is resolved here, and on-or-off is truth rather than a type test —
// a setting that is there and is not false is on. A namespace that behaves one way in the config
// and another way here would be a divergence at the one boundary this design exists to keep clean.
func Declared(of Settings) arch.Declared { return declared{of} }

type declared struct{ of Settings }

func (d declared) String(key string) (string, bool) {
	text, ok := d.of[key].(string)
	if !ok {
		return "", false
	}
	return expand(text), true
}

func (d declared) Bool(key string) (bool, bool) {
	value, ok := d.of[key]
	if !ok || value == nil {
		return false, false
	}
	if off, ok := value.(bool); ok {
		return off, true
	}
	return true, true
}

func (d declared) Strings(key string) ([]string, bool) {
	list, ok := d.of[key].([]string)
	return list, ok
}

// expand resolves ~ in a setting, because a person typed it.
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
