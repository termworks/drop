package files

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"math"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/bresilla/drop/src/pkg/arch"
	"github.com/bresilla/drop/src/pkg/node"
	"github.com/bresilla/drop/src/pkg/wire"
)

func TestAStoppedFilesWatcherSaysItIsFinished(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	done := New(Into{}).Watch(ctx, nil)

	select {
	case <-done:
		t.Fatal("the watcher stopped before its context")
	default:
	}

	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("the watcher did not finish after its context stopped")
	}
}

// readWriter is the two halves of a stream a test has in two buffers.
type readWriter struct {
	io.Reader
	io.Writer
}

// serving runs a files namespace over a pipe and hands back the caller's side of the stream.
func serving(t *testing.T, dir string, writable bool, hooks Into) *wire.Conn {
	t.Helper()

	caller, server := net.Pipe()
	t.Cleanup(func() { _ = caller.Close() })

	go func() {
		defer func() { _ = server.Close() }()

		at := arch.Session{
			Path:   "/files",
			Config: Config{Dir: dir, Writable: writable},
			Conn:   wire.NewConn(server),
		}
		_ = New(hooks).Serve(t.Context(), at)
	}()

	return wire.NewConn(caller)
}

// opened runs a files namespace and walks it.
func opened(t *testing.T, dir string, writable bool, hooks Into) *Browsing {
	t.Helper()

	b, err := Browse(serving(t, dir, writable, hooks))
	if err != nil {
		t.Fatalf("Browse(): %v", err)
	}
	return b
}

func read(t *testing.T, path string) []byte {
	t.Helper()

	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading back %s: %v", path, err)
	}
	return body
}

func TestBrowseListsAndReadsAFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("what happened"), 0o644); err != nil {
		t.Fatalf("writing the file: %v", err)
	}
	if err := os.Mkdir(filepath.Join(dir, "deeper"), 0o755); err != nil {
		t.Fatalf("making the directory: %v", err)
	}

	b := opened(t, dir, false, Into{})
	if b.Writable() {
		t.Error("a read-only mount said it was writable")
	}

	entries, err := b.List("")
	if err != nil {
		t.Fatalf("List(): %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("List() gave %d entries, want 2", len(entries))
	}

	byName := map[string]Entry{}
	for _, e := range entries {
		byName[e.Name] = e
	}
	if got := byName["notes.txt"]; got.Dir || got.Size != int64(len("what happened")) {
		t.Errorf("notes.txt = %+v", got)
	}
	if !byName["deeper"].Dir {
		t.Error("deeper did not come back as a directory")
	}

	into := filepath.Join(t.TempDir(), "copy.txt")
	if err := b.Get("notes.txt", into, Want{}); err != nil {
		t.Fatalf("Get(): %v", err)
	}
	if got := read(t, into); string(got) != "what happened" {
		t.Errorf("the file arrived as %q", got)
	}
}

// The session outlives one operation: that is the whole reason for request and reply rounds.
func TestBrowseKeepsGoingAfterEveryRound(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"one", "two"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(name), 0o644); err != nil {
			t.Fatalf("writing %s: %v", name, err)
		}
	}

	b := opened(t, dir, false, Into{})
	landing := t.TempDir()

	for range 2 {
		if _, err := b.List(""); err != nil {
			t.Fatalf("List(): %v", err)
		}
		for _, name := range []string{"one", "two"} {
			if err := b.Get(name, filepath.Join(landing, name), Want{}); err != nil {
				t.Fatalf("Get(%q): %v", name, err)
			}
		}
	}
	if err := b.Get("missing", filepath.Join(landing, "missing"), Want{}); err == nil {
		t.Fatal("Get() of a file that is not there succeeded")
	}
	if _, err := b.List(""); err != nil {
		t.Fatalf("List() after a refusal: %v", err)
	}
}

