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

// opened runs a files namespace over a pipe and hands back the caller's side of it.
func opened(t *testing.T, dir string, writable bool, hooks Into) *Browsing {
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

	b, err := Browse(wire.NewConn(caller))
	if err != nil {
		t.Fatalf("Browse(): %v", err)
	}
	return b
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
	body, err := os.ReadFile(into)
	if err != nil {
		t.Fatalf("reading what landed: %v", err)
	}
	if string(body) != "what happened" {
		t.Errorf("the file arrived as %q", body)
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
	body, err := os.ReadFile(landed)
	if err != nil {
		t.Fatalf("reading what landed: %v", err)
	}
	if string(body) != "the numbers" {
		t.Errorf("the upload landed as %q", body)
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

func TestUnderKeepsOrdinaryNames(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "Some Dir"), 0o755); err != nil {
		t.Fatalf("making the directory: %v", err)
	}

	for _, rel := range []string{"", ".", "Report 2024 (final).PDF", "Some Dir/a file.txt", "sub/./thing"} {
		got, err := under(root, rel)
		if err != nil {
			t.Errorf("under(%q): %v", rel, err)
			continue
		}
		if !strings.HasPrefix(got, root) {
			t.Errorf("under(%q) = %q, which is not under the root", rel, got)
		}
	}
}

func TestUnderRefusesWhatLeavesTheRoot(t *testing.T) {
	root := t.TempDir()

	bad := []string{
		"/etc/passwd",
		"..",
		"../outside",
		"sub/../../outside",
		"a/../../../../../../etc/shadow",
		strings.Repeat("a", MaxRel+1),
	}
	for _, rel := range bad {
		if got, err := under(root, rel); err == nil {
			t.Errorf("under(%q) = %q, want a refusal", rel, got)
		}
	}

	if _, err := under("", "anything"); err == nil {
		t.Error("under() accepted a namespace with no directory")
	}
}

// The one that is usually missed: the path is clean, and the escape is a link on the disk.
func TestUnderRefusesASymlinkOut(t *testing.T) {
	root, elsewhere := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(elsewhere, "secret"), []byte("."), 0o600); err != nil {
		t.Fatalf("writing the file: %v", err)
	}
	if err := os.Symlink(elsewhere, filepath.Join(root, "out")); err != nil {
		t.Fatalf("making the link: %v", err)
	}
	if err := os.Symlink(filepath.Join(elsewhere, "secret"), filepath.Join(root, "one")); err != nil {
		t.Fatalf("making the link: %v", err)
	}

	for _, rel := range []string{"out", "out/secret", "one"} {
		if got, err := under(root, rel); err == nil {
			t.Errorf("under(%q) = %q, want a refusal: it resolves outside the root", rel, got)
		}
	}

	// A link that stays inside is not an escape.
	if err := os.Symlink(root, filepath.Join(root, "self")); err != nil {
		t.Fatalf("making the link: %v", err)
	}
	if _, err := under(root, "self"); err != nil {
		t.Errorf("under() refused a link that stays inside: %v", err)
	}
}

// Even with containment right, a planted link must not be written through.
func TestABrowsedWriteWillNotFollowALink(t *testing.T) {
	dir, elsewhere := t.TempDir(), t.TempDir()
	target := filepath.Join(elsewhere, "victim")
	if err := os.WriteFile(target, []byte("original"), 0o600); err != nil {
		t.Fatalf("writing the file: %v", err)
	}
	// Dangling as far as resolving goes -- it is the .part file the link is laid over.
	if err := os.Symlink(target, filepath.Join(dir, "planted.part")); err != nil {
		t.Fatalf("making the link: %v", err)
	}

	b := opened(t, dir, true, Into{})
	if err := b.Put("planted", strings.NewReader("wrote"), 5, 0o644, nil); err == nil {
		t.Fatal("Put() wrote through a planted symlink")
	}

	body, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("reading the file behind the link: %v", err)
	}
	if string(body) != "original" {
		t.Errorf("the file behind the link now says %q", body)
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

// The bytes of a transfer are checked, not taken on trust.
func TestTakeBodyRefusesACorruptedTransfer(t *testing.T) {
	var buf bytes.Buffer
	conn := wire.NewConn(readWriter{&buf, &buf})

	if err := conn.WriteData([]byte("hello")); err != nil {
		t.Fatalf("writing the data: %v", err)
	}
	end := wire.End{Size: 5, Digest: bytes.Repeat([]byte{0}, 32)}
	if err := conn.WriteFrame(wire.KindEnd, end.Encode()); err != nil {
		t.Fatalf("writing the end: %v", err)
	}

	into := filepath.Join(t.TempDir(), "landed")
	if err := takeBody(conn, "landed", into, 5, 0o644, nil); err == nil {
		t.Fatal("takeBody() accepted a digest that does not match")
	}
	if _, err := os.Stat(into); err == nil {
		t.Error("a transfer that did not verify was kept")
	}
}
