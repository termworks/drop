package lua

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bresilla/drop/src/pkg/arch"
	"github.com/bresilla/drop/src/pkg/ns"
	"github.com/bresilla/drop/src/pkg/wire"
)

// told is a declaration written in a test, the way a config would have written one.
type told map[string]any

func (d told) String(key string) (string, bool) { v, ok := d[key].(string); return v, ok }
func (d told) Bool(key string) (bool, bool)     { v, ok := d[key].(bool); return v, ok }

func (d told) Strings(key string) ([]string, bool) { v, ok := d[key].([]string); return v, ok }

// deadlined is a pipe with the deadline a session stream is expected to have.
type deadlined struct{ net.Conn }

func (d deadlined) SetReadDeadline(t time.Time) error { return d.Conn.SetReadDeadline(t) }

// written puts a plugin in a file and compiles it the way a config does.
func written(t *testing.T, source string) *Plugin {
	t.Helper()

	dir := t.TempDir()
	file := filepath.Join(dir, "thing.lua")
	if err := os.WriteFile(file, []byte(source), 0o600); err != nil {
		t.Fatalf("writing the plugin: %v", err)
	}

	made, err := compile(file, filepath.Join(dir, "keeps"))
	if err != nil {
		t.Fatalf("compiling the plugin: %v", err)
	}
	if len(made) != 1 {
		t.Fatalf("the file declared %d archetypes, expected one", len(made))
	}
	return made[0]
}

// opened runs one session of a plugin over a pipe and hands back the caller's end of it.
func opened(t *testing.T, p *Plugin, settings arch.Config) (*wire.Conn, net.Conn, <-chan error) {
	t.Helper()

	client, server := net.Pipe()
	done := make(chan error, 1)

	go func() {
		defer server.Close()
		done <- p.Serve(t.Context(), arch.Session{
			Path:   "/thing",
			Config: settings,
			Who:    ns.Caller{ID: "aaaa", Name: "laptop", Paired: true},
			Conn:   wire.NewConn(server),
			Stream: deadlined{server},
		})
	}()
	return wire.NewConn(client), client, done
}

// said is one frame back from a plugin.
func said(t *testing.T, conn *wire.Conn) (byte, []byte) {
	t.Helper()

	kind, body, err := conn.ReadFrame()
	if err != nil {
		t.Fatalf("reading what the plugin said: %v", err)
	}
	return kind, body
}

// A plugin's world is what was put in it and nothing else. Everything named here is a way out of
// the sandbox, and none of them is reachable.
func TestAPluginCannotReachTheHost(t *testing.T) {
	p := written(t, `
		drop.archetype{
			name  = "prober",
			read  = function(d) return {} end,
			note  = function(c) return {} end,
			serve = function(s, c)
				local found = {}
				local names = {
					"io", "os", "debug", "package", "require", "load", "loadfile", "dofile",
					"print", "_G", "coroutine", "collectgarbage", "warn",
				}
				for _, name in ipairs(names) do
					if _ENV[name] ~= nil then found[#found + 1] = name end
				end
				s:write(table.concat(found, " "))
			end,
		}
	`)

	conn, client, done := opened(t, p, nil)
	defer client.Close()

	_, body := said(t, conn)
	if len(body) != 0 {
		t.Fatalf("a plugin can reach %s", body)
	}
	if err := <-done; err != nil {
		t.Fatalf("Serve(): %v", err)
	}
}

// A loop that never ends is stopped, the daemon is still standing afterwards, and the next session
// of the same plugin works.
func TestALoopThatNeverEndsIsStopped(t *testing.T) {
	p := written(t, `
		drop.archetype{
			name  = "spinner",
			read  = function(d) return {} end,
			note  = function(c) return {} end,
			serve = function(s, c)
				if s:read() == "spin" then
					while true do end
				end
				s:write("still here")
			end,
		}
	`)

	conn, client, done := opened(t, p, nil)
	if err := conn.WriteFrame(wire.KindItem, []byte("spin")); err != nil {
		t.Fatalf("asking it to spin: %v", err)
	}
	err := <-done
	client.Close()

	if err == nil || !strings.Contains(err.Error(), "CPU") {
		t.Fatalf("the loop ended with %v", err)
	}

	after, client, done := opened(t, p, nil)
	defer client.Close()

	if err := after.WriteFrame(wire.KindItem, []byte("behave")); err != nil {
		t.Fatalf("asking the next one to behave: %v", err)
	}
	if _, body := said(t, after); string(body) != "still here" {
		t.Fatalf("the session after it said %q", body)
	}
	if err := <-done; err != nil {
		t.Fatalf("the session after it: %v", err)
	}
}

