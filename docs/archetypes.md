# The archetypes

Seven of them ship. Each is one kind of thing, and knows nothing about the others.

| | | shareable | writable |
|---|---|---|---|
| [`share`](#share) | hand files over, once | no | yes |
| [`files`](#files) | a directory, to walk through | yes | optional |
| [`chat`](#chat) | messages, kept as a conversation | no | yes |
| [`note`](#note) | a file, written by several people at once | yes | yes |
| [`link`](#link) | open a link over there | no | yes |
| [`stream`](#stream) | output from a command, as it comes | no | no |
| [`tty`](#tty) | a terminal, as it is being used | no | optional |

"Shareable" means several machines may hold one of these and see each other's changes — see
[one thing, several machines](shared.md). A terminal is somebody's screen and a cast is somebody's
output; there is no sense in which two of them are the same one, so they are not shareable.

## share

A drop box. Things are pushed in and land in a directory; nothing in that directory is listed or
readable from the other side.

```lua
drop.mount("/inbox", { type = "share", dir = "~/Downloads" })
```

A push carries files, not whole directories. Each item is offered with its name and size, accepted
with how much is already held, then sent and verified against a blake3 digest.

An item waits in a `.part` file while it arrives, so a dropped connection resumes rather than
starting again. The sender is folded into that name along with the name and the size, because two
peers offering a file of the same name and size would otherwise write into one file and each be told
theirs had arrived.

`share` and `files` are not the same thing, and the difference is the point. A share appears and
disappears: one side sends, the other receives, and afterwards there is nothing to open. A files
namespace is a directory that stays, that you walk through, and that you add to and remove from.

## files

A directory the far end walks — lists it, and takes copies out of it. Read-only unless it says
otherwise.

```lua
drop.mount("/papers", { type = "files", dir = "~/papers" })
drop.mount("/scratch", { type = "files", dir = "~/scratch", writable = true, access = { "me" } })
```

`writable` is one flag, not one per operation: whoever it admits may upload, make directories, move
things and **delete** them. Write it against a rule you would say out loud.

Rounds, one request and one reply at a time, for as long as the caller keeps asking. Every path is
resolved through `os.Root`, so a name that climbs out — with `..`, an absolute path, a backslash, a
NUL — is refused rather than followed. A name in a listing must be one name in a directory: nothing
with a separator in it, because the moment something builds a local path from one it would inherit
whatever the far end put there.

Two machines can hold one `files` namespace and work in it at once. That is [shared](shared.md).

## chat

Messages, kept as a conversation. What is acknowledged is what reached a disk, nothing more.

```lua
drop.mount("/chat", { type = "chat" })
```

A message for a device that is switched off is queued and goes when it next connects. Conversations
live under `$XDG_DATA_HOME/drop/convo/<peer id>/` and can be encrypted — see [on the disk](storage.md).

## note

One file, edited by several people at once. The file is a real file on your disk, opened in whatever
editor you like: drop never becomes an editor.

```lua
drop.mount("/standup", { type = "note", file = "~/notes/standup.md" })
```

What it does is notice a save, sign what was saved as a change, and — when changes made elsewhere
arrive — write back the file all the changes together make. Merging happens on a save and on an
arrival, not per keystroke. Details in [shared](shared.md).

## link

A URL, handed to a command over there.

```lua
drop.mount("/open", { type = "link", action = "xdg-open" })
drop.mount("/read", { type = "link" })          -- written down, and nothing else
```

Without an `action` it is only recorded, which is the safer half and the default.

## stream

A command runs and whatever it writes goes over, for as long as it writes it. Nobody knows how much
that will be, which is the point.

```lua
drop.mount("/logs", { type = "stream", command = "journalctl -f -n 50" })
```

One direction only: nothing the far end sends is read into anything, so the session lasts exactly as
long as the command's output does. The command gets a process group of its own and the group is
ended with the session — see [hardening](security.md) for why that is not the obvious code.

## tty

A shell on this machine, one terminal shared by everybody watching it.

```lua
drop.mount("/term", { type = "tty", shell = "/bin/sh", input = false })
```

`input` decides whether watchers may type. Watching a shell and driving one are different things to
hand over, and it is off unless you say otherwise.

One shell per path, not per watcher: somebody arriving late is handed the screen as it stands,
rebuilt from the scrollback, and sees the same thing everybody else does. When the shell exits the
terminal leaves the table so the next watcher starts a fresh one.

`drop path cast` is the other half: a terminal read from standard input and served as an asciicast,
for output that was recorded rather than a shell that is live.

## Where they live

One package each under `src/pkg/arch/`. The interface and the registry are in `src/pkg/arch/arch.go`
— see [namespaces](namespaces.md). To write one of your own, in Lua, both ends loading the same
plugin: [an archetype in Lua](lua.md).
