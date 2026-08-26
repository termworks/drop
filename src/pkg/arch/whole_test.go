package arch_test

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bresilla/drop/src/pkg/arch"
	"github.com/bresilla/drop/src/pkg/conf"
	"github.com/bresilla/drop/src/pkg/node"
	"github.com/bresilla/drop/src/pkg/ns"
	"github.com/bresilla/drop/src/pkg/proto"
	"github.com/bresilla/drop/src/pkg/wire"
)

// The proof the boundary holds.
//
// A camera is written here and nowhere else: it reads its own settings out of a lua table, says its
// own thing about itself, and speaks a protocol of its own invention. It is declared in a config,
// read by the config reader, mounted in the namespace table, opened over a pipe and served — and
// not one line of ns, conf or proto knows a word of it.

// camera is an archetype that exists only in this file.
type camera struct{}

// cameraConfig is what a camera made of its declaration.
type cameraConfig struct {
	Device string
	Colour bool
}

func (camera) Name() string { return "camera" }
func (camera) Version() int { return 3 }

func (camera) Read(d arch.Declared) (arch.Config, error) {
	device, ok := d.String("device")
	if !ok || device == "" {
		return nil, fmt.Errorf("a camera namespace needs a device")
	}
	colour, _ := d.Bool("colour")
	return cameraConfig{Device: device, Colour: colour}, nil
}

func (camera) Note(c arch.Config) arch.Note {
	cfg, _ := c.(cameraConfig)
	return arch.Note{Detail: cfg.Device, About: "a camera, as it sees things", Glyph: "◉"}
}

// Serve says what this camera is pointed at. Nothing outside this file reads a byte of it.
func (camera) Serve(ctx context.Context, at arch.Session) error {
	cfg, ok := at.Config.(cameraConfig)
	if !ok {
		return fmt.Errorf("%s is not a camera after all", at.Path)
	}
	return at.Conn.WriteFrame(wire.KindItem, fmt.Appendf(nil, "%s %v", cfg.Device, cfg.Colour))
}

func TestAnArchetypeWrittenOutsideDropIsDeclaredAndServed(t *testing.T) {
	known := arch.NewRegistry()
	known.Register(camera{})

	// The lua a person writes. Nothing about it is special: the keys are read by the camera.
	path := filepath.Join(t.TempDir(), "init.lua")
	written := `
		local drop = require("drop")
		drop.mount("/film", { type = "camera", device = "/dev/video0", colour = true, access = "paired" })
	`
	if err := os.WriteFile(path, []byte(written), 0o600); err != nil {
		t.Fatalf("writing the config: %v", err)
	}
	t.Setenv("DROP_CONFIG", path)

	cfg, err := conf.Load(known)
	if err != nil {
		t.Fatalf("Load(): %v", err)
	}
	defer cfg.Close()

	mount, _, ok := cfg.Mounts.Lookup("/film")
	if !ok || mount.Archetype != "camera" {
		t.Fatalf("/film = %+v ok %v", mount, ok)
	}
	if got, ok := mount.Config.(cameraConfig); !ok || got.Device != "/dev/video0" || !got.Colour {
		t.Fatalf("the camera's settings came back as %#v", mount.Config)
	}

	// What a listing says about it comes from the camera too.
	caller := ns.Caller{ID: "aaaa", Name: "laptop", Paired: true}
	shown := proto.Describe(cfg.Mounts, known, caller)
	if len(shown) != 1 || shown[0].Archetype != "camera" || shown[0].About != "a camera, as it sees things" {
		t.Fatalf("the listing says %+v", shown)
	}
	if shown[0].Version != 3 {
		t.Errorf("the listing says version %d", shown[0].Version)
	}

	// And a session on it is answered by the camera, over a stream nothing generic has read.
	client, server := net.Pipe()
	defer client.Close()

	go func() {
		defer server.Close()
		_ = proto.Handle(t.Context(), deadlined{server}, node.ID{}, proto.Policy{
			Mounts:     cfg.Mounts,
			Archetypes: known,
			Who:        func(node.ID, proto.Badged) ns.Caller { return caller },
			Allow:      func(node.ID, proto.Opening) (bool, string) { return true, "" },
		})
	}()

	conn, err := proto.Open(client, "/film", "camera", 3, "", "tester")
	if err != nil {
		t.Fatalf("Open(): %v", err)
	}
	kind, body, err := conn.ReadFrame()
	if err != nil {
		t.Fatalf("reading what the camera said: %v", err)
	}
	if kind != wire.KindItem || string(body) != "/dev/video0 true" {
		t.Fatalf("the camera said frame kind %d, %q", kind, body)
	}
}

// deadlined is a pipe with the deadline a session stream is expected to have.
type deadlined struct{ net.Conn }

func (d deadlined) SetReadDeadline(t time.Time) error { return d.Conn.SetReadDeadline(t) }
