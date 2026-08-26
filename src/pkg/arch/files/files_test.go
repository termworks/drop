package files

import (
	"bytes"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bresilla/drop/src/pkg/arch"
	"github.com/bresilla/drop/src/pkg/node"
	"github.com/bresilla/drop/src/pkg/wire"
)

// readWriter is the two halves of a stream a test has in two buffers.
type readWriter struct {
	io.Reader
	io.Writer
}

// serving runs a files namespace over a pipe and hands back the caller's side of the stream.
func serving(t *testing.T, dir string, writable bool, hooks Into) *wire.Conn {
	t.Helper()

	caller, server := net.Pipe()
	t.Cleanup(func() { caller.Close() })

	go func() {
		defer server.Close()

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
	if err := b.Get("notes.txt", into, nil); err != nil {
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
			if err := b.Get(name, filepath.Join(landing, name), nil); err != nil {
				t.Fatalf("Get(%q): %v", name, err)
			}
		}
	}
	if err := b.Get("missing", filepath.Join(landing, "missing"), nil); err == nil {
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
	if err := b.Put("uploads/report", strings.NewReader(sent), int64(len(sent)), 0o777, nil); err != nil {
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

	if err := b.Put("sneaky", strings.NewReader("x"), 1, 0o644, nil); err == nil {
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

func TestBrowseReportsAnUploadThatLands(t *testing.T) {
	dir := t.TempDir()

	var name string
	var size int64
	hooks := Into{Landed: func(_ node.ID, n string, s int64) { name, size = n, s }}

	b := opened(t, dir, true, hooks)
	if err := b.Put("log", strings.NewReader("ping"), 4, 0o644, nil); err != nil {
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
		defer server.Close()

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
	caller.Close()

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
	if err := b.Get("notes", into, nil); err != nil {
		t.Fatalf("Get() through a namespace reached by a link: %v", err)
	}
	if err := b.Get("out/secret", into, nil); err == nil {
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
		if err := b.Get(name, into, nil); err == nil {
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
	if err := b.Put("one", strings.NewReader("wrote"), 5, 0o644, nil); err != nil {
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
	if err := b.Get("same/notes", into, nil); err != nil {
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
		if err := b.Get(name, filepath.Join(t.TempDir(), "copy"), nil); err == nil {
			t.Errorf("Get(%q) was allowed", name)
		}
		if err := b.Put(name, strings.NewReader("x"), 1, 0o644, nil); err == nil {
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
	defer listening.Close()

	b := opened(t, dir, false, Into{})
	if err := b.Get("socket", filepath.Join(t.TempDir(), "copy"), nil); err == nil {
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
	if err := b.Get(deep, filepath.Join(t.TempDir(), "copy"), nil); err == nil {
		t.Error("Get() of a name 400 deep succeeded")
	}
	if err := b.Mkdir(deep); err == nil {
		t.Error("Mkdir() of a name 400 deep succeeded")
	}
	if _, err := b.List(""); err != nil {
		t.Fatalf("List() after a name 400 deep: %v", err)
	}
}

// The .part path is worked out from the name, so a link planted at it is unlinked, not written
// through.
func TestALinkAtThePartIsNotWrittenThrough(t *testing.T) {
	dir, elsewhere := t.TempDir(), t.TempDir()
	outside := filepath.Join(elsewhere, "victim")
	if err := os.WriteFile(outside, []byte("original"), 0o600); err != nil {
		t.Fatalf("writing the file: %v", err)
	}

	body := "wrote"
	if err := os.Symlink(outside, filepath.Join(dir, partName("planted", int64(len(body))))); err != nil {
		t.Fatalf("making the link: %v", err)
	}

	b := opened(t, dir, true, Into{})
	if err := b.Put("planted", strings.NewReader(body), int64(len(body)), 0o644, nil); err != nil {
		t.Fatalf("Put(): %v", err)
	}
	if got := read(t, outside); string(got) != "original" {
		t.Errorf("the file behind the link now says %q", got)
	}
	if got := read(t, filepath.Join(dir, "planted")); string(got) != body {
		t.Errorf("the upload landed as %q", got)
	}
}

// What a stopped transfer left at the .part is not part of what arrives next.
func TestAStalePartIsNotLeftUnderTheUpload(t *testing.T) {
	dir := t.TempDir()

	body := "short"
	stale := filepath.Join(dir, partName("notes", int64(len(body))))
	if err := os.WriteFile(stale, []byte("a much longer tail than this"), 0o600); err != nil {
		t.Fatalf("writing the stale part: %v", err)
	}

	b := opened(t, dir, true, Into{})
	if err := b.Put("notes", strings.NewReader(body), int64(len(body)), 0o644, nil); err != nil {
		t.Fatalf("Put(): %v", err)
	}
	if got := read(t, filepath.Join(dir, "notes")); string(got) != body {
		t.Errorf("the upload landed as %q", got)
	}
}

// What arrives never replaces a file that was on this disk first.
func TestAnUploadDoesNotLandOnWhatIsThere(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("mine"), 0o600); err != nil {
		t.Fatalf("writing the file: %v", err)
	}

	var landed string
	b := opened(t, dir, true, Into{Landed: func(_ node.ID, name string, _ int64) { landed = name }})
	if err := b.Put("notes.txt", strings.NewReader("theirs"), 6, 0o644, nil); err != nil {
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
	q := request{Op: opPut, Name: "sub/thing.txt", To: "sub/other.txt", Size: 1234, Mode: 0o755}
	back, err := decodeRequest(q.encode())
	if err != nil {
		t.Fatalf("decodeRequest(): %v", err)
	}
	if back != q {
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
	if err := takeOnto(conn, into, "landed", 5, 0o644, nil); err == nil {
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

	if err := b.Put("missing/thing", strings.NewReader("x"), 1, 0o644, nil); err == nil {
		t.Error("Put() into a directory that is not there succeeded")
	}
	if err := b.Mkdir("here"); err != nil {
		t.Fatalf("Mkdir() after the refusal: %v", err)
	}
	if err := b.Put("here/thing", strings.NewReader("x"), 1, 0o644, nil); err != nil {
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
		if err := b.Get("swap", into, nil); err != nil {
			continue
		}
		if got, err := os.ReadFile(into); err == nil && string(got) == "the secret" {
			t.Fatal("a get read the file outside the namespace")
		}
	}
}
