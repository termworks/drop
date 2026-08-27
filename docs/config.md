# Configuration

`$XDG_CONFIG_HOME/drop/init.lua`, which is Lua and not a data format. It assigns settings, registers
namespaces, and returns nothing — so it can branch on the machine it is running on rather than
describing one shape and hoping it fits.

```lua
local drop = require("drop")

drop.name = "workstation"
drop.open_links = true

drop.mount("/", { access = "paired" })
drop.mount("/inbox",   { type = "share",  dir = "~/Downloads" })
drop.mount("/papers",  { type = "files",  dir = "~/papers" })
drop.mount("/scratch", { type = "files",  dir = "~/scratch", writable = true, access = { "me" } })
drop.mount("/logs",    { type = "stream", command = "journalctl -f -n 50" })
drop.mount("/term",    { type = "tty",    shell = "/bin/sh", input = false })
```

With no file at all, drop serves a small default: `/inbox` to send to, `/chat` to talk in, `/open`
for links — and nothing that hands over a directory, runs a command or shares a terminal, because
those are decisions.

A file that exists and does not parse is **fatal**, and the error names the file and the line,
whether what is wrong is the Lua or a setting an archetype refused. A typo that silently drops half
your namespaces is worse than not starting.

## The settings

| | |
|---|---|
| `drop.name` | what this node calls itself; defaults to the hostname |
| `drop.open_links` | whether an arriving link may be handed to an opener |
| `drop.rendezvous` | publish where this device is, so peers can find it after it moves. On by default |
| `drop.direct` | include this machine's own addresses in what is published. On by default |
| `drop.relays` | relay URLs to use instead of the defaults |
| `drop.user_key` | the SSH key that is your identity — see [pairing](pairing.md) |
| `drop.user_sign` | a command that signs, for a key drop cannot read |
| `drop.vault` | a key file, or a list of age recipients — see [on the disk](storage.md) |

## Namespaces

```lua
drop.mount(path, { type = "…", access = …, <the archetype's own settings> })
```

`type` is the archetype. Everything else in the table is that archetype's own business: the config
reader hands it a bag of names it does not understand, and the archetype reads its own settings out
and refuses a declaration it cannot serve. That is why this page does not list them — they are on
[the archetypes](archetypes.md), beside the thing that reads them.

`access` is on [access rules](access.md). A mount with no `type` is a branch: it holds others and
serves nothing itself, which is what the `/` line above is.

Mounts are keyed by path, so declaring one twice replaces it rather than adding a second.

## Reacting to things

```lua
drop.on.message(function(m)
  if m.kind == "link" and m.body:match("^https://internal%.") then
    os.execute("notify-send 'drop' " .. string.format("%q", m.body))
  end
end)

drop.on.file(function(f)
  print(string.format("drop: %s sent %s (%d bytes)", f.from, f.name, f.size))
end)
```

Handlers are appended rather than replaced, so a config can declare more than one for an event.

## Branching

It is a program, so this works:

```lua
local host = io.popen("hostname"):read("l")

if host == "laptop" then
  drop.mount("/scratch", { type = "files", dir = "~/scratch", writable = true, access = { "me" } })
else
  drop.mount("/backup", { type = "files", dir = "/srv/backup", access = { "me" } })
end
```

The configuration is not the sandbox. It is your file on your machine, and it gets a full Lua. A
[plugin](lua.md) is the other thing entirely — somebody else's code, and it gets an allowlist.

## Environment

For a profile, a one-off, or a test.

```
DROP_NAME          what this node calls itself; overrides drop.name
DROP_PORT          the port to listen on; defaults to 47777
DROP_RELAYS        relay urls to use instead of the defaults
DROP_CONFIG        the config file
DROP_USER_KEY      the user key to sign badges with; overrides drop.user_key
DROP_PROFILE       run as another person on this machine, with its own keys and port
DROP_NO_MDNS       turn the local wire off, so finding a device has to go out and back
DROP_NO_PUBLISH    publish where this device is nowhere
DROP_OPENER        what opens an arriving link; defaults to xdg-open
XDG_CONFIG_HOME    where the config, the address book and the keys live
XDG_DATA_HOME      where conversations and shared records live
```

## Where it lives

`src/pkg/conf/` reads the file. [`misc/init.lua`](../misc/init.lua) is a worked example with one
namespace of every archetype in it.
