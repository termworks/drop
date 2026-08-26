package arch_test

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bresilla/drop/src/pkg/arch"
	"github.com/bresilla/drop/src/pkg/arch/lua"
	"github.com/bresilla/drop/src/pkg/conf"
	"github.com/bresilla/drop/src/pkg/node"
	"github.com/bresilla/drop/src/pkg/ns"
	"github.com/bresilla/drop/src/pkg/proto"
	"github.com/bresilla/drop/src/pkg/wire"
)

// The proof the boundary holds for an archetype nobody compiled.
//
// whole_test.go writes a camera in Go and shows that nothing generic learns a word of it. This one
// writes the same camera in lua, in a file beside a config, and shows the same thing: it is
// registered, read, listed, opened over a pipe and served, and ns, conf, proto and wire contain not
// one line that knows it exists.

// theCamera is the whole of the plugin: a file somebody wrote, and nothing else.
const theCamera = `
	drop.archetype{
	  name    = "camera",
	  version = 3,
	  shape   = "note",

	  read = function(d)
	    if not d.device then error("a camera namespace needs a device") end
	    return { device = d.device, colour = d.colour or false }
	  end,

	  note = function(c)
	    return { detail = c.device, about = "a camera, as it sees things", glyph = "◉" }
	  end,

	  serve = function(s, c)
	    s:write(c.device .. " " .. tostring(c.colour))
	  end,
	}
`

// beside writes a config and the archetypes beside it, and points drop at them.
func beside(t *testing.T, config string, plugins map[string]string) {
	t.Helper()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "init.lua"), []byte(config), 0o600); err != nil {
		t.Fatalf("writing the config: %v", err)
	}

	written := filepath.Join(dir, lua.Beside)
	if err := os.MkdirAll(written, 0o700); err != nil {
		t.Fatalf("making %s: %v", written, err)
	}
	for name, source := range plugins {
		if err := os.WriteFile(filepath.Join(written, name), []byte(source), 0o600); err != nil {
			t.Fatalf("writing %s: %v", name, err)
		}
	}

	t.Setenv("DROP_CONFIG", filepath.Join(dir, "init.lua"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(dir, "data"))
}

func TestAnArchetypeWrittenInLuaIsDeclaredAndServed(t *testing.T) {
	beside(t, `
		local drop = require("drop")
		drop.mount("/film", { type = "camera", device = "/dev/video0", colour = true, access = "paired" })
	`, map[string]string{"camera.lua": theCamera})

	// A registry with nothing in it. Everything it answers to came out of the file.
	known := arch.NewRegistry()

	cfg, err := conf.Load(known)
	if err != nil {
		t.Fatalf("Load(): %v", err)
	}
	defer cfg.Close()

	if names := known.Names(); len(names) != 1 || names[0] != "camera" {
		t.Fatalf("this build answers to %v", names)
	}

	mount, _, ok := cfg.Mounts.Lookup("/film")
	if !ok || mount.Archetype != "camera" {
		t.Fatalf("/film = %+v ok %v", mount, ok)
	}
	made, ok := mount.Config.(map[string]any)
	if !ok || made["device"] != "/dev/video0" || made["colour"] != true {
		t.Fatalf("the camera's settings came back as %#v", mount.Config)
	}

	// What a listing says about it comes from the file too, including the archetype a machine
	// without the file would open it as.
	caller := ns.Caller{ID: "aaaa", Name: "laptop", Paired: true}
	shown := proto.Describe(cfg.Mounts, known, caller)
	if len(shown) != 1 || shown[0].Archetype != "camera" || shown[0].About != "a camera, as it sees things" {
		t.Fatalf("the listing says %+v", shown)
	}
	if shown[0].Version != 3 || shown[0].Shape != "note" {
		t.Fatalf("the listing says version %d shaped like %q", shown[0].Version, shown[0].Shape)
	}

	// And a session on it is answered by the file, over a stream nothing generic has read.
	kind, body := over(t, cfg, known, caller, "camera", 3)
	if kind != wire.KindItem || string(body) != "/dev/video0 true" {
		t.Fatalf("the camera said frame kind %d, %q", kind, body)
	}
}

// A caller that has never heard of a camera opens it as the archetype the camera says it sounds
// like, and is not turned away for calling it by that name.
func TestAnArchetypeIsOpenedByTheShapeItSpeaks(t *testing.T) {
	beside(t, `
		local drop = require("drop")
		drop.mount("/film", { type = "camera", device = "/dev/video0", access = "paired" })
	`, map[string]string{"camera.lua": theCamera})

	known := arch.NewRegistry()
	cfg, err := conf.Load(known)
	if err != nil {
		t.Fatalf("Load(): %v", err)
	}
	defer cfg.Close()

	caller := ns.Caller{ID: "aaaa", Name: "laptop", Paired: true}
	kind, body := over(t, cfg, known, caller, "note", 0)
	if kind != wire.KindItem || string(body) != "/dev/video0 false" {
		t.Fatalf("opening it as a note said frame kind %d, %q", kind, body)
	}
}

// A plugin that raises while it is reading a declaration is that mount's mistake, named where it
// was written, and not a config that will not load for reasons nobody can find.
func TestARaiseWhileReadingIsThatMountsError(t *testing.T) {
	beside(t, `
		local drop = require("drop")
		drop.mount("/film", { type = "camera", access = "paired" })
	`, map[string]string{"camera.lua": theCamera})

	_, err := conf.Load(arch.NewRegistry())
	if err == nil {
		t.Fatal("a camera with no device was mounted anyway")
	}
	for _, want := range []string{`drop.mount("/film")`, "needs a device", "camera.lua"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("%q is not in %q", want, err)
		}
	}
}

// over opens /film by whatever name the caller uses and hands back the one frame it answers with.
func over(t *testing.T, cfg *conf.Config, known *arch.Registry, caller ns.Caller, archetype string, version int) (byte, []byte) {
	t.Helper()

	client, server := net.Pipe()
	t.Cleanup(func() { client.Close() })

	go func() {
		defer server.Close()
		_ = proto.Handle(t.Context(), deadlined{server}, node.ID{}, proto.Policy{
			Mounts:     cfg.Mounts,
			Archetypes: known,
			Who:        func(node.ID, proto.Badged) ns.Caller { return caller },
			Allow:      func(node.ID, proto.Opening) (bool, string) { return true, "" },
		})
	}()

	conn, err := proto.Open(client, "/film", archetype, version, "", "tester")
	if err != nil {
		t.Fatalf("opening /film as a %s: %v", archetype, err)
	}
	kind, body, err := conn.ReadFrame()
	if err != nil {
		t.Fatalf("reading what it said: %v", err)
	}
	return kind, body
}