func TestBrowseWritesWhenTheMountSaysSo(t *testing.T) {
	dir := t.TempDir()
	b := opened(t, dir, true, Into{})

	if !b.Writable() {
		t.Fatal("a writable mount said it was not")
	}
	if err := b.Mkdir("uploads"); err != nil {
		t.Fatalf("Mkdir(): %v", err)
	}

	sent := "the numbers"
	if err := b.Put("uploads/report", strings.NewReader(sent), Given{Size: int64(len(sent)), Mode: 0o777}); err != nil {
		t.Fatalf("Put(): %v", err)
	}

	landed := filepath.Join(dir, "uploads", "report")
	if got := read(t, landed); string(got) != sent {
		t.Errorf("the upload landed as %q", got)
	}

	// The sender is trusted for one bit: whether the thing runs. The rest of the mode is ours.
	stat, err := os.Stat(landed)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := stat.Mode().Perm(); perm != 0o700 {
		t.Errorf("the upload landed as %o, want 700", perm)
	}
	if stat, err := os.Stat(filepath.Join(dir, "uploads")); err != nil || stat.Mode().Perm() != 0o700 {
		t.Errorf("the directory was made as %v", stat.Mode())
	}

	if err := b.Move("uploads/report", "uploads/final"); err != nil {
		t.Fatalf("Move(): %v", err)
	}
	if err := b.Remove("uploads/final"); err != nil {
		t.Fatalf("Remove(): %v", err)
	}
	if err := b.Remove("uploads"); err != nil {
		t.Fatalf("Remove() of the directory: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "uploads")); err == nil {
		t.Error("the directory is still there")
	}
}

// The mount flag is the only thing that permits a write, and it permits all of them or none.
func TestBrowseRefusesEveryWriteOnAReadOnlyMount(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "kept"), []byte("."), 0o644); err != nil {
		t.Fatalf("writing the file: %v", err)
	}

	b := opened(t, dir, false, Into{})

	if err := b.Put("sneaky", strings.NewReader("x"), Given{Size: 1, Mode: 0o644}); err == nil {
		t.Error("Put() worked on a read-only namespace")
	}
	if err := b.Remove("kept"); err == nil {
		t.Error("Remove() worked on a read-only namespace")
	}
	if err := b.Mkdir("new"); err == nil {
		t.Error("Mkdir() worked on a read-only namespace")
	}
	if err := b.Move("kept", "moved"); err == nil {
		t.Error("Move() worked on a read-only namespace")
	}

	if _, err := os.Stat(filepath.Join(dir, "kept")); err != nil {
		t.Errorf("the file did not survive: %v", err)
	}
	if _, err := b.List(""); err != nil {
		t.Fatalf("List() after four refusals: %v", err)
	}
}

func TestBrowseRefusesAnUploadLargerThanFreeSpace(t *testing.T) {
	dir := t.TempDir()
	b := opened(t, dir, true, Into{})

	err := b.Put("too-large", strings.NewReader(""), Given{Size: math.MaxInt64, Mode: 0o600})
	if err == nil || !strings.Contains(err.Error(), "free space") {
		t.Fatalf("the oversized upload was answered with %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "too-large")); !os.IsNotExist(err) {
		t.Fatalf("the refused upload left a file: %v", err)
	}
}

func TestBrowseReportsAnUploadThatLands(t *testing.T) {
	dir := t.TempDir()

	var name string
	var size int64
	hooks := Into{Landed: func(_ node.ID, n string, s int64) { name, size = n, s }}

	b := opened(t, dir, true, hooks)
	if err := b.Put("log", strings.NewReader("ping"), Given{Size: 4, Mode: 0o644}); err != nil {
		t.Fatalf("Put(): %v", err)
	}
	// One more round, so the note the far end makes after acknowledging has been made.
	if _, err := b.List(""); err != nil {
		t.Fatalf("List(): %v", err)
	}
	if name != "log" || size != 4 {
		t.Errorf("the upload was reported as %q, %d bytes", name, size)
	}
}

// A caller that has finished asking closes the stream. That is how a browse ends, not a fault.
func TestAClosedSessionIsNotAnError(t *testing.T) {
	caller, server := net.Pipe()

	done := make(chan error, 1)
	go func() {
		defer func() { _ = server.Close() }()

		at := arch.Session{
			Path:   "/files",
			Config: Config{Dir: t.TempDir()},
			Conn:   wire.NewConn(server),
		}
		done <- New(Into{}).Serve(t.Context(), at)
	}()

	b, err := Browse(wire.NewConn(caller))
	if err != nil {
		t.Fatalf("Browse(): %v", err)
	}
	if _, err := b.List(""); err != nil {
		t.Fatalf("List(): %v", err)
	}
	_ = caller.Close()

	if err := <-done; err != nil {
		t.Fatalf("a session that was closed came back as %v", err)
	}
}

// A namespace pointed at a directory that is not there is refused where it is opened, and the
// caller is told rather than left waiting.
func TestANamespaceWithNoDirectoryIsRefused(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "not here")
	if _, err := Browse(serving(t, missing, true, Into{})); err == nil {
		t.Fatal("Browse() walked a namespace whose directory does not exist")
	}

	file := filepath.Join(t.TempDir(), "a file")
	if err := os.WriteFile(file, []byte("."), 0o600); err != nil {
		t.Fatalf("writing the file: %v", err)
	}
	if _, err := Browse(serving(t, file, true, Into{})); err == nil {
		t.Fatal("Browse() walked a namespace pointed at a file")
	}
}

