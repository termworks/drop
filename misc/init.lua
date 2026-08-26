-- drop's configuration.
--
-- Settings are assigned, namespaces are registered, and this file returns nothing — so it can
-- branch on the machine it is running on rather than describing one shape and hoping it fits.
--
-- A namespace is a path this node serves. Its `type` is the archetype it belongs to — what
-- opening it does — and the rest of the table is that archetype's own settings.

local drop = require("drop")

drop.name = "workstation"
drop.open_links = true

-- A branch: no type, only who may reach what is under it. Access inherits downward and a path with
-- no rule above it is reachable by nobody, so this line is what makes the rest of the file serve
-- anybody at all. A rule written deeper replaces this one rather than adding to it.
drop.mount("/", { access = "paired" })

-- share: a drop box. Things are pushed in and land here; nothing in the directory is listed or
-- readable from the other side. A namespace is a path under this node's id, so `workstation/inbox`
-- is an address a paired device can send to.
drop.mount("/inbox", { type = "share", dir = "~/Downloads" })
drop.mount("/inbox/photos", { type = "share", dir = "~/Pictures/drop" })

-- files: a folder the far end walks — lists it, and takes copies out of it. This one is read-only,
-- which is what a files namespace is unless it says otherwise.
drop.mount("/papers", { type = "files", dir = "~/papers" })

-- The same archetype, writable. Now whoever this rule admits may also upload, make directories,
-- move things and DELETE them: there is one flag, not one per operation. Write it against a rule
-- you would say out loud, and prefer the line above when reading is enough.
drop.mount("/scratch", { type = "files", dir = "~/scratch", writable = true, access = { "me" } })

-- stream: a command runs and whatever it writes goes over, for as long as it writes it. Nobody
-- knows how much that will be, which is the point.
drop.mount("/logs", { type = "stream", command = "journalctl -f -n 50" })
drop.mount("/top", { type = "stream", command = "top -b -d 2" })

-- tty: a shell on this machine, one terminal shared by everybody watching it. `input` is what
-- decides whether they may type into it, and it is off here — watching a shell and driving one are
-- different things to hand over.
drop.mount("/term", { type = "tty", shell = "/bin/sh", input = false })

-- chat: messages, kept as a conversation. link: a URL, handed to `action` — without one it is
-- only written down, which is the safer half.
drop.mount("/chat", { type = "chat" })
drop.mount("/open", { type = "link", action = "xdg-open" })

-- The config is a program, so a machine can decide for itself what it offers.
if os.getenv("DROP_DEV") then
  drop.mount("/build", { type = "stream", command = "tail -f /tmp/build.log" })
end

-- Behaviour is registered, and registration repeats: declare as many handlers as you like and
-- every one of them runs. A raise in one is reported and survived, so it cannot take the rest down.
drop.on.message(function(m)
  if m.kind == "link" and m.body:match("^https://internal%.") then
    os.execute("notify-send 'drop' " .. string.format("%q", m.body))
  end
end)

drop.on.file(function(f)
  print(string.format("drop: %s sent %s (%d bytes)", f.from, f.name, f.size))
end)

-- The VM is Lua 5.4, so integer division, bitwise operators and the integer subtype are all here.
for i = 1, 4 do
  drop.mount("/stream/" .. i, { type = "stream", command = "echo stream " .. i })
end
