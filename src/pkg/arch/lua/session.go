package lua

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	rt "github.com/arnodel/golua/runtime"

	"github.com/bresilla/drop/src/pkg/arch"
	"github.com/bresilla/drop/src/pkg/wire"
)

// MaxFile bounds one read out of a namespace's own directory, so naming a big file cannot make the
// daemon hold a disk in memory.
const MaxFile = 8 << 20

// MaxSaid bounds what a process a plugin starts may say back.
const MaxSaid = 1 << 20

// MaxOpen is how many files one session may hold at once.
//
// A descriptor belongs to the whole process and not to the session that took it, so a plugin that
// opens a file for every frame and forgets to close it would leave the daemon with none: no stream,
// no store, no config. Sixty-four is more than a plugin has a reason to hold.
const MaxOpen = 64

// Waiting is how long a process a plugin starts may take before it is killed.
const Waiting = 30 * time.Second

// Lingering is how long its output is waited for once the process itself is gone, which is a wait
// at all only when it left something behind holding the other end.
const Lingering = 2 * time.Second

// wants is what a plugin has stopped for. Everything else it asks for is answered where it stands.
type wants byte

const (
	wantNothing wants = iota
	wantRead
	wantWrite
)

// session is one namespace open, as the plugin holds it.
//
// The fields either side of the yield are read on two goroutines and never at once: a yield hands
// control to the driver and a resume hands it back, and neither runs while the other does.
type session struct {
	ctx context.Context
	at  arch.Session
	// where is the directory this namespace keeps its own files in, and dir is that directory
	// opened. Nothing is made on disk until a plugin asks for a file.
	where string
	dir   *os.Root
	// open is every file the plugin has open, closed with the session.
	open []*os.File
	// mark is what makes a name this session's own, and what is swept up after it.
	mark string

	// want is what the plugin stopped for, with body and kind for a frame going out.
	want wants
	body []byte
	kind byte
	// gave, was and more are the answer: a frame, what kind it was, and whether there was one.
	gave []byte
	was  byte
	more bool
}

// drive runs the plugin's serve as a coroutine and answers whatever it stops for.
//
// The plugin writes straight-line code and this decides when it runs. Every wait happens out here,
// on the far side of a yield, where the session's budget is not being charged for standing still.
func (s *session) drive(w *world, serve rt.Callable) error {
	main := w.lua.MainThread()

	th := rt.NewThread(w.lua)
	// A coroutine left suspended holds its goroutine for ever. Closing it is what ends that one.
	defer th.Close(main)

	th.Start(serve)
	args := []rt.Value{s.value(w.lua), value(s.at.Config)}

	for {
		if _, err := th.Resume(main, args); err != nil {
			return err
		}
		if th.Status() == rt.ThreadDead {
			return nil
		}
		if err := s.answer(); err != nil {
			return err
		}
		args = nil
	}
}

// answer does the one thing the plugin stopped for.
func (s *session) answer() error {
	if err := s.ctx.Err(); err != nil {
		return err
	}

	switch s.want {
	case wantRead:
		kind, body, err := s.at.Conn.ReadFrame()
		switch {
		case wire.Closed(err), err == nil && kind == wire.KindEnd:
			s.gave, s.was, s.more = nil, 0, false
		case err != nil:
			return fmt.Errorf("reading a frame on %s: %w", s.at.Path, err)
		default:
			s.gave, s.was, s.more = body, kind, true
		}

	case wantWrite:
		if err := s.at.Conn.WriteFrame(s.kind, s.body); err != nil {
			return fmt.Errorf("writing a frame on %s: %w", s.at.Path, err)
		}
	}

	s.want, s.body = wantNothing, nil
	return nil
}

// shut closes what the session left open and takes away what it named its own.
func (s *session) shut() {
	for _, file := range s.open {
		file.Close()
	}
	if s.dir != nil {
		s.sweep()
		s.dir.Close()
	}
}