// A namespace may be configured through a link. What it stands on is resolved once, when it opens,
// and everything under it is still measured against that.
func TestANamespaceMayBeReachedThroughALink(t *testing.T) {
	real, elsewhere := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(real, "notes"), []byte("inside"), 0o600); err != nil {
		t.Fatalf("writing the file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(elsewhere, "secret"), []byte("outside"), 0o600); err != nil {
		t.Fatalf("writing the file: %v", err)
	}
	if err := os.Symlink(elsewhere, filepath.Join(real, "out")); err != nil {
		t.Fatalf("making the link: %v", err)
	}

	link := filepath.Join(t.TempDir(), "through")
	if err := os.Symlink(real, link); err != nil {
		t.Fatalf("making the link: %v", err)
	}

	b := opened(t, link, false, Into{})
	into := filepath.Join(t.TempDir(), "copy")
	if err := b.Get("notes", into, Want{}); err != nil {
		t.Fatalf("Get() through a namespace reached by a link: %v", err)
	}
	if err := b.Get("out/secret", into, Want{}); err == nil {
		t.Error("Get() read a file outside the namespace")
	}
}

// The one that is usually missed: the path is clean, and the escape is a link on the disk.
func TestALinkOutOfTheNamespaceIsNotFollowed(t *testing.T) {
	dir, elsewhere := t.TempDir(), t.TempDir()
	secret := filepath.Join(elsewhere, "secret")
	if err := os.WriteFile(secret, []byte("the secret"), 0o600); err != nil {
		t.Fatalf("writing the file: %v", err)
	}
	if err := os.Symlink(elsewhere, filepath.Join(dir, "out")); err != nil {
		t.Fatalf("making the link: %v", err)
	}
	if err := os.Symlink(secret, filepath.Join(dir, "one")); err != nil {
		t.Fatalf("making the link: %v", err)
	}

	b := opened(t, dir, true, Into{})
	into := filepath.Join(t.TempDir(), "copy")

	// A link at the last component of a get is the same escape as one halfway along it.
	for _, name := range []string{"out/secret", "one"} {
		if err := b.Get(name, into, Want{}); err == nil {
			t.Errorf("Get(%q) read a file outside the namespace", name)
		}
	}
	if _, err := b.List("out"); err == nil {
		t.Error("List() walked a directory outside the namespace")
	}
	if err := b.Remove("out/secret"); err == nil {
		t.Error("Remove() deleted a file outside the namespace")
	}
	if err := b.Move("out/secret", "moved"); err == nil {
		t.Error("Move() moved a file outside the namespace")
	}

	// A write at the name of a link is something already there, so it lands beside it.
	if err := b.Put("one", strings.NewReader("wrote"), Given{Size: 5, Mode: 0o644}); err != nil {
		t.Fatalf("Put(): %v", err)
	}
	if got := read(t, secret); string(got) != "the secret" {
		t.Errorf("the file outside now says %q", got)
	}
	if got := read(t, filepath.Join(dir, "one-1")); string(got) != "wrote" {
		t.Errorf("the upload landed as %q", got)
	}
}

// A link that stays inside the namespace is not an escape, and is walked like any other name.
func TestALinkThatStaysInsideIsWalked(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "real"), 0o700); err != nil {
		t.Fatalf("making the directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "real", "notes"), []byte("here"), 0o600); err != nil {
		t.Fatalf("writing the file: %v", err)
	}
	if err := os.Symlink("real", filepath.Join(dir, "same")); err != nil {
		t.Fatalf("making the link: %v", err)
	}

	b := opened(t, dir, false, Into{})
	if _, err := b.List("same"); err != nil {
		t.Errorf("List() through a link that stays inside: %v", err)
	}

	into := filepath.Join(t.TempDir(), "copy")
	if err := b.Get("same/notes", into, Want{}); err != nil {
		t.Fatalf("Get() through a link that stays inside: %v", err)
	}
	if got := read(t, into); string(got) != "here" {
		t.Errorf("the file arrived as %q", got)
	}
}

// The namespace itself is not a file. Nothing but a listing may name it, however it is spelled.
func TestTheNamespaceItselfIsNotAName(t *testing.T) {
	dir := t.TempDir()
	b := opened(t, dir, true, Into{})

	for _, name := range []string{"", ".", "./", "sub/.."} {
		if err := b.Remove(name); err == nil {
			t.Errorf("Remove(%q) was allowed", name)
		}
		if err := b.Mkdir(name); err == nil {
			t.Errorf("Mkdir(%q) was allowed", name)
		}
		if err := b.Move(name, "elsewhere"); err == nil {
			t.Errorf("Move(%q) was allowed", name)
		}
		if err := b.Move("thing", name); err == nil {
			t.Errorf("Move() onto %q was allowed", name)
		}
		if err := b.Get(name, filepath.Join(t.TempDir(), "copy"), Want{}); err == nil {
			t.Errorf("Get(%q) was allowed", name)
		}
		if err := b.Put(name, strings.NewReader("x"), Given{Size: 1, Mode: 0o644}); err == nil {
			t.Errorf("Put(%q) was allowed", name)
		}
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("the namespace's own directory did not survive: %v", err)
	}
	if _, err := b.List("."); err != nil {
		t.Errorf("List() of the namespace itself: %v", err)
	}
}

