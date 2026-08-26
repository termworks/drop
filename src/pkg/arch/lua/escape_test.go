package lua

import (
	"strings"
	"testing"
)

// Naming a thing that is absent is not the same as trying to climb out to it.
//
// These are the routes a Lua sandbox is usually broken by: a metatable that leads somewhere, a
// function asked for its own environment, bytecode dumped and loaded back, and the coroutine the
// session is already running on. None of them arrives anywhere, and the last one matters most —
// serve runs as a coroutine, and reaching the one underneath would be reaching the driver.
func TestAPluginCannotClimbOut(t *testing.T) {
	climbs := map[string]string{
		"coroutine underneath":  `return tostring(coroutine)`,
		"the metatable of _ENV": `return tostring(getmetatable(_ENV))`,
		"rawget past the world": `return tostring(rawget(_ENV, "os")) .. tostring(rawget(_ENV, "io"))`,
		"a function's world":    `return tostring(getmetatable(function() end))`,
		"anything that loads":   `return tostring(load) .. tostring(loadfile) .. tostring(dofile) .. tostring(require)`,
		"the loaded table":      `return tostring(_ENV._LOADED) .. tostring(_ENV._PRELOAD) .. tostring(_ENV.package)`,
	}

	for what, climb := range climbs {
		p := written(t, `
			drop.archetype{
				name  = "prober",
				read  = function(d) return {} end,
				note  = function(c) return {} end,
				serve = function(s, c)
					local ok, got = pcall(function() `+climb+` end)
					s:write(tostring(ok) .. "|" .. tostring(got))
				end,
			}
		`)

		conn, client, done := opened(t, p, nil)
		_, said := said(t, conn)
		client.Close()
		<-done

		got := string(said)
		if !strings.HasPrefix(got, "true|") {
			t.Errorf("%s: the attempt itself failed, so it proves nothing: %s", what, got)
			continue
		}
		// Every one of these must arrive at nothing. A table or a function is a way out.
		if rest := strings.TrimPrefix(got, "true|"); strings.Trim(rest, "nil") != "" {
			t.Errorf("%s reached %s", what, rest)
		}
	}
}