// sweep removes everything in the namespace's directory carrying this session's mark.
func (s *session) sweep() {
	if s.mark == "" {
		return
	}
	dir, err := s.dir.Open(".")
	if err != nil {
		return
	}
	names, _ := dir.Readdirnames(-1)
	dir.Close()

	for _, name := range names {
		if strings.HasSuffix(name, "."+s.mark) {
			_ = s.dir.Remove(name)
		}
	}
}

// value is the session as the plugin holds it: a thing with methods, and nothing to read out of it.
func (s *session) value(machine *rt.Runtime) rt.Value {
	on := rt.NewTable()
	machine.SetEnvGoFunc(on, "read", guarded("s:read", s.read), 1, false).SolemnlyDeclareCompliance(safe)
	machine.SetEnvGoFunc(on, "write", guarded("s:write", s.write), 3, false).SolemnlyDeclareCompliance(safe)
	machine.SetEnvGoFunc(on, "who", guarded("s:who", s.who), 1, false).SolemnlyDeclareCompliance(safe)
	machine.SetEnvGoFunc(on, "path", guarded("s:path", s.path), 1, false).SolemnlyDeclareCompliance(safe)
	machine.SetEnvGoFunc(on, "open", guarded("s:open", s.opens), 3, false).SolemnlyDeclareCompliance(safe)
	machine.SetEnvGoFunc(on, "mine", guarded("s:mine", s.mine), 2, false).SolemnlyDeclareCompliance(safe)
	machine.SetEnvGoFunc(on, "run", guarded("s:run", s.runs), 2, false).SolemnlyDeclareCompliance(safe)

	meta := rt.NewTable()
	machine.SetEnv(meta, "__index", rt.TableValue(on))
	return rt.UserDataValue(rt.NewUserData(s, meta))
}

// read is `s:read()`: the next frame and what kind it was, and nothing at all once the far end has
// finished.
func (s *session) read(t *rt.Thread, c *rt.GoCont) (rt.Cont, error) {
	s.want = wantRead
	if _, err := t.Yield(nil); err != nil {
		return nil, err
	}
	if !s.more {
		return c.Next(), nil
	}

	// What arrived is charged to the session, so a plugin that hoovers up everything sent to it
	// runs out the way one that makes it up runs out.
	t.RequireBytes(len(s.gave))

	next := c.Next()
	t.Push1(next, rt.StringValue(string(s.gave)))
	t.Push1(next, rt.StringValue(wordOf[s.was]))
	return next, nil
}

// write is `s:write(bytes)`, or `s:write(bytes, kind)` for a plugin speaking somebody else's
// protocol.
func (s *session) write(t *rt.Thread, c *rt.GoCont) (rt.Cont, error) {
	if err := c.CheckNArgs(2); err != nil {
		return nil, err
	}
	body, err := c.StringArg(1)
	if err != nil {
		return nil, err
	}

	kind := wire.KindItem
	if c.NArgs() > 2 {
		word, err := c.StringArg(2)
		if err != nil {
			return nil, err
		}
		kind, err = kindOf(word)
		if err != nil {
			return nil, err
		}
	}

	s.want, s.body, s.kind = wantWrite, []byte(body), kind
	if _, err := t.Yield(nil); err != nil {
		return nil, err
	}
	return c.Next(), nil
}

// who is `s:who()`: who is calling, and what this machine calls them.
func (s *session) who(t *rt.Thread, c *rt.GoCont) (rt.Cont, error) {
	out := rt.NewTable()
	out.Set(rt.StringValue("id"), rt.StringValue(s.at.Who.ID))
	out.Set(rt.StringValue("name"), rt.StringValue(s.at.Who.Name))
	out.Set(rt.StringValue("label"), rt.StringValue(s.at.Who.Label))
	out.Set(rt.StringValue("person"), rt.StringValue(s.at.Who.UserName))
	out.Set(rt.StringValue("paired"), rt.BoolValue(s.at.Who.Paired))
	out.Set(rt.StringValue("trusted"), rt.BoolValue(s.at.Who.Trusted))
	return c.PushingNext1(t.Runtime, rt.TableValue(out)), nil
}