// Only a file is sent down a stream: a socket or a device would be read until the far end gave up.
func TestOnlyARegularFileIsRead(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "socket")
	listening, err := net.Listen("unix", sock)
	if err != nil {
		t.Skipf("no unix sockets here: %v", err)
	}
	defer func() { _ = listening.Close() }()

	b := opened(t, dir, false, Into{})
	if err := b.Get("socket", filepath.Join(t.TempDir(), "copy"), Want{}); err == nil {
		t.Fatal("Get() read something that is not a file")
	}
	if _, err := b.List(""); err != nil {
		t.Fatalf("List() after the refusal: %v", err)
	}
}

// A name nested past anything reasonable is answered, not crashed on.
func TestADeeplyNestedNameIsAnsweredNotFollowed(t *testing.T) {
	dir := t.TempDir()
	b := opened(t, dir, true, Into{})

	deep := strings.Repeat("a/", 400) + "thing"
	if err := b.Get(deep, filepath.Join(t.TempDir(), "copy"), Want{}); err == nil {
		t.Error("Get() of a name 400 deep succeeded")
	}
	if err := b.Mkdir(deep); err == nil {
		t.Error("Mkdir() of a name 400 deep succeeded")
	}
	if _, err := b.List(""); err != nil {
		t.Fatalf("List() after a name 400 deep: %v", err)
	}
}

// A part file is made here or not at all: what is already at the path is neither unlinked nor
// written through.
func TestALinkAtThePartIsNotWrittenThrough(t *testing.T) {
	dir, elsewhere := t.TempDir(), t.TempDir()
	outside := filepath.Join(elsewhere, "victim")
	if err := os.WriteFile(outside, []byte("original"), 0o600); err != nil {
		t.Fatalf("writing the file: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, ".planted.abcdef012345.part")); err != nil {
		t.Fatalf("making the link: %v", err)
	}

	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatalf("opening the namespace: %v", err)
	}
	defer func() { _ = root.Close() }()

	if out, _, err := opening(root, arriving{part: ".planted.abcdef012345.part"}); err == nil {
		_ = out.Close()
		t.Fatal("opening() opened a part that was already there")
	}
	if got := read(t, outside); string(got) != "original" {
		t.Errorf("the file behind the link now says %q", got)
	}
}

func TestALinkAtAResumablePartIsNotWrittenThrough(t *testing.T) {
	dir := t.TempDir()
	victim := filepath.Join(dir, "victim")
	if err := os.WriteFile(victim, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}

	sum := bytes.Repeat([]byte{1}, 32)
	part := partFor("report.bin", sum)
	if err := os.Symlink("victim", filepath.Join(dir, part)); err != nil {
		t.Fatal(err)
	}

	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()

	if out, _, err := opening(root, arriving{part: part, kept: true}); err == nil {
		_ = out.Close()
		t.Fatal("opening() followed a resumable part link")
	}
	if got := read(t, victim); string(got) != "original" {
		t.Errorf("the file behind the link now says %q", got)
	}
}

func TestOnlyOneTransferFillsAResumablePart(t *testing.T) {
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()

	a := arriving{part: ".report.sum.part", kept: true}
	first, _, err := opening(root, a)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = first.Close() }()

	if second, _, err := opening(root, a); err == nil {
		_ = second.Close()
		t.Fatal("two transfers locked the same resumable part")
	} else if !strings.Contains(err.Error(), "already filling") {
		t.Fatalf("the second transfer failed as %v", err)
	}

	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	third, _, err := opening(root, a)
	if err != nil {
		t.Fatalf("the part stayed locked after its transfer ended: %v", err)
	}
	_ = third.Close()
}

func TestAResumePartMustStillHaveTheRequestedSize(t *testing.T) {
	dir := t.TempDir()
	part := ".report.sum.part"
	if err := os.WriteFile(filepath.Join(dir, part), []byte("changed"), 0o600); err != nil {
		t.Fatal(err)
	}

	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()

	if out, _, err := opening(root, arriving{part: part, have: 3, kept: true}); err == nil {
		_ = out.Close()
		t.Fatal("opening() resumed from a size the part no longer had")
	}
	if got := read(t, filepath.Join(dir, part)); string(got) != "changed" {
		t.Errorf("the changed part now says %q", got)
	}
}

// Two transfers of one name never fill one part file, so the tag on it is drawn afresh every time.
func TestEachTransferGetsItsOwnPart(t *testing.T) {
	first, err := partName("sub/notes.txt")
	if err != nil {
		t.Fatalf("partName(): %v", err)
	}
	second, err := partName("sub/notes.txt")
	if err != nil {
		t.Fatalf("partName(): %v", err)
	}

	if first == second {
		t.Errorf("two transfers of one name share %q", first)
	}
	for _, at := range []string{first, second} {
		if want := "sub/.notes.txt."; !strings.HasPrefix(at, want) || !strings.HasSuffix(at, ".part") {
			t.Errorf("partName() gave %q, which does not wait beside what it lands as", at)
		}
	}
}

