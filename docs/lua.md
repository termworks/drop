# An archetype in Lua

An archetype written in Lua is loaded by both ends. Nothing is compiled into drop, nothing is
shipped, and a machine that has the file serves the thing while a machine that does not opens it as
whatever it says it *speaks like*.

That last part is the whole reason this works between two people. Without it, a namespace of a type
you have never heard of is a namespace you cannot open at all.

```lua
-- ~/.config/drop/archetypes/camera.lua
drop.archetype{
  name    = "camera",
  version = 1,
  shape   = "note",     -- what a machine without this file opens it as

  read = function(d)
    if not d.device then error("a camera namespace needs a device") end
    return { device = d.device, snap = d.snap }
  end,

  note = function(c)
    return { detail = c.device, about = "a camera, as it sees things", glyph = "◉" }
  end,

  serve = function(s, c)
    s:write("a camera pointed at " .. c.device .. "\n")
    while true do
      local asked = s:read()
      if not asked then return end
      if asked ~= "still" then
        s:write("this camera takes the word still, and nothing else\n")
      else
        s:write(taken(s, c), "data")
      end
    end
  end,
}
```

```lua
-- init.lua
drop.mount("/film", { type = "camera", device = "/dev/video0",
                      snap = "ffmpeg -f v4l2 -i /dev/video0 -frames:v 1 -y", access = "paired" })
```

Files in the archetypes directory beside `init.lua` are loaded at startup and registered into the
same registry the built-in ones use. A plugin is an archetype in every way that matters — it appears
in `drop path create`, it has a glyph in listings, and `drop connect` opens it.

## The four functions

| | |
|---|---|
| `read(declared)` | turn a declaration into settings, or `error()` — called once, when the config is read |
| `note(config)` | what may be said about one without knowing what it is: `detail`, `about`, `glyph`, `writable`, `shareable` |
| `serve(session, config)` | answer one session; returning ends it |
| `name`, `version`, `shape` | what it is called, which revision, and what it falls back to |

`shape` names another archetype whose protocol this one speaks. A `camera` with `shape = "note"`
writes a line about itself first, because a note is answered before it has asked for anything — so a
machine without the plugin gets something sensible instead of a hang.

## The session

```lua
s:read()                  -- the next thing the far end said, or nil when it is over
s:write(body)             -- send text
s:write(body, "data")     -- send bytes
s:who()                   -- { name, person, id, paired, trusted }
s:path()                  -- which namespace this is
s:mine(name)              -- a path in this namespace's own directory
s:open(name)              -- open a file there: :read() :write() :close()
s:run{ "sh", "-c", cmd }  -- run a program, get its output
drop.log(text)            -- a line in the daemon log
```

`s:mine` is worth a sentence. It gives a path in a directory belonging to the *namespace*, and
`s:open` will not leave it. In the camera example the still is written to `s:mine("still.jpg")`
rather than a fixed path, because two people asking for a still at the same moment would otherwise
run their commands into one file and each read back the other's half of it.

## The sandbox

A plugin is somebody's code running in your daemon. Two things bound it.

**What it can reach** is an allowlist `_ENV`. The plugin gets `pairs`, `ipairs`, `next`, `rawget`,
`rawlen`, `rawequal`, `pcall`, `assert`, `error`, `getmetatable`, `string`, `table`, `math`, and the
`drop` table — and nothing else. No `io`, no `os`, no `package`, no `require`, no `load`, no
`debug`, no `dofile`.

This is a structural allowlist and not the interpreter's compliance flags, and the difference
matters: under the flags alone `io.open` is refused while **`io.popen` still runs a shell**, and
`os.getenv` still reads your environment. A test tries thirty-odd escapes — reaching a real `_ENV`
through a metatable, through a string's metatable, through `getmetatable`, through a coroutine —
and each has to come back empty.

**What it can spend** is a quota: CPU, memory and wall-clock, all charged. Writing was the one way
out for a while — a loop that only logged cost nothing — so `drop.log` is charged like any other
write, bounded, and passed through the same sanitiser everything printed goes through.

A program started with `s:run` gets a process group of its own and the group is killed when the
session ends, so a plugin cannot leave something behind holding your machine.

Each session gets a runtime of its own. The interpreter's runtime is not safe to share between
goroutines — seven out of eight panicked when tried — so they are not shared. The compiled unit is,
which is the cheap part: loading one takes about 380 nanoseconds against about 35 microseconds to
build a runtime.

## What it cannot do

Both ends need the file. That is the design, not a limitation to work around: two machines share a
plugin in order to understand each other, and one that does not have it says so plainly and falls
back to `shape`.

A plugin cannot open a network connection, read a file outside its own directory, or see your
environment. If it needs something from the machine, it runs a program with `s:run` and that program
is subject to the same session lifetime.

## Where it lives

`src/pkg/arch/lua/` — `load.go` finds and compiles plugins, `world.go` builds the sandboxed `_ENV`,
`session.go` is what a plugin can do to a session, `escape_test.go` is the attempts to get out.
[`misc/archetypes/camera.lua`](../misc/archetypes/camera.lua) is the worked example.