// path is `s:path()`: the namespace this session is on.
func (s *session) path(t *rt.Thread, c *rt.GoCont) (rt.Cont, error) {
	return c.PushingNext1(t.Runtime, rt.StringValue(s.at.Path)), nil
}

// opens is `s:open(name)`, or `s:open(name, "w")` to write one.
//
// The name is a name and never a path: it is resolved inside a directory this process holds open,
// one component at a time, and leaves it for nothing — no link out, no dot-dot, and nothing that
// appears between the check and the open.
func (s *session) opens(t *rt.Thread, c *rt.GoCont) (rt.Cont, error) {
	if err := c.CheckNArgs(2); err != nil {
		return nil, err
	}
	name, err := c.StringArg(1)
	if err != nil {
		return nil, err
	}

	how := "r"
	if c.NArgs() > 2 {
		if how, err = c.StringArg(2); err != nil {
			return nil, err
		}
	}

	if len(s.open) >= MaxOpen {
		return nil, fmt.Errorf("this session holds %d files open already, which is as many as it may", MaxOpen)
	}
	dir, err := s.under()
	if err != nil {
		return nil, err
	}
	t.RequireCPU(costOpen)

	file, err := opening(dir, name, how)
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", name, err)
	}
	s.open = append(s.open, file)

	return c.PushingNext1(t.Runtime, s.holding(t.Runtime, file)), nil
}

// mine is `s:mine(name)`: that name, made this session's own.
//
// Every session of one namespace keeps its files in one directory, so two of them running the same
// command into the same name write over each other's work and read back the halves. This gives the
// name back carrying a mark no other session has, and what carries that mark goes when the session
// does. The same name asked for twice is the same name both times.
func (s *session) mine(t *rt.Thread, c *rt.GoCont) (rt.Cont, error) {
	if err := c.CheckNArgs(2); err != nil {
		return nil, err
	}
	name, err := c.StringArg(1)
	if err != nil {
		return nil, err
	}

	if s.mark == "" {
		var seed [6]byte
		if _, err := rand.Read(seed[:]); err != nil {
			return nil, fmt.Errorf("naming a file only %s uses: %w", s.at.Path, err)
		}
		s.mark = hex.EncodeToString(seed[:])
	}
	return c.PushingNext1(t.Runtime, rt.StringValue(name+"."+s.mark)), nil
}

// runs is `s:run{ "program", "argument" }`: a process, its output read back.
//
// It is given an environment chosen here rather than the one this daemon is running under, because
// a plugin that can start a program must not thereby be a plugin that can read everything the owner
// exported. What it cannot have is for ever: it and everything it starts are one process group,
// killed with the session and killed anyway once it has taken longer than anybody would wait.
func (s *session) runs(t *rt.Thread, c *rt.GoCont) (rt.Cont, error) {
	if err := c.CheckNArgs(2); err != nil {
		return nil, err
	}
	list, err := c.TableArg(1)
	if err != nil {
		return nil, err
	}
	argv := words(list)
	if len(argv) == 0 {
		return nil, fmt.Errorf("a command is a program and its arguments, and this one names no program")
	}
	if _, err := s.under(); err != nil {
		return nil, err
	}
	t.RequireCPU(costRun)

	ctx, stop := context.WithTimeout(s.ctx, Waiting)
	defer stop()

	said := &capped{left: MaxSaid}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir, cmd.Stdout, cmd.Stderr = s.where, said, said
	cmd.Env = environ(s.where)

	// The program and whatever it starts are one process group, so ending it ends all of them, and
	// output is waited for only so long once the program itself has gone.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return ending(cmd) }
	cmd.WaitDelay = Lingering

	if err := cmd.Run(); err != nil {
		// The group outlasted its leader, which is what holding the output means, so there is still
		// a group of that number to send away.
		if errors.Is(err, exec.ErrWaitDelay) {
			_ = ending(cmd)
			return nil, fmt.Errorf("running %s: it left something behind holding its output", argv[0])
		}
		return nil, fmt.Errorf("running %s: %w", argv[0], err)
	}
	t.RequireBytes(len(said.body))

	return c.PushingNext1(t.Runtime, rt.StringValue(string(said.body))), nil
}