// What a stopped transfer left at a .part is nothing the next one touches.
func TestAStalePartIsNotWrittenInto(t *testing.T) {
	dir := t.TempDir()

	stale := filepath.Join(dir, ".notes.abcdef012345.part")
	if err := os.WriteFile(stale, []byte("a much longer tail than this"), 0o600); err != nil {
		t.Fatalf("writing the stale part: %v", err)
	}

	body := "short"
	b := opened(t, dir, true, Into{})
	if err := b.Put("notes", strings.NewReader(body), Given{Size: int64(len(body)), Mode: 0o644}); err != nil {
		t.Fatalf("Put(): %v", err)
	}
	if got := read(t, filepath.Join(dir, "notes")); string(got) != body {
		t.Errorf("the upload landed as %q", got)
	}
	if got := read(t, stale); string(got) != "a much longer tail than this" {
		t.Errorf("the stale part now says %q", got)
	}
}

// Two peers pushing one name at once each land their own bytes: neither is acknowledged for an
// upload the other's part file swallowed.
func TestTwoUploadsOfOneNameDoNotShareAPart(t *testing.T) {
	dir := t.TempDir()

	// The first upload stops halfway, and is let go only once the second has landed on top of it.
	landed, rest := make(chan struct{}), make(chan struct{})
	first := &held{body: bytes.Repeat([]byte("A"), 8), rest: rest}
	slow := opened(t, dir, true, Into{Progress: func(_ string, done, _ int64) {
		if done == 4 {
			close(landed)
		}
	}})

	acked := make(chan error, 1)
	go func() {
		acked <- slow.Put("report.bin", first, Given{Size: 8, Mode: 0o644})
	}()
	<-landed

	// The second runs to its end while the first still has its part file open.
	quick := opened(t, dir, true, Into{})
	if err := quick.Put("report.bin", bytes.NewReader(bytes.Repeat([]byte("B"), 8)), Given{Size: 8, Mode: 0o644}); err != nil {
		t.Fatalf("the second Put(): %v", err)
	}

	close(rest)
	if err := <-acked; err != nil {
		t.Fatalf("the first Put(): %v", err)
	}

	if got := read(t, filepath.Join(dir, "report.bin")); string(got) != "BBBBBBBB" {
		t.Errorf("report.bin says %q", got)
	}
	if got := read(t, filepath.Join(dir, "report-1.bin")); string(got) != "AAAAAAAA" {
		t.Errorf("report-1.bin says %q", got)
	}
}

// held is a body that hands over half of itself and waits there until rest is closed.
type held struct {
	body []byte
	at   int
	rest chan struct{}
}

func (h *held) Read(p []byte) (int, error) {
	if h.at == len(h.body) {
		return 0, io.EOF
	}
	if h.at > 0 {
		<-h.rest
	}

	n := copy(p, h.body[h.at:h.at+len(h.body)/2])
	h.at += n
	return n, nil
}

// What arrives never replaces a file that was on this disk first.
func TestAnUploadDoesNotLandOnWhatIsThere(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("mine"), 0o600); err != nil {
		t.Fatalf("writing the file: %v", err)
	}

	var landed string
	b := opened(t, dir, true, Into{Landed: func(_ node.ID, name string, _ int64) { landed = name }})
	if err := b.Put("notes.txt", strings.NewReader("theirs"), Given{Size: 6, Mode: 0o644}); err != nil {
		t.Fatalf("Put(): %v", err)
	}
	if _, err := b.List(""); err != nil {
		t.Fatalf("List(): %v", err)
	}

	if got := read(t, filepath.Join(dir, "notes.txt")); string(got) != "mine" {
		t.Errorf("the file that was here now says %q", got)
	}
	if got := read(t, filepath.Join(dir, "notes-1.txt")); string(got) != "theirs" {
		t.Errorf("the upload landed as %q", got)
	}
	if landed != "notes-1.txt" {
		t.Errorf("the upload was reported as %q", landed)
	}
}

func TestCleanKeepsOrdinaryNames(t *testing.T) {
	for _, at := range []struct{ rel, want string }{
		{"", "."},
		{".", "."},
		{"Report 2024 (final).PDF", "Report 2024 (final).PDF"},
		{"Some Dir/a file.txt", "Some Dir/a file.txt"},
		{"sub/./thing", "sub/thing"},
		{"sub/", "sub"},
		{"C:notes", "C:notes"},
	} {
		got, err := clean(at.rel)
		if err != nil {
			t.Errorf("clean(%q): %v", at.rel, err)
			continue
		}
		if got != at.want {
			t.Errorf("clean(%q) = %q, want %q", at.rel, got, at.want)
		}
	}
}

