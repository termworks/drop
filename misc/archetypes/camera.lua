-- A camera: what a machine can see, served as a namespace.
--
-- Nothing here is compiled into drop. Put this file in the archetypes directory beside init.lua
-- and this machine serves cameras:
--
--   drop.mount("/film", {
--     type   = "camera",
--     device = "/dev/video0",
--     snap   = "ffmpeg -loglevel quiet -f v4l2 -i /dev/video0 -frames:v 1 -y",
--     access = "paired",
--   })
--
-- A machine that has this file too can ask for a still. One that does not opens it as a note and
-- reads the line saying what it is pointed at, because that is what shape means: the far end only
-- ever needed to know what to say down the stream.

-- taken runs the snap command and reads back what it left.
--
-- The command writes into this namespace's own directory: still.jpg is a name and not a path, and
-- there is nowhere else it could land.
local function taken(s, c)
	if not c.snap then
		error("this camera has no way to take a still")
	end

	s:run{ "sh", "-c", c.snap .. " still.jpg" }

	local file = s:open("still.jpg")
	local body = file:read()
	file:close()
	return body
end

drop.archetype{
	name    = "camera",
	version = 1,
	shape   = "note",

	read = function(d)
		if not d.device then
			error("a camera namespace needs a device")
		end
		return { device = d.device, snap = d.snap }
	end,

	note = function(c)
		return {
			detail = c.device,
			about  = "a camera, as it sees things",
			glyph  = "◉",
		}
	end,

	serve = function(s, c)
		-- Said first and to everybody, because a machine without this file opens this as a note,
		-- and a note is answered before it has asked for anything.
		s:write("a camera pointed at " .. c.device .. "\n")

		while true do
			local asked = s:read()
			if not asked then
				return
			end

			if asked ~= "still" then
				s:write("this camera takes the word still, and nothing else\n")
			else
				drop.log(s:who().name .. " asked " .. s:path() .. " for a still")

				local ok, body = pcall(taken, s, c)
				if ok then
					s:write(body, "data")
				else
					s:write(tostring(body) .. "\n")
				end
			end
		end
	end,
}