// ending sends a command's whole process group away. A group that has already gone is the command
// having finished of its own accord, which is not a failure to cancel it.
func ending(cmd *exec.Cmd) error {
	err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	if errors.Is(err, syscall.ESRCH) {
		return os.ErrProcessDone
	}
	return err
}

// environ is what a process a plugin starts is given: a way to find a program, and a home of its
// own inside the directory the namespace already keeps its files in.
func environ(home string) []string {
	path := os.Getenv("PATH")
	if path == "" {
		path = "/usr/local/bin:/usr/bin:/bin"
	}
	return []string{"PATH=" + path, "HOME=" + home}
}

// under is where this namespace keeps its files, opened the first time a plugin asks for one.
func (s *session) under() (*os.Root, error) {
	if s.dir != nil {
		return s.dir, nil
	}
	// Absolute rather than merely present: with no data directory to join to, a name joins to
	// nothing and comes out a relative one, which would be resolved against wherever the daemon
	// happened to be started from.
	if !filepath.IsAbs(s.where) {
		return nil, fmt.Errorf("this machine has nowhere for %s to keep files", s.at.Path)
	}
	if err := os.MkdirAll(s.where, 0o700); err != nil {
		return nil, fmt.Errorf("making somewhere for %s to keep files: %w", s.at.Path, err)
	}

	dir, err := os.OpenRoot(s.where)
	if err != nil {
		return nil, fmt.Errorf("opening somewhere for %s to keep files: %w", s.at.Path, err)
	}
	s.dir = dir
	return dir, nil
}

// opening is one file inside the namespace's directory, in the one of three ways a plugin may ask
// for it.
//
// Opened without waiting, and only if it turns out to be a plain file. A fifo with nobody at the
// other end waits in the kernel, where the session's budget, the timeout and the cancellation all
// reach a host function that is no longer running lua and cannot be told anything.
func opening(dir *os.Root, name, how string) (*os.File, error) {
	var flag int
	switch how {
	case "r":
		flag = os.O_RDONLY
	case "w":
		flag = os.O_WRONLY | os.O_CREATE | os.O_TRUNC
	case "a":
		flag = os.O_WRONLY | os.O_CREATE | os.O_APPEND
	default:
		return nil, fmt.Errorf("%q is not a way to open a file: r, w or a", how)
	}

	file, err := dir.OpenFile(name, flag|syscall.O_NONBLOCK, 0o600)
	if err != nil {
		return nil, err
	}
	said, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, err
	}
	if !said.Mode().IsRegular() {
		file.Close()
		return nil, errors.New("not a plain file")
	}
	return file, nil
}

// holding is an open file as the plugin holds it.
func (s *session) holding(machine *rt.Runtime, file *os.File) rt.Value {
	on := rt.NewTable()
	machine.SetEnvGoFunc(on, "read", guarded("f:read", reading(file)), 2, false).SolemnlyDeclareCompliance(safe)
	machine.SetEnvGoFunc(on, "write", guarded("f:write", writing(file)), 2, false).SolemnlyDeclareCompliance(safe)
	machine.SetEnvGoFunc(on, "close", guarded("f:close", s.closing(file)), 1, false).SolemnlyDeclareCompliance(safe)

	meta := rt.NewTable()
	machine.SetEnv(meta, "__index", rt.TableValue(on))
	return rt.UserDataValue(rt.NewUserData(file, meta))
}