func TestCleanRefusesWhatIsNotAPathInsideTheNamespace(t *testing.T) {
	bad := []string{
		"/etc/passwd",
		"..",
		"../outside",
		"sub/../../outside",
		"a/../../../../../../etc/shadow",
		`..\..\outside`,
		`sub\thing`,
		`C:\Windows\win.ini`,
		`\\host\share\thing`,
		"with\x00a nul",
		strings.Repeat("a", MaxRel+1),
	}
	for _, rel := range bad {
		if got, err := clean(rel); err == nil {
			t.Errorf("clean(%q) = %q, want a refusal", rel, got)
		}
	}
}

func TestRequestAndReplyRoundTrip(t *testing.T) {
	q := request{
		Op: opReplace, Name: "sub/thing.txt", To: "sub/other.txt", Size: 1234, Mode: 0o755,
		At: 1724751000123456789, Sum: bytes.Repeat([]byte{0x7f}, 32), From: 4096,
	}
	back, err := decodeRequest(q.encode())
	if err != nil {
		t.Fatalf("decodeRequest(): %v", err)
	}
	if !reflect.DeepEqual(back, q) {
		t.Errorf("request came back as %+v, want %+v", back, q)
	}

	p := reply{OK: true, Reason: "", Entries: []Entry{
		{Name: "a", Size: 1, Mode: 0o644, At: 100},
		{Name: "b", Size: -1, Mode: 0o755, Dir: true, At: 200},
	}}
	said, err := decodeReply(p.encode())
	if err != nil {
		t.Fatalf("decodeReply(): %v", err)
	}
	if said.OK != p.OK || len(said.Entries) != 2 || said.Entries[1] != p.Entries[1] {
		t.Errorf("reply came back as %+v", said)
	}
}

func TestRequestRefusesAnInvalidUnknownSize(t *testing.T) {
	q := request{Op: opPut, Name: "report.bin", Size: wire.SizeUnknown - 1}
	if _, err := decodeRequest(q.encode()); err == nil {
		t.Fatal("decodeRequest() accepted an invalid negative size")
	}
}

// A count is a claim. A small body must not make a large allocation, and must not decode.
func TestReplyRefusesAnImpossibleCount(t *testing.T) {
	w := wire.NewWriter()
	w.Bool(true)
	w.String("")
	w.Uint(MaxEntries + 1)
	if _, err := decodeReply(w.Body()); err == nil {
		t.Fatal("decodeReply() accepted a listing over the limit")
	}

	w = wire.NewWriter()
	w.Bool(true)
	w.String("")
	w.Uint(MaxEntries)
	if _, err := decodeReply(w.Body()); err == nil {
		t.Fatal("decodeReply() accepted a listing that is not there")
	}
}

// Every string on the wire is bounded where it is read, not where it is used.
func TestNamesOnTheWireAreBounded(t *testing.T) {
	long := strings.Repeat("a", MaxRel+1)

	w := wire.NewWriter()
	w.Byte(opGet)
	w.String(long)
	w.String("")
	w.Int(0)
	w.Uint(0)
	if _, err := decodeRequest(w.Body()); err == nil {
		t.Error("decodeRequest() accepted a name over the limit")
	}

	w = wire.NewWriter()
	w.Bool(true)
	w.String("")
	w.Uint(1)
	w.String(long)
	if _, err := decodeReply(w.Body()); err == nil {
		t.Error("decodeReply() accepted an entry name over the limit")
	}
}

// The bytes of a transfer are checked, not taken on trust.
func TestTakeOntoRefusesACorruptedTransfer(t *testing.T) {
	var buf bytes.Buffer
	conn := wire.NewConn(readWriter{&buf, &buf})

	if err := conn.WriteData([]byte("hello")); err != nil {
		t.Fatalf("writing the data: %v", err)
	}
	end := wire.End{Size: 5, Digest: bytes.Repeat([]byte{0}, 32)}
	if err := conn.WriteFrame(wire.KindEnd, end.Encode()); err != nil {
		t.Fatalf("writing the end: %v", err)
	}

	dir := t.TempDir()
	into := filepath.Join(dir, "landed")
	if err := takeOnto(conn, into, "landed", Entry{Size: 5, Mode: 0o644}, nil, 0, nil); err == nil {
		t.Fatal("takeOnto() accepted a digest that does not match")
	}
	if _, err := os.Stat(into); err == nil {
		t.Error("a transfer that did not verify was kept")
	}
	left, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading the directory: %v", err)
	}
	if len(left) != 0 {
		t.Errorf("a transfer that did not verify left %d files behind", len(left))
	}
}

func TestNumberedSpacesANameOut(t *testing.T) {
	for _, at := range []struct {
		name string
		n    int
		want string
	}{
		{"report.txt", 0, "report.txt"},
		{"report.txt", 2, "report-2.txt"},
		{"archive.tar.gz", 1, "archive.tar-1.gz"},
		{"sub/report.txt", 1, "sub/report-1.txt"},
	} {
		if got := numbered(at.name, at.n); got != at.want {
			t.Errorf("numbered(%q, %d) = %q, want %q", at.name, at.n, got, at.want)
		}
	}
}

