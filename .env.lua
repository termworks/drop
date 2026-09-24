-- The NVIDIA driver version, for flake.nix to build the nixGL wrapper that matches it. It has to be
-- in the environment before the shell is evaluated, and the evaluation has to be impure to see it.
local function busy_wait(seconds)
  local start = os.clock()
  while os.clock() - start < seconds do end
end

local function nvidia_version()
  local present = false
  for _, path in ipairs(oslo.fs.glob("/sys/bus/pci/devices/*/vendor")) do
    local vendor = oslo.fs.read(path)
    if vendor and vendor:match("^%s*0x10de%s*$") then
      present = true
      break
    end
  end
  if not present then
    return nil
  end

  -- The card can be on the bus a moment before its module has loaded.
  for _ = 1, 5 do
    local text = oslo.fs.read("/proc/driver/nvidia/version")
    local version = text and text:match("%s%s(%d[%d%.]*)%s%sRelease")
    if version then
      return version
    end
    busy_wait(0.3)
  end
  return nil
end

local nvidia = nvidia_version()
if nvidia then
  oslo.env.set("NVIDIA_VERSION", nvidia)
  oslo.env.set("__NV_PRIME_RENDER_OFFLOAD", "1")
  oslo.env.set("__NV_PRIME_RENDER_OFFLOAD_PROVIDER", "NVIDIA-G0")
  oslo.env.set("__GLX_VENDOR_LIBRARY_NAME", "nvidia")
  oslo.env.set("__VK_LAYER_NV_optimus", "NVIDIA_only")
end

oslo.direnv.nix_develop({ impure = true })

oslo.direnv.path_add("./")

oslo.env.set("TOP_HEAD", oslo.sys.pwd())

oslo.env.unset("GITHUB_TOKEN")

oslo.env.set("CGO_ENABLED", "0")

oslo.env.set_alias("_b", "make build")
oslo.env.set_alias("_c", "make compile")
oslo.env.set_alias("_r", "make run")
oslo.env.set_alias("_t", "make test")
oslo.env.set_alias("_v", "make verify")
oslo.env.set_alias("_i", "make install")
