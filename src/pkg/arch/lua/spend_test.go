package lua

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bresilla/drop/src/pkg/arch"
	"github.com/bresilla/drop/src/pkg/ns"
	"github.com/bresilla/drop/src/pkg/wire"
)

// ended waits for a session to finish, and says so rather than hanging when it does not.
func ended(t *testing.T, done <-chan error, within time.Duration) error {
	t.Helper()

	select {
	case err := <-done:
		return err
	case <-time.After(within):
		t.Fatalf("the session was still running %s later", within)
		return nil
	}
}

// until waits for a file to appear, so that what a running process is doing can be waited on rather
// than guessed at.
func until(t *testing.T, name string) {
	t.Helper()

	for range 200 {
		if _, err := os.Stat(name); err == nil {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("%s never appeared", name)
}

// A process a plugin starts takes what it started with it, and the session is not held by whatever
// it left behind.
func TestAProcessTakesWhatItStartedWithIt(t *testing.T) {
	p := written(t, `
		drop.archetype{
			name  = "starter",
			read  = function(d) return {} end,
			note  = function(c) return {} end,
			serve = function(s, c)
				local ok, said = pcall(function()
					return s:run{ "sh", "-c", "(sleep 4; echo late > late) & echo started" }
				end)
				s:write(tostring(ok) .. " " .. tostring(said))
			end,
		}
	`)

	began := time.Now()
	conn, client, done := opened(t, p, nil)
	defer client.Close()

	_, body := said(t, conn)
	took := time.Since(began)
	if err := ended(t, done, 10*time.Second); err != nil {
		t.Fatalf("Serve(): %v", err)
	}
	if took > Lingering+5*time.Second {
		t.Fatalf("s:run took %s to come back from a command that left something running", took)
	}
	if !strings.HasPrefix(string(body), "false ") {
		t.Fatalf("a command that left something behind holding its output said %q", body)
	}

	// Long enough for what was left behind to have written, if it is still alive.
	time.Sleep(5 * time.Second)
	own := filepath.Join(p.keeps, p.name, slug("/thing"))
	if _, err := os.Stat(filepath.Join(own, "late")); err == nil {
		t.Fatal("what the command left behind outlived the session and wrote its file")
	}
}

// Cancelling a session kills everything the plugin started, and not only what it started directly.
func TestCancellingASessionKillsTheWholeGroup(t *testing.T) {
	p := written(t, `
		drop.archetype{
			name  = "sleeper",
			read  = function(d) return {} end,
			note  = function(c) return {} end,
			serve = function(s, c)
				s:run{ "sh", "-c", "(sleep 4; echo late > late) & echo up > up; sleep 30" }
			end,
		}
	`)

	ctx, stop := context.WithCancel(t.Context())
	client, server := net.Pipe()
	defer client.Close()

	done := make(chan error, 1)
	go func() {
		defer server.Close()
		done <- p.Serve(ctx, arch.Session{
			Path:   "/thing",
			Who:    ns.Caller{ID: "aaaa", Name: "laptop", Paired: true},
			Conn:   wire.NewConn(server),
			Stream: deadlined{server},
		})
	}()

	// Cancelled while the command is running, which is the only moment any of this is about.
	own := filepath.Join(p.keeps, p.name, slug("/thing"))
	until(t, filepath.Join(own, "up"))
	stop()

	if err := ended(t, done, 10*time.Second); err == nil {
		t.Fatal("a cancelled session came back with no error")
	}

	// Long enough for what the plugin started underneath to have written, if it is still alive.
	time.Sleep(5 * time.Second)
	if _, err := os.Stat(filepath.Join(own, "late")); err == nil {
		t.Fatal("the group outlived the session and wrote its file")
	}
}

// A process a plugin starts gets an environment chosen here, and not the one the daemon is running
// under.
func TestAProcessGetsAChosenEnvironment(t *testing.T) {
	t.Setenv("DROP_TEST_SECRET", "the-owners-secret")

	p := written(t, `
		drop.archetype{
			name  = "prier",
			read  = function(d) return {} end,
			note  = function(c) return {} end,
			serve = function(s, c) s:write(s:run{ "/usr/bin/env" }) end,
		}
	`)

	conn, client, done := opened(t, p, nil)
	defer client.Close()

	_, body := said(t, conn)
	if err := ended(t, done, 10*time.Second); err != nil {
		t.Fatalf("Serve(): %v", err)
	}
	if strings.Contains(string(body), "the-owners-secret") {
		t.Fatalf("a plugin read what the owner exported: %s", body)
	}

	own := filepath.Join(p.keeps, p.name, slug("/thing"))
	for _, wanted := range []string{"PATH=", "HOME=" + own} {
		if !strings.Contains(string(body), wanted) {
			t.Errorf("a process was started without %s: %s", wanted, body)
		}
	}
}

// A session may hold only so many files open, because a descriptor belongs to the whole daemon.
func TestASessionMayHoldOnlySoManyFilesOpen(t *testing.T) {
	p := written(t, `
		drop.archetype{
			name  = "hoarder",
			read  = function(d) return {} end,
			note  = function(c) return {} end,
			serve = function(s, c)
				local held = {}
				for n = 1, 5000 do
					local ok, f = pcall(function() return s:open("f" .. n, "w") end)
					if not ok then
						s:write("stopped at " .. n)
						return
					end
					held[n] = f
				end
				s:write("no bound")
			end,
		}
	`)

	conn, client, done := opened(t, p, nil)
	defer client.Close()

	_, body := said(t, conn)
	if err := ended(t, done, 30*time.Second); err != nil {
		t.Fatalf("Serve(): %v", err)
	}
	if want := "stopped at " + itoa(MaxOpen+1); string(body) != want {
		t.Fatalf("holding files open said %q, wanted %q", body, want)
	}
}

// Closing a file gives its place back, so a plugin that opens and closes in a loop keeps working.
func TestClosingAFileGivesItsPlaceBack(t *testing.T) {
	p := written(t, `
		drop.archetype{
			name  = "tidy",
			read  = function(d) return {} end,
			note  = function(c) return {} end,
			serve = function(s, c)
				for n = 1, 500 do
					local f = s:open("f", "w")
					f:write("x")
					f:close()
				end
				s:write("all of them")
			end,
		}
	`)

	conn, client, done := opened(t, p, nil)
	defer client.Close()

	if _, body := said(t, conn); string(body) != "all of them" {
		t.Fatalf("opening and closing said %q", body)
	}
	if err := ended(t, done, 30*time.Second); err != nil {
		t.Fatalf("Serve(): %v", err)
	}
}

// A name that is not a plain file is refused rather than waited on, because a fifo with nobody at
// the other end waits in the kernel where no budget can reach it.
func TestOpeningSomethingThatIsNotAFileIsRefused(t *testing.T) {
	p := written(t, `
		drop.archetype{
			name  = "piper",
			read  = function(d) return {} end,
			note  = function(c) return {} end,
			serve = function(s, c)
				s:run{ "sh", "-c", "rm -f pipe; mkfifo pipe" }
				for _, how in ipairs({ "r", "w", "a" }) do
					local ok, said = pcall(function() return s:open("pipe", how) end)
					s:write(how .. " " .. tostring(ok))
				end
			end,
		}
	`)

	conn, client, done := opened(t, p, nil)
	defer client.Close()

	for _, how := range []string{"r", "w", "a"} {
		back := make(chan []byte, 1)
		go func() {
			_, body := said(t, conn)
			back <- body
		}()
		select {
		case body := <-back:
			if want := how + " false"; string(body) != want {
				t.Fatalf("opening a fifo %q said %q", how, body)
			}
		case <-time.After(10 * time.Second):
			t.Fatalf("opening a fifo %q never came back", how)
		}
	}
	if err := ended(t, done, 10*time.Second); err != nil {
		t.Fatalf("Serve(): %v", err)
	}
}

// Starting a process costs the session's budget, so a loop that starts them runs out the way a loop
// that counts does.
func TestStartingProcessesRunsOutOfBudget(t *testing.T) {
	p := written(t, `
		drop.archetype{
			name  = "forker",
			read  = function(d) return {} end,
			note  = function(c) return {} end,
			serve = function(s, c) while true do s:run{ "/bin/true" } end end,
		}
	`)

	_, client, done := opened(t, p, nil)
	defer client.Close()

	err := ended(t, done, 60*time.Second)
	if err == nil || !strings.Contains(err.Error(), "CPU") {
		t.Fatalf("a loop of processes ended with %v", err)
	}
}

// Writing costs the session's budget too, so a plugin cannot fill a disk in a session.
func TestWritingRunsOutOfBudget(t *testing.T) {
	p := written(t, `
		drop.archetype{
			name  = "filler",
			read  = function(d) return {} end,
			note  = function(c) return {} end,
			serve = function(s, c)
				local f = s:open("big", "w")
				local chunk = string.rep("x", 1048576)
				for n = 1, 100 do f:write(chunk) end
			end,
		}
	`)

	_, client, done := opened(t, p, nil)
	defer client.Close()

	err := ended(t, done, 60*time.Second)
	if err == nil || !strings.Contains(err.Error(), "CPU") {
		t.Fatalf("a loop of writes ended with %v", err)
	}

	own := filepath.Join(p.keeps, p.name, slug("/thing"))
	said, err := os.Stat(filepath.Join(own, "big"))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if said.Size() > sessionSteps+(1<<20) {
		t.Fatalf("a session wrote %d bytes", said.Size())
	}
}

// A name a session makes its own is nobody else's, and it goes when the session does.
func TestANameOfItsOwnIsNobodyElses(t *testing.T) {
	p := written(t, `
		drop.archetype{
			name  = "namer",
			read  = function(d) return {} end,
			note  = function(c) return {} end,
			serve = function(s, c)
				local name = s:mine("still.jpg")
				if s:mine("still.jpg") ~= name then error("the same name twice gave two names") end
				local f = s:open(name, "w")
				f:write("mine")
				f:close()
				s:write(name)
				s:read()
			end,
		}
	`)

	var names []string
	for range 2 {
		conn, client, done := opened(t, p, nil)
		_, body := said(t, conn)
		names = append(names, string(body))
		client.Close()
		<-done
	}

	if names[0] == names[1] {
		t.Fatalf("two sessions both called their file %q", names[0])
	}
	own := filepath.Join(p.keeps, p.name, slug("/thing"))
	for _, name := range names {
		if _, err := os.Stat(filepath.Join(own, name)); err == nil {
			t.Errorf("%s outlived the session that named it", name)
		}
	}
}

// A machine with nowhere to keep files says so, rather than keeping them wherever the daemon was
// started from.
func TestNowhereToKeepFilesIsRefused(t *testing.T) {
	t.Chdir(t.TempDir())

	p := written(t, `
		drop.archetype{
			name  = "keeper",
			read  = function(d) return {} end,
			note  = function(c) return {} end,
			serve = function(s, c)
				local ok, said = pcall(function() return s:open("mine.txt", "w") end)
				s:write(tostring(ok) .. " " .. tostring(said))
			end,
		}
	`)
	p.keeps = ""

	conn, client, done := opened(t, p, nil)
	defer client.Close()

	_, body := said(t, conn)
	if err := ended(t, done, 10*time.Second); err != nil {
		t.Fatalf("Serve(): %v", err)
	}
	if !strings.HasPrefix(string(body), "false ") || !strings.Contains(string(body), "nowhere") {
		t.Fatalf("a plugin with nowhere to keep files said %q", body)
	}
	if _, err := os.Stat(filepath.Join("keeper", slug("/thing"))); err == nil {
		t.Fatal("files landed in the directory the daemon was started from")
	}
}