// A put with nowhere to land ends the round, not the session.
func TestAPutWithNowhereToLandIsRefused(t *testing.T) {
	b := opened(t, t.TempDir(), true, Into{})

	if err := b.Put("missing/thing", strings.NewReader("x"), Given{Size: 1, Mode: 0o644}); err == nil {
		t.Error("Put() into a directory that is not there succeeded")
	}
	if err := b.Mkdir("here"); err != nil {
		t.Fatalf("Mkdir() after the refusal: %v", err)
	}
	if err := b.Put("here/thing", strings.NewReader("x"), Given{Size: 1, Mode: 0o644}); err != nil {
		t.Fatalf("Put() after the refusal: %v", err)
	}
}

// The classic way past containment is a name that becomes a link between the check and the open, so
// one is swapped under a session that is asking for it over and over.
func TestALinkSwappedInUnderASessionIsNotFollowed(t *testing.T) {
	dir, elsewhere := t.TempDir(), t.TempDir()
	secret := filepath.Join(elsewhere, "secret")
	if err := os.WriteFile(secret, []byte("the secret"), 0o600); err != nil {
		t.Fatalf("writing the file: %v", err)
	}

	target := filepath.Join(dir, "swap")
	stop, swapping := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(swapping)
		for turn := 0; ; turn++ {
			select {
			case <-stop:
				return
			default:
			}
			_ = os.Remove(target)
			if turn%2 == 0 {
				_ = os.Symlink(secret, target)
				continue
			}
			_ = os.WriteFile(target, []byte("inside"), 0o600)
		}
	}()
	defer func() {
		close(stop)
		<-swapping
	}()

	b := opened(t, dir, false, Into{})
	into := filepath.Join(t.TempDir(), "copy")

	for range 300 {
		_ = os.Remove(into)
		if err := b.Get("swap", into, Want{}); err != nil {
			continue
		}
		if got, err := os.ReadFile(into); err == nil && string(got) == "the secret" {
			t.Fatal("a get read the file outside the namespace")
		}
	}
}