// A plugin that makes more than it may is stopped too, and by the other half of the budget.
func TestAPluginThatEatsMemoryIsStopped(t *testing.T) {
	p := written(t, `
		drop.archetype{
			name  = "hog",
			read  = function(d) return {} end,
			note  = function(c) return {} end,
			serve = function(s, c)
				local kept = {}
				local i = 1
				while true do
					kept[i] = string.rep("x", 65536)
					i = i + 1
				end
			end,
		}
	`)

	_, client, done := opened(t, p, nil)
	defer client.Close()

	err := <-done
	if err == nil || !strings.Contains(err.Error(), "memory") {
		t.Fatalf("the hog ended with %v", err)
	}
}

// A plugin that raises is that session's error and nothing else's.
func TestARaiseIsOneSessionsError(t *testing.T) {
	p := written(t, `
		drop.archetype{
			name  = "brittle",
			read  = function(d) return {} end,
			note  = function(c) return {} end,
			serve = function(s, c)
				if s:read() == "break" then error("the camera fell over") end
				s:write("fine")
			end,
		}
	`)

	conn, client, done := opened(t, p, nil)
	if err := conn.WriteFrame(wire.KindItem, []byte("break")); err != nil {
		t.Fatalf("asking it to break: %v", err)
	}
	err := <-done
	client.Close()

	if err == nil || !strings.Contains(err.Error(), "the camera fell over") {
		t.Fatalf("the raise came back as %v", err)
	}
	if !strings.Contains(err.Error(), "thing.lua") {
		t.Errorf("the raise does not say which file: %v", err)
	}

	after, client, done := opened(t, p, nil)
	defer client.Close()

	if err := after.WriteFrame(wire.KindItem, []byte("carry on")); err != nil {
		t.Fatalf("asking the next one: %v", err)
	}
	if _, body := said(t, after); string(body) != "fine" {
		t.Fatalf("the session after it said %q", body)
	}
	if err := <-done; err != nil {
		t.Fatalf("the session after it: %v", err)
	}
}

// Two sessions of one plugin are two conversations. Run hard and at once, because everything about
// one runtime per session rests on this.
func TestSessionsOfOnePluginShareNothing(t *testing.T) {
	p := written(t, `
		local seen = 0
		drop.archetype{
			name  = "counter",
			read  = function(d) return {} end,
			note  = function(c) return {} end,
			serve = function(s, c)
				while true do
					local b = s:read()
					if not b then break end
					seen = seen + 1
					s:write(b .. ":" .. tostring(seen))
				end
			end,
		}
	`)

	var wg sync.WaitGroup
	for at := range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()

			conn, client, done := opened(t, p, nil)
			defer client.Close()

			for i := 1; i <= 100; i++ {
				if err := conn.WriteFrame(wire.KindItem, []byte("a")); err != nil {
					t.Errorf("session %d frame %d: %v", at, i, err)
					return
				}
				kind, body, err := conn.ReadFrame()
				if err != nil {
					t.Errorf("session %d frame %d: %v", at, i, err)
					return
				}
				want := "a:" + itoa(i)
				if kind != wire.KindItem || string(body) != want {
					t.Errorf("session %d frame %d said %q, wanted %q", at, i, body, want)
					return
				}
			}
			if err := conn.WriteFrame(wire.KindEnd, nil); err != nil {
				t.Errorf("session %d ending: %v", at, err)
			}
			if err := <-done; err != nil {
				t.Errorf("session %d: %v", at, err)
			}
		}()
	}
	wg.Wait()
}

// A session left in the middle takes its goroutine with it.
func TestAnAbandonedSessionLeavesNoGoroutine(t *testing.T) {
	p := written(t, `
		drop.archetype{
			name  = "waiter",
			read  = function(d) return {} end,
			note  = function(c) return {} end,
			serve = function(s, c)
				s:read()
				s:write("nobody is listening")
				s:write("nor to this")
			end,
		}
	`)

	settle(t)
	before := runtime.NumGoroutine()

	for range 200 {
		conn, client, done := opened(t, p, nil)
		if err := conn.WriteFrame(wire.KindItem, []byte("hello")); err != nil {
			t.Fatalf("saying hello: %v", err)
		}
		client.Close()
		<-done
	}

	settle(t)
	if after := runtime.NumGoroutine(); after > before+10 {
		t.Fatalf("200 abandoned sessions left %d goroutines behind", after-before)
	}
}