// reading is `f:read()` for the rest of the file, or `f:read(n)` for that many bytes. Nothing at
// all means the file is finished.
func reading(file *os.File) rt.GoFunctionFunc {
	return func(t *rt.Thread, c *rt.GoCont) (rt.Cont, error) {
		want := int64(MaxFile)
		if c.NArgs() > 1 {
			n, err := c.IntArg(1)
			if err != nil {
				return nil, err
			}
			if n < 0 || n > MaxFile {
				return nil, fmt.Errorf("a read of %d bytes, and %d is as much as one may take", n, MaxFile)
			}
			want = n
		}

		body, err := io.ReadAll(io.LimitReader(file, want))
		if err != nil {
			return nil, fmt.Errorf("reading: %w", err)
		}
		if len(body) == 0 {
			return c.Next(), nil
		}
		t.RequireBytes(len(body))

		return c.PushingNext1(t.Runtime, rt.StringValue(string(body))), nil
	}
}

func writing(file *os.File) rt.GoFunctionFunc {
	return func(t *rt.Thread, c *rt.GoCont) (rt.Cont, error) {
		if err := c.CheckNArgs(2); err != nil {
			return nil, err
		}
		body, err := c.StringArg(1)
		if err != nil {
			return nil, err
		}
		t.RequireCPU(costWrite * uint64(len(body)))

		if _, err := file.WriteString(body); err != nil {
			return nil, fmt.Errorf("writing: %w", err)
		}
		return c.Next(), nil
	}
}

func (s *session) closing(file *os.File) rt.GoFunctionFunc {
	return func(t *rt.Thread, c *rt.GoCont) (rt.Cont, error) {
		if err := file.Close(); err != nil {
			return nil, fmt.Errorf("closing: %w", err)
		}
		s.forget(file)
		return c.Next(), nil
	}
}

// forget drops a file the plugin closed, so closing one gives its place back.
func (s *session) forget(file *os.File) {
	for i, held := range s.open {
		if held == file {
			s.open = append(s.open[:i], s.open[i+1:]...)
			return
		}
	}
}

// capped takes what a process says up to a limit and throws the rest away, so a program that never
// stops talking cannot make the daemon hold all of it.
type capped struct {
	body []byte
	left int
}

func (c *capped) Write(p []byte) (int, error) {
	if take := min(c.left, len(p)); take > 0 {
		c.body = append(c.body, p[:take]...)
		c.left -= take
	}
	return len(p), nil
}

// words reads a Lua list of strings, stopping at the first hole.
func words(list *rt.Table) []string {
	var out []string
	for i := int64(1); ; i++ {
		word, ok := list.Get(rt.IntValue(i)).TryString()
		if !ok {
			return out
		}
		out = append(out, word)
	}
}

// kinds is what a frame is called in a plugin.
//
// A plugin that invented its own protocol never says any of these and gets an item every time. One
// that speaks a protocol somebody else wrote needs the words that protocol uses, which is what lets
// a machine without the plugin open a namespace of it at all.
var kinds = map[string]byte{
	"open":    wire.KindOpen,
	"accept":  wire.KindAccept,
	"reject":  wire.KindReject,
	"item":    wire.KindItem,
	"data":    wire.KindData,
	"end":     wire.KindEnd,
	"ack":     wire.KindAck,
	"resize":  wire.KindResize,
	"ping":    wire.KindPing,
	"pong":    wire.KindPong,
	"request": wire.KindRequest,
	"reply":   wire.KindReply,
}

// wordOf is the same the other way round, for saying what arrived.
var wordOf = spoken(kinds)

func spoken(of map[string]byte) map[byte]string {
	out := make(map[byte]string, len(of))
	for word, kind := range of {
		out[kind] = word
	}
	return out
}

func kindOf(word string) (byte, error) {
	kind, ok := kinds[word]
	if !ok {
		return 0, fmt.Errorf("%q is not a kind of frame", word)
	}
	return kind, nil
}
