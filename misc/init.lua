-- drop's configuration.
--
-- Settings are assigned, namespaces are registered, and this file returns nothing — so it can
-- branch on the machine it is running on rather than describing one shape and hoping it fits.

local drop = require("drop")

drop.name = "workstation"
drop.open_links = true

-- Files land here. A namespace is a path under this node's id, so `workstation/inbox` is an
-- address a paired device can send to.
drop.mount("/inbox", { type = "files", dir = "~/Downloads" })
drop.mount("/inbox/photos", { type = "files", dir = "~/Pictures/drop" })

-- A stream namespace runs a command and sends whatever it writes, for as long as it writes it.
-- Nobody knows how much that will be, which is the point.
drop.mount("/logs", { type = "stream", command = "journalctl -f -n 50" })
drop.mount("/top", { type = "stream", command = "top -b -d 2" })

-- A terminal. Read-only unless input is allowed, and `only` narrows it further.
drop.mount("/term", { type = "tty", shell = "/bin/sh", input = false })

-- Chat and links.
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
