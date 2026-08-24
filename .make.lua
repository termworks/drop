-- drop's build, as recipes.
--
--   make              the recipes, with what each of them says it does
--   make build        the static release binary
--   make run          run it; arguments pass through
--   make test         the suite
--   make verify       the whole local gate
--   make install      the binary, into $PREFIX/bin
--
-- At an oslo prompt in this directory `make` is enough; everywhere else it is `oslo make`.

local make = oslo.make

---------------------------------------------------------------------------- what the build is

-- `src/main.go` is the one place the version is written down, and `release` rewrites it there.
local NAME = "drop"
local MODULE = oslo.fs.read("go.mod"):match("^module%s+(%S+)")
local VERSION = oslo.fs.read("src/main.go"):match('version%s*=%s*"([^"]+)"')
assert(MODULE, "go.mod is missing its module line")
assert(VERSION, "src/main.go is missing its version line")

local BIN = NAME
-- What the compiler produces, before packing. Kept so `_compile` has an output of its own.
local RAW = "target/" .. NAME
local ENTRY = "./src"
local PREFIX = os.getenv("PREFIX") or (os.getenv("HOME") .. "/.local")

-- What the binary is built from. `**` matches one directory level, not any depth, so each
-- level is listed: without the third pattern nothing under src/pkg/<name>/ counts as a source
-- and `build` reports up to date while shipping the previous binary.
local SOURCES = {
  "src/*.go",
  "src/**/*.go",
  "src/**/**/*.go",
  "go.mod",
  "go.sum",
}

-- Stamped into the binary, so `drop --version` answers with the checkout it came from.
local function ldflags()
  local commit = oslo.run{ "git", "rev-parse", "--short", "HEAD", capture = true }
  local stamp = os.date("!%Y-%m-%dT%H:%M:%SZ")
  return table.concat({
    "-s", "-w",
    "-X main.version=" .. VERSION,
    "-X main.commit=" .. ((commit.ok and (commit.out or ""):gsub("%s+$", "")) or ""),
    "-X main.date=" .. stamp,
    "-X main.builtBy=make",
  }, " ")
end

---------------------------------------------------------------------------- saying what was built

local function absolute(path)
  if oslo.path.is_absolute(path) then return oslo.path.normalize(path) end
  return oslo.path.normalize(oslo.path.join(oslo.fs.cwd(), path))
end

-- Whether `dir` is somewhere `$PATH` already looks.
local function on_path(dir)
  local want = absolute(dir)
  for entry in ((os.getenv("PATH") or "") .. ":"):gmatch("([^:]*):") do
    if entry ~= "" and absolute(entry) == want then return true end
  end
  return false
end

-- `8613440` → `8,613,440`.
local function grouped(n)
  local text = tostring(math.floor(n))
  local out = text:sub(-3)
  local at = #text - 3
  while at > 0 do
    out = text:sub(math.max(1, at - 2), at) .. "," .. out
    at = at - 3
  end
  return out
end

local function dim(text)
  return oslo.ui.style(text, { dim = true })
end

local function line(label, value)
  print(dim(oslo.ui.pad(label, 8)) .. value)
end

-- Where the binary is, what it weighs, and whether you can run it by name. The last row is the one
-- that earns its place: a build that succeeded and a `$PATH` that does not reach it look identical
-- until you type `drop` and get the installed one instead.
local function report(path)
  local stat = oslo.fs.stat(path)
  if not stat then return end
  local dir = oslo.path.parent(absolute(path))
  local megabytes = ("%.2f MB"):format(stat.size / 1048576)

  print("")
  print(oslo.ui.title(("%s %s   %s"):format(NAME, VERSION, megabytes)))
  line("binary", path)
  line("size", megabytes .. dim("   " .. grouped(stat.size) .. " bytes"))
  line("linking", oslo.ui.style("✓ static", { fg = "green" }) .. dim("   CGO_ENABLED=0"))
  if on_path(dir) then
    line("path", oslo.ui.style("✓ on $PATH", { fg = "green" }) .. dim("  " .. dir))
  else
    line("path", oslo.ui.style("✗ not on $PATH", { fg = "yellow" }))
    print(oslo.ui.subtitle(('         add to .env.lua:  oslo.direnv.path_add("%s")'):format(dir)))
  end
  print("")