func TestScanNoticesAReplacementWithMatchingMetadata(t *testing.T) {
	dir := t.TempDir()
	at := filepath.Join(dir, "same.txt")
	if err := os.WriteFile(at, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	when := time.Now().Add(-time.Hour)
	if err := os.Chtimes(at, when, when); err != nil {
		t.Fatal(err)
	}
	first, err := scan(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	held, err := os.Open(at)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := held.Close(); err != nil {
			t.Error(err)
		}
	})

	replacement := filepath.Join(dir, "replacement")
	if err := os.WriteFile(replacement, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(replacement, when, when); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(replacement, at); err != nil {
		t.Fatal(err)
	}
	second, err := scan(dir, first)
	if err != nil {
		t.Fatal(err)
	}
	if first["same.txt"].Sum == second["same.txt"].Sum {
		t.Fatal("replacement with matching size and timestamp kept the old digest")
	}
}

func TestScanRefusesHardLinks(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outside, []byte("private"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(outside, filepath.Join(dir, "linked")); err != nil {
		t.Skipf("hard links are unavailable: %v", err)
	}

	got, err := scan(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got["linked"]; ok {
		t.Fatal("a hard link was included in a shared folder")
	}
}

func TestScanDoesNotReportUnsupportedReplacementsAsDeleted(t *testing.T) {
	for _, test := range []struct {
		name    string
		replace func(*testing.T, string)
	}{
		{
			name: "directory",
			replace: func(t *testing.T, at string) {
				if err := os.Mkdir(at, 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(at, "child"), []byte("not tracked"), 0o600); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "symbolic link",
			replace: func(t *testing.T, at string) {
				outside := filepath.Join(t.TempDir(), "outside")
				if err := os.WriteFile(outside, []byte("outside"), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, at); err != nil {
					t.Skipf("symbolic links are unavailable: %v", err)
				}
			},
		},
		{
			name: "hard link",
			replace: func(t *testing.T, at string) {
				outside := filepath.Join(t.TempDir(), "outside")
				if err := os.WriteFile(outside, []byte("outside"), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Link(outside, at); err != nil {
					t.Skipf("hard links are unavailable: %v", err)
				}
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			at := filepath.Join(dir, "tracked")
			if err := os.WriteFile(at, []byte("held"), 0o600); err != nil {
				t.Fatal(err)
			}
			first, err := scan(dir, nil)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(at); err != nil {
				t.Fatal(err)
			}
			test.replace(t, at)

			second, err := scan(dir, first)
			if err != nil {
				t.Fatal(err)
			}
			if second["tracked"].Sum != first["tracked"].Sum {
				t.Fatal("the last readable version was not retained")
			}
			if edits := (&keeper{held: first}).mine(second, nil); len(edits) != 0 {
				t.Fatalf("an unsupported replacement became edits: %+v", edits)
			}
			if _, tracked := second["tracked/child"]; tracked {
				t.Fatal("a directory replacing a tracked file was descended into")
			}
		})
	}
}

func TestScanRefusesSparseFiles(t *testing.T) {
	dir := t.TempDir()
	at := filepath.Join(dir, "sparse")
	if err := os.WriteFile(at, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(at, 1<<20); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(at)
	if err != nil {
		t.Fatal(err)
	}
	hasHole, err := sparse(file, 1<<20)
	if closeErr := file.Close(); err == nil && closeErr != nil {
		err = closeErr
	}
	if err != nil {
		t.Fatal(err)
	}
	if !hasHole {
		t.Skip("filesystem does not expose sparse extents")
	}

	got, err := scan(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got["sparse"]; ok {
		t.Fatal("a sparse file was included in a shared folder")
	}
}

func TestScanLeavesOutPathsTheProtocolCannotCarry(t *testing.T) {
	dir := t.TempDir()
	deep := dir
	for i := range 5 {
		deep = filepath.Join(deep, strings.Repeat(string(rune('a'+i)), 220))
	}
	if err := os.MkdirAll(deep, 0o700); err != nil {
		t.Skipf("this disk cannot make a path over %d bytes: %v", MaxRel, err)
	}
	long := filepath.Join(deep, "too-long")
	if err := os.WriteFile(long, []byte("not carried"), 0o600); err != nil {
		t.Skipf("this disk cannot make a path over %d bytes: %v", MaxRel, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "carried"), []byte("yes"), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := scan(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got["carried"]; !ok {
		t.Fatal("a supported path was lost beside an unsupported one")
	}
	rel, err := filepath.Rel(dir, long)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got[filepath.ToSlash(rel)]; ok {
		t.Fatal("a path longer than the wire limit was included")
	}
}

// A pipe under a name is answered, not waited on: an open that waits for a writer has no deadline
// and nothing to end it.
func TestAPipeUnderANameIsNotWaitedOn(t *testing.T) {
	dir := t.TempDir()
	pipe := filepath.Join(dir, "pipe")
	if err := exec.Command("mkfifo", pipe).Run(); err != nil {
		t.Skipf("no fifos here: %v", err)
	}
	if file, _, err := lifted(pipe); err == nil {
		_ = file.Close()
		t.Fatal("lifted() accepted a pipe for upload")
	}
	summed := make(chan error, 1)
	go func() {
		_, _, err := sumOf(pipe)
		summed <- err
	}()
	select {
	case err := <-summed:
		if err == nil {
			t.Fatal("sumOf() accepted a pipe")
		}
	case <-time.After(time.Second):
		t.Fatal("hashing a pipe waited for a writer")
	}

	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatalf("opening the namespace: %v", err)
	}
	defer func() { _ = root.Close() }()

	done := make(chan os.FileMode, 1)
	go func() {
		file, stat, err := reading(root, "pipe")
		if err != nil {
			done <- 0
			return
		}
		defer func() { _ = file.Close() }()
		done <- stat.Mode()
	}()

	select {
	case mode := <-done:
		if mode.IsRegular() {
			t.Errorf("a pipe came back as a regular file, mode %v", mode)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("opening a pipe waited for a writer")
	}
}

// A listing of long names is refused rather than answered with a frame no reader will take.
func TestAListingTooBigForOneReplyIsRefused(t *testing.T) {
	dir := t.TempDir()

	name := strings.Repeat("n", 250)
	for i := range MaxEntries {
		at := filepath.Join(dir, fmt.Sprintf("%06d%s", i, name[6:]))
		if err := os.WriteFile(at, nil, 0o600); err != nil {
			t.Fatalf("writing entry %d: %v", i, err)
		}
	}

	b := opened(t, dir, false, Into{})
	_, err := b.List("")
	if err == nil {
		t.Fatal("List() answered with a listing that does not fit in a frame")
	}
	if !strings.Contains(err.Error(), "over the") || strings.Contains(err.Error(), "wire:") {
		t.Fatalf("List() failed as %v, rather than being refused", err)
	}
	if _, err := b.List("."); err == nil || strings.Contains(err.Error(), "wire:") {
		t.Fatalf("the session did not survive the refusal: %v", err)
	}
}

// A part that cannot be moved onto its name leaves neither itself nor the name behind.
func TestAPartThatCannotBeMovedIsNotLeftBehind(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".x.abcdef012345.part"), 0o700); err != nil {
		t.Fatalf("making the part: %v", err)
	}

	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatalf("opening the namespace: %v", err)
	}
	defer func() { _ = root.Close() }()

	if final, err := place(root, ".x.abcdef012345.part", "x"); err == nil {
		t.Fatalf("place() landed on %s", final)
	}

	left, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading the directory back: %v", err)
	}
	for _, at := range left {
		t.Errorf("%s was left behind", at.Name())
	}
}
