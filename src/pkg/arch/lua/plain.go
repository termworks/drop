package lua

import (
	"fmt"
	"sort"

	rt "github.com/arnodel/golua/runtime"
)

// What a plugin makes of a declaration has to outlive the runtime that made it.
//
// It is kept on a mount and handed back for a session that has not started yet, in a runtime that
// does not exist yet, so it cannot stay a Lua value: those belong to the runtime they were made in
// and nowhere else. Strings, numbers, booleans and tables of those cross. A function, a thread or a
// userdata does not, because there would be nothing on the other side for it to be.

// deep bounds how far a nested table may go, and wide how many entries one table may hold.
const (
	deep = 16
	wide = 4096
)

// plain copies a Lua value out of the runtime that made it.
func plain(v rt.Value, at int) (any, error) {
	if at > deep {
		return nil, fmt.Errorf("a table nested more than %d deep", deep)
	}
	if v.IsNil() {
		return nil, nil
	}
	if b, ok := v.TryBool(); ok {
		return b, nil
	}
	if n, ok := v.TryInt(); ok {
		return n, nil
	}
	if f, ok := v.TryFloat(); ok {
		return f, nil
	}
	if s, ok := v.TryString(); ok {
		return s, nil
	}
	if t, ok := v.TryTable(); ok {
		return spread(t, at)
	}
	return nil, fmt.Errorf("a %s, which is nothing at all once the runtime that made it is gone", v.TypeName())
}

// spread copies a table: a list where the keys are 1 upwards, and a map of names otherwise.
func spread(t *rt.Table, at int) (any, error) {
	if n := t.Len(); n > 0 {
		if int64(count(t)) == n {
			return listed(t, n, at)
		}
	}
	return mapped(t, at)
}

func listed(t *rt.Table, n int64, at int) ([]any, error) {
	if n > wide {
		return nil, fmt.Errorf("a table of %d entries, over the %d one may hold", n, wide)
	}

	out := make([]any, 0, n)
	for i := int64(1); i <= n; i++ {
		item, err := plain(t.Get(rt.IntValue(i)), at+1)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, nil
}

func mapped(t *rt.Table, at int) (map[string]any, error) {
	out := map[string]any{}

	for key, value, ok := t.Next(rt.NilValue); ok && !key.IsNil(); key, value, ok = t.Next(key) {
		name, is := key.TryString()
		if !is {
			return nil, fmt.Errorf("a table keyed by a %s, and a setting is keyed by a name", key.TypeName())
		}
		if len(out) >= wide {
			return nil, fmt.Errorf("a table of more than %d entries", wide)
		}
		made, err := plain(value, at+1)
		if err != nil {
			return nil, err
		}
		out[name] = made
	}
	return out, nil
}

// count is how many entries a table holds, stopping once there are more than a table may.
func count(t *rt.Table) int {
	n := 0
	for key, _, ok := t.Next(rt.NilValue); ok && !key.IsNil() && n <= wide; key, _, ok = t.Next(key) {
		n++
	}
	return n
}

// value puts a plain value back into a runtime, which is where a plugin can see it again.
func value(v any) rt.Value {
	switch made := v.(type) {
	case nil:
		return rt.NilValue
	case bool:
		return rt.BoolValue(made)
	case int64:
		return rt.IntValue(made)
	case float64:
		return rt.FloatValue(made)
	case string:
		return rt.StringValue(made)
	case []any:
		t := rt.NewTable()
		for i, item := range made {
			t.Set(rt.IntValue(int64(i+1)), value(item))
		}
		return rt.TableValue(t)
	case map[string]any:
		t := rt.NewTable()
		for _, name := range keys(made) {
			t.Set(rt.StringValue(name), value(made[name]))
		}
		return rt.TableValue(t)
	}
	return rt.NilValue
}

// keys is a map's names in the order a person would read them, so that building a table twice
// builds the same one.
func keys(of map[string]any) []string {
	out := make([]string, 0, len(of))
	for name := range of {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}