end

---------------------------------------------------------------------------- building

make.recipe{ name = "version", desc = "what this checkout calls itself",
             run = function() print(("%s v%s"):format(NAME, VERSION)) end }

-- **Two recipes, because a skipped recipe prints nothing.**
--
-- The staleness declaration belongs to the compile, so a second `make build` with nothing changed
-- does not link again. But that also skipped the report, and a build that says nothing looks the
-- same as one that did not run. This one always runs and always answers.
make.recipe{
  name = "build",
  desc = "the release binary, packed",
  run = function()
    make.run("_compile")
    make.run("_pack")
    report(BIN)
  end,
}

-- The compile writes RAW, not BIN, and packing produces BIN from it.
--
-- Packing over the compile's own declared output would make the staleness check lie: the next
-- build sees the file present, calls itself up to date, and leaves a packed binary to be tested
-- and installed as though it were the compiler's.
make.recipe{
  name = "_compile",
  desc = "compile the release binary",
  inputs = SOURCES,
  outputs = { RAW },
  stale = "content",
  run = function()
    oslo.env.set("CGO_ENABLED", "0")
    sh.mkdir("-p", oslo.path.parent(RAW))
    sh.go("build", "-trimpath", "-ldflags", ldflags(), "-o", RAW, ENTRY)
  end,
}

-- UPX at -9. The compression level is free at startup: -1, -5 and -9 all unpack in the same
-- 0.09s, because the default decompressor runs at a speed the level does not change. Only lzma is
-- slow to unpack (0.40s), which is why it is not used here.
--
--   uncompressed   32.1 MB   0.005s to start
--   -9             11.7 MB   0.09s
--   --best --lzma   8.6 MB   0.40s
--
-- Without upx the raw binary is copied through, so a machine that lacks it still builds.
make.recipe{
  name = "_pack",
  desc = "pack the compiled binary",
  inputs = { RAW },
  outputs = { BIN },
  stale = "content",
  run = function()
    -- Built beside the target and moved onto it, rather than written over it. A rename
    -- replaces the name while whatever is already running keeps the file it started from, so
    -- `drop serve` staying up does not turn a rebuild into "text file busy".
    local staging = BIN .. ".new"

    sh.cp(RAW, staging)

    if oslo.run{ "sh", "-c", "command -v upx" }.ok then
      sh.upx("-9", "-q", staging)
    else
      print(oslo.ui.style("upx not found; shipping the binary uncompressed", { fg = "yellow" }))
    end

    sh.mv("-f", staging, BIN)
  end,
}

make.recipe{
  name = "clean",
  desc = "remove every build output",
  run = function()
    sh.rm("-rf", BIN, BIN .. ".new", "target", "dist", "coverage.out")
    oslo.run{ "go", "clean", capture = true }
  end,
}

make.recipe{ name = "compile", desc = "clean, then build", deps = { "clean", "build" } }
make.alias("c", "compile")


---------------------------------------------------------------------------- running