// Bytes are bytes. A frame with a zero in it and a frame that is not text at all come back exactly
// as they went.
func TestBinarySurvivesAPlugin(t *testing.T) {
	p := written(t, `
		drop.archetype{
			name  = "echo",
			read  = function(d) return {} end,
			note  = function(c) return {} end,
			serve = function(s, c)
				while true do
					local b = s:read()
					if not b then break end
					s:write(b)
				end
			end,
		}
	`)

	conn, client, done := opened(t, p, nil)
	defer client.Close()

	for _, body := range [][]byte{
		{0, 1, 2, 0, 255},
		{0xff, 0xfe, 0xfd},
		[]byte("\x00a\x00b\x00"),
		{},
	} {
		if err := conn.WriteFrame(wire.KindItem, body); err != nil {
			t.Fatalf("sending %q: %v", body, err)
		}
		kind, back := said(t, conn)
		if kind != wire.KindItem || string(back) != string(body) {
			t.Fatalf("%q came back as %q", body, back)
		}
	}

	if err := conn.WriteFrame(wire.KindEnd, nil); err != nil {
		t.Fatalf("ending: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatalf("Serve(): %v", err)
	}
}

// A name a plugin gives is a name inside its own directory, and there is no way to write one that
// is not — not by climbing, and not by following something already there.
func TestAPluginCannotOpenOutsideItsOwnDirectory(t *testing.T) {
	p := written(t, `
		drop.archetype{
			name  = "opener",
			read  = function(d) return {} end,
			note  = function(c) return {} end,
			serve = function(s, c)
				while true do
					local name = s:read()
					if not name then break end
					local ok, said = pcall(function()
						local f = s:open(name, "w")
						f:write("here")
						f:close()
						return "wrote it"
					end)
					s:write(tostring(ok) .. " " .. tostring(said))
				end
			end,
		}
	`)

	// The namespace's own directory, with something in it that points out of it.
	own := filepath.Join(p.keeps, p.name, slug("/thing"))
	if err := os.MkdirAll(own, 0o700); err != nil {
		t.Fatalf("making the namespace's directory: %v", err)
	}
	outside := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(outside, []byte("not yours"), 0o600); err != nil {
		t.Fatalf("writing something outside: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(own, "away")); err != nil {
		t.Skipf("this filesystem has no symlinks: %v", err)
	}

	conn, client, done := opened(t, p, nil)
	defer client.Close()

	for _, name := range []string{"../secret", "away", "/etc/passwd", "deeper/../../secret"} {
		if err := conn.WriteFrame(wire.KindItem, []byte(name)); err != nil {
			t.Fatalf("naming %s: %v", name, err)
		}
		if _, body := said(t, conn); !strings.HasPrefix(string(body), "false ") {
			t.Fatalf("opening %q said %q", name, body)
		}
	}

	// And a name that is a name works, so the test above is about the boundary and not about
	// opening being broken.
	if err := conn.WriteFrame(wire.KindItem, []byte("mine.txt")); err != nil {
		t.Fatalf("naming mine.txt: %v", err)
	}
	if _, body := said(t, conn); !strings.HasPrefix(string(body), "true ") {
		t.Fatalf("opening a plain name said %q", body)
	}
	if _, err := os.Stat(filepath.Join(own, "mine.txt")); err != nil {
		t.Fatalf("the file was not written where it belongs: %v", err)
	}
	if got, _ := os.ReadFile(outside); string(got) != "not yours" {
		t.Fatalf("what is outside now says %q", got)
	}

	if err := conn.WriteFrame(wire.KindEnd, nil); err != nil {
		t.Fatalf("ending: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatalf("Serve(): %v", err)
	}
}

// The settings a plugin makes of a declaration are its own, and what it says about a namespace
// comes back the same way.
func TestAPluginReadsItsOwnSettings(t *testing.T) {
	p := written(t, `
		drop.archetype{
			name    = "camera",
			version = 3,
			shape   = "note",
			read    = function(d)
				if not d.device then error("a camera needs a device") end
				return { device = d.device, colour = d.colour or false }
			end,
			note    = function(c)
				return { detail = c.device, about = "a camera, as it sees things", glyph = "◉" }
			end,
			serve   = function(s, c) s:write(c.device) end,
		}
	`)

	if p.Version() != 3 || p.shape != "note" {
		t.Fatalf("the plugin is %s/%d shaped like %q", p.Name(), p.Version(), p.shape)
	}

	if _, err := p.Read(told{}); err == nil {
		t.Fatal("a camera with no device was read anyway")
	}

	settings, err := p.Read(told{"device": "/dev/video0", "colour": true})
	if err != nil {
		t.Fatalf("Read(): %v", err)
	}
	made, ok := settings.(map[string]any)
	if !ok || made["device"] != "/dev/video0" || made["colour"] != true {
		t.Fatalf("the settings came back as %#v", settings)
	}

	note := p.Note(settings)
	if note.Detail != "/dev/video0" || note.Glyph != "◉" || note.Shape != "note" {
		t.Fatalf("what it says about itself is %+v", note)
	}

	conn, client, done := opened(t, p, settings)
	defer client.Close()

	if _, body := said(t, conn); string(body) != "/dev/video0" {
		t.Fatalf("the session said %q", body)
	}
	if err := <-done; err != nil {
		t.Fatalf("Serve(): %v", err)
	}
}

// The rest of what a session hands a plugin: who is calling, where it is, a process, and a line in
// the daemon's output.
func TestASessionHandsThePluginWhatItNeeds(t *testing.T) {
	p := written(t, `
		drop.archetype{
			name  = "asker",
			read  = function(d) return {} end,
			note  = function(c) return {} end,
			serve = function(s, c)
				local who = s:who()
				drop.log("serving " .. who.name)
				local said = s:run{ "/bin/echo", "hello" }
				s:write(who.name .. " " .. tostring(who.paired) .. " " .. s:path() .. " " .. said)
			end,
		}
	`)

	conn, client, done := opened(t, p, nil)
	defer client.Close()

	_, body := said(t, conn)
	if string(body) != "laptop true /thing hello\n" {
		t.Fatalf("the session said %q", body)
	}
	if err := <-done; err != nil {
		t.Fatalf("Serve(): %v", err)
	}
}

// A plugin may not keep something in its settings that cannot outlive the runtime that made it.
func TestSettingsThatCannotTravelAreRefused(t *testing.T) {
	p := written(t, `
		drop.archetype{
			name  = "clever",
			read  = function(d) return { later = function() end } end,
			note  = function(c) return {} end,
			serve = function(s, c) end,
		}
	`)

	if _, err := p.Read(told{}); err == nil || !strings.Contains(err.Error(), "function") {
		t.Fatalf("keeping a function in the settings came back as %v", err)
	}
}

// A file that will not compile says where.
func TestAPluginThatWillNotCompileSaysWhere(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "broken.lua")
	if err := os.WriteFile(file, []byte("drop.archetype{ name = \n"), 0o600); err != nil {
		t.Fatalf("writing the plugin: %v", err)
	}

	known := arch.NewRegistry()
	err := Load(dir, known)
	if err == nil {
		t.Fatal("a file that will not compile was loaded anyway")
	}
	if !strings.Contains(err.Error(), "broken.lua") {
		t.Errorf("the error does not name the file: %v", err)
	}
}

// The archetype shipped as an example is one somebody will copy, so it had better load.
func TestTheExampleShippedWithDropLoads(t *testing.T) {
	known := arch.NewRegistry()
	if err := Load(filepath.Join("..", "..", "..", "..", "misc", Beside), known); err != nil {
		t.Fatalf("Load(): %v", err)
	}

	p, ok := known.Lookup("camera", 0)
	if !ok {
		t.Fatalf("the example registered %v", known.Names())
	}
	settings, err := p.Read(told{"device": "/dev/video0"})
	if err != nil {
		t.Fatalf("Read(): %v", err)
	}
	if note := p.Note(settings); note.Detail != "/dev/video0" || note.Shape != "note" {
		t.Fatalf("what it says about itself is %+v", note)
	}
}

// A directory with no archetypes in it is not a mistake.
func TestNothingBesideTheConfigIsFine(t *testing.T) {
	if err := Load(filepath.Join(t.TempDir(), "archetypes"), arch.NewRegistry()); err != nil {
		t.Fatalf("Load(): %v", err)
	}
}

// A plugin may not take the name of an archetype this build was made with.
func TestAPluginCannotTakeABuiltInName(t *testing.T) {
	dir := t.TempDir()
	written := `drop.archetype{ name = "chat", read = function(d) return {} end,
		note = function(c) return {} end, serve = function(s, c) end }`
	if err := os.WriteFile(filepath.Join(dir, "chat.lua"), []byte(written), 0o600); err != nil {
		t.Fatalf("writing the plugin: %v", err)
	}

	known := arch.NewRegistry()
	known.Register(stub{})

	err := Load(dir, known)
	if err == nil || !strings.Contains(err.Error(), "chat") {
		t.Fatalf("a plugin took a built-in name and came back with %v", err)
	}
}

// Nor at a version this build never had. A mount that pinned no version asks for the newest, so a
// plugin declaring a later one is the one every existing mount would reach.
func TestAPluginCannotTakeABuiltInNameAtALaterVersion(t *testing.T) {
	dir := t.TempDir()
	written := `drop.archetype{ name = "chat", version = 2, read = function(d) return {} end,
		note = function(c) return {} end, serve = function(s, c) end }`
	if err := os.WriteFile(filepath.Join(dir, "anything.lua"), []byte(written), 0o600); err != nil {
		t.Fatalf("writing the plugin: %v", err)
	}

	known := arch.NewRegistry()
	known.Register(stub{})

	err := Load(dir, known)
	if err == nil || !strings.Contains(err.Error(), "chat") {
		t.Fatalf("a plugin took a built-in name at version 2 and came back with %v", err)
	}
	if was, _ := known.Lookup("chat", 0); was != arch.Archetype(stub{}) {
		t.Fatalf("chat is now served by %T", was)
	}
}

// Two files declaring one name is a mistake somebody has to be told about, not a race between two
// filenames.
func TestTwoFilesCannotDeclareOneName(t *testing.T) {
	dir := t.TempDir()
	for _, file := range []string{"one.lua", "two.lua"} {
		written := `drop.archetype{ name = "camera", read = function(d) return {} end,
			note = function(c) return {} end, serve = function(s, c) end }`
		if err := os.WriteFile(filepath.Join(dir, file), []byte(written), 0o600); err != nil {
			t.Fatalf("writing %s: %v", file, err)
		}
	}

	err := Load(dir, arch.NewRegistry())
	if err == nil || !strings.Contains(err.Error(), "camera") {
		t.Fatalf("two files declared one camera and came back with %v", err)
	}
}

// The example shipped with drop is the one everybody copies, so two people asking one camera for a
// still at the same moment have to get two whole stills.
func TestTwoWatchersOfOneCameraGetTheirOwnStill(t *testing.T) {
	made, err := compile(filepath.Join("..", "..", "..", "..", "misc", Beside, "camera.lua"), t.TempDir())
	if err != nil {
		t.Fatalf("compiling the example: %v", err)
	}
	p := made[0]

	// A snap that takes as long as a real one does and writes a mark of its own three times.
	snap := `sh -c 'n=$$; : > "$0"; for i in 1 2 3; do echo $n >> "$0"; sleep 0.1; done'`
	settings, err := p.Read(told{"device": "/dev/video0", "snap": snap})
	if err != nil {
		t.Fatalf("Read(): %v", err)
	}

	stills := make([]string, 2)
	var wg sync.WaitGroup
	for at := range stills {
		wg.Add(1)
		go func() {
			defer wg.Done()

			conn, client, done := opened(t, p, settings)
			defer client.Close()

			said(t, conn)
			if err := conn.WriteFrame(wire.KindItem, []byte("still")); err != nil {
				t.Errorf("watcher %d asking: %v", at, err)
				return
			}
			_, body := said(t, conn)
			stills[at] = string(body)

			if err := conn.WriteFrame(wire.KindEnd, nil); err != nil {
				t.Errorf("watcher %d ending: %v", at, err)
			}
			<-done
		}()
	}
	wg.Wait()

	// Every line of a still came from one capture, and the two watchers watched two of them.
	for at, still := range stills {
		lines := strings.Fields(still)
		if len(lines) != 3 {
			t.Fatalf("watcher %d got %d lines of a still: %q", at, len(lines), still)
		}
		for _, line := range lines {
			if line != lines[0] {
				t.Fatalf("watcher %d got a still made of %q", at, still)
			}
		}
	}
	if stills[0] == stills[1] {
		t.Fatalf("both watchers were handed the same capture: %q", stills[0])
	}
}

// stub is an archetype of this build's own, to be shadowed and not shadowed.
type stub struct{}

func (stub) Name() string                              { return "chat" }
func (stub) Version() int                              { return 1 }
func (stub) Read(arch.Declared) (arch.Config, error)   { return nil, nil }
func (stub) Note(arch.Config) arch.Note                { return arch.Note{} }
func (stub) Serve(context.Context, arch.Session) error { return nil }

// settle waits for goroutines that are on their way out to finish going.
func settle(t *testing.T) {
	t.Helper()

	was := runtime.NumGoroutine()
	for range 100 {
		runtime.Gosched()
		time.Sleep(5 * time.Millisecond)
		now := runtime.NumGoroutine()
		if now == was {
			return
		}
		was = now
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var out []byte
	for ; n > 0; n /= 10 {
		out = append([]byte{byte('0' + n%10)}, out...)
	}
	return string(out)
}