-- Bare words reach the binary as they are written; anything with a leading dash goes in --args,
-- because make parses a flag before the recipe ever sees it.
--
--   make run peers
--   make run --args="send --help"
--
-- The `=` is not optional there.
make.recipe{
  name = "run",
  desc = "run it: bare words pass through, flags go in --args",
  deps = { "build" },
  params = { { "--args", desc = "a quoted argument string, for arguments that start with a dash" } },
  run = function(a)
    local argv = { "./" .. BIN }
    for _, word in ipairs(a.rest or {}) do argv[#argv + 1] = word end
    if type(a.args) == "string" then
      for word in a.args:gmatch("%S+") do argv[#argv + 1] = word end
    end
    local ran = oslo.run(argv)
    os.exit(ran.status or 0)
  end,
}

make.alias("r", "run")

---------------------------------------------------------------------------- the gate

make.recipe{
  name = "test",
  desc = "the suite",
  run = function() sh.go("test", "./...") end,
}

make.alias("t", "test")


make.recipe{
  name = "e2e",
  desc = "two real nodes, driven from the command line",
  run = function()
    -- Behind a tag, and not part of `test`: it builds the binary, starts daemons, opens sockets
    -- and takes half a minute. That belongs in a decision, not in every run of the unit tests.
    oslo.env.set("CGO_ENABLED", "0")
    sh.go("test", "-tags", "e2e", "-count=1", "-timeout", "15m", "-v", "./src/e2e/")
  end,
}

make.recipe{
  name = "test-all",
  desc = "the suite with the race detector",
  run = function()
    oslo.env.set("CGO_ENABLED", "1")
    sh.go("test", "-race", "./...")
  end,
}

make.recipe{
  name = "cover",
  desc = "the suite, with a coverage profile",
  run = function()
    sh.go("test", "-coverprofile=coverage.out", "./...")
    sh.go("tool", "cover", "-func=coverage.out")
  end,
}

make.recipe{
  name = "check",
  desc = "vet the source",
  run = function() sh.go("vet", "./...") end,
}

make.alias("vet", "check")

make.recipe{
  name = "lint",
  desc = "golangci-lint over the source",
  run = function()
    assert(oslo.run{ "sh", "-c", "command -v golangci-lint" }.ok,
           "golangci-lint is not installed; enter the dev shell first")
    sh["golangci-lint"]("run", "./...")
  end,
}

make.recipe{
  name = "fmt",
  desc = "format the source",
  run = function() sh.gofmt("-w", "-s", "src") end,
}

make.recipe{
  name = "fmt-check",
  desc = "fail if anything is unformatted",
  run = function()
    local listed = oslo.run{ "gofmt", "-l", "-s", "src", capture = true }
    assert(listed.ok, "gofmt could not read the source")
    local unformatted = (listed.out or ""):gsub("%s+$", "")
    assert(unformatted == "", "gofmt needed on: " .. unformatted:gsub("\n", " "))
  end,
}

make.recipe{
  name = "tidy",
  desc = "sync go.mod and go.sum",
  run = function() sh.go("mod", "tidy") end,
}

make.recipe{
  name = "verify",
  desc = "the whole local gate",
  deps = { "fmt-check", "check", "test", "build" },
}

make.alias("v", "verify")

---------------------------------------------------------------------------- installing

make.recipe{
  name = "install",
  desc = "put the release binary in $PREFIX/bin",
  deps = { "build" },
  run = function()
    local dest = (os.getenv("DESTDIR") or "") .. PREFIX .. "/bin"
    sh.install("-d", dest)
    sh.install("-m", "0755", BIN, dest .. "/" .. NAME)
    print(oslo.ui.style("✓ ", { fg = "green" }) .. dest .. "/" .. NAME)
    if not on_path(dest) then
      print(oslo.ui.subtitle(("  %s is not on $PATH, so `%s` still finds something else")
        :format(dest, NAME)))
    end
  end,
}

make.recipe{
  name = "uninstall",
  desc = "take it back out of $PREFIX/bin",
  run = function()
    local dest = PREFIX .. "/bin/" .. NAME
    sh.rm("-f", dest)
    print("removed " .. dest)
  end,
}

---------------------------------------------------------------------------- releasing

make.recipe{
  name = "changelog",
  desc = "regenerate CHANGELOG.md",
  run = function()
    assert(oslo.run{ "sh", "-c", "command -v git-cliff" }.ok,
           "git-cliff is not installed; install it first")
    sh.git("cliff", "-o", "CHANGELOG.md")
  end,
}

make.recipe{
  name = "release",
  desc = "cut a version: --type patch | minor | major | M.m.p",
  params = { { "--type", desc = "patch | minor | major | M.m.p" } },
  run = function(a)
    assert(oslo.run{ "sh", "-c", "command -v git-rel" }.ok,
           "git-rel is not installed; install it first")
    assert(type(a.type) == "string",
           "which release? make release --type patch|minor|major|M.m.p")
    sh.git("rel", a.type)
  end,
}
