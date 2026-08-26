package share

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"

	"lukechampine.com/blake3"

	"github.com/bresilla/drop/src/pkg/node"
	"github.com/bresilla/drop/src/pkg/wire"
)

// spoke is one item as a sender puts it on the wire: what it sends now, and everything the item is,
// which is what the digest covers.
type spoke struct {
	sent  []byte
	whole []byte
}

func spoken(t *testing.T, items ...spoke) *bytes.Buffer {
	t.Helper()

	var buf bytes.Buffer
	conn := wire.NewConn(readWriter{&buf, &buf})
	for _, item := range items {
		if len(item.sent) > 0 {
			if err := conn.WriteData(item.sent); err != nil {
				t.Fatalf("writing data: %v", err)
			}
		}
		digest := blake3.New(32, nil)
		digest.Write(item.whole)
		end := wire.End{Size: int64(len(item.whole)), Digest: digest.Sum(nil)}
		if err := conn.WriteFrame(wire.KindEnd, end.Encode()); err != nil {
			t.Fatalf("writing the end: %v", err)
		}
	}
	return &buf
}

// taking runs a receiving session against a directory and hands back what was answered.
//
// The offer goes in front of what the sender wrote, because that is the first thing on the stream.
func taking(t *testing.T, dir string, items []Item, sent *bytes.Buffer, done *[]string) (*bytes.Buffer, error) {
	t.Helper()

	var offering bytes.Buffer
	if err := wire.NewConn(readWriter{&offering, &offering}).WriteFrame(wire.KindItem, offer{Items: items}.encode()); err != nil {
		t.Fatalf("writing the offer: %v", err)
	}
	offering.Write(sent.Bytes())

	var hooks Into
	if done != nil {
		hooks.Landed = func(_ node.ID, name string, _ int64) { *done = append(*done, name) }
	}

	var out bytes.Buffer
	err := receive(wire.NewConn(readWriter{&offering, &out}), dir, node.ID{}, hooks)
	return &out, err
}

// readWriter is the two halves of a stream a test has in two buffers.
type readWriter struct {
	io.Reader
	io.Writer
}

// answered reads back the frames a receiving session wrote.
func answered(t *testing.T, out *bytes.Buffer) []byte {
	t.Helper()

	conn := wire.NewConn(readWriter{out, &bytes.Buffer{}})
	kind, body, err := conn.ReadFrame()
	if err != nil {
		t.Fatalf("reading the answer: %v", err)
	}
	if kind == wire.KindReject {
		t.Fatalf("the session was refused")
	}
	if kind != wire.KindAccept {
		t.Fatalf("answered with frame kind %d, expected an accept", kind)
	}
	return body
}

func read(t *testing.T, path string) []byte {
	t.Helper()

	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading back %s: %v", path, err)
	}
	return body
}

// A .part left behind by a transfer that stopped is longer than the item that comes next. Without
// truncation its tail stays under the new bytes, and the digest -- taken over the stream -- passes.
func TestAStalePartIsNotLeftUnderTheItem(t *testing.T) {
	dir := t.TempDir()
	item := Item{Name: "notes", Size: wire.SizeUnknown, Mode: 0o644}

	stale := bytes.Repeat([]byte("x"), 4096)
	if err := os.WriteFile(filepath.Join(dir, partName(item)), stale, 0o600); err != nil {
		t.Fatalf("planting a stale part: %v", err)
	}

	body := []byte("the whole of it")
	if _, err := taking(t, dir, []Item{item}, spoken(t, spoke{sent: body, whole: body}), nil); err != nil {
		t.Fatalf("receiving: %v", err)
	}

	if got := read(t, filepath.Join(dir, "notes")); !bytes.Equal(got, body) {
		t.Fatalf("the file is %d bytes, expected the %d that were sent", len(got), len(body))
	}
}

// A .part belonging to a different offer of the same name is not resumed against.
func TestResumeOnlyPicksUpTheSameOffer(t *testing.T) {
	dir := t.TempDir()
	earlier := Item{Name: "a.txt", Size: 10}
	if err := os.WriteFile(filepath.Join(dir, partName(earlier)), []byte("0123456789"), 0o600); err != nil {
		t.Fatalf("planting an earlier part: %v", err)
	}

	whole := []byte("something else entirely")
	item := Item{Name: "a.txt", Size: int64(len(whole))}

	out, err := taking(t, dir, []Item{item}, spoken(t, spoke{sent: whole, whole: whole}), nil)
	if err != nil {
		t.Fatalf("receiving: %v", err)
	}

	picked, err := decodeResume(answered(t, out))
	if err != nil {
		t.Fatalf("decoding the answer: %v", err)
	}
	if picked.At[0] != 0 {
		t.Fatalf("resumed at %d against another offer's part", picked.At[0])
	}
	if got := read(t, filepath.Join(dir, "a.txt")); !bytes.Equal(got, whole) {
		t.Fatalf("landed %q, expected %q", got, whole)
	}
}

// The part of an offer that matches is what a resumed transfer continues.
func TestResumeContinuesTheSameOffer(t *testing.T) {
	dir := t.TempDir()
	whole := []byte("0123456789abcdef")
	item := Item{Name: "b.bin", Size: int64(len(whole))}

	if err := os.WriteFile(filepath.Join(dir, partName(item)), whole[:6], 0o600); err != nil {
		t.Fatalf("planting a part: %v", err)
	}

	out, err := taking(t, dir, []Item{item}, spoken(t, spoke{sent: whole[6:], whole: whole}), nil)
	if err != nil {
		t.Fatalf("receiving: %v", err)
	}

	picked, err := decodeResume(answered(t, out))
	if err != nil {
		t.Fatalf("decoding the answer: %v", err)
	}
	if picked.At[0] != 6 {
		t.Fatalf("resumed at %d, expected 6", picked.At[0])
	}
	if got := read(t, filepath.Join(dir, "b.bin")); !bytes.Equal(got, whole) {
		t.Fatalf("landed %q, expected %q", got, whole)
	}
}

// What arrives never lands on top of a file that was there first.
func TestNothingAlreadyThereIsOverwritten(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("mine"), 0o600); err != nil {
		t.Fatalf("writing the file that is there: %v", err)
	}

	theirs := []byte("theirs")
	item := Item{Name: "notes.txt", Size: int64(len(theirs))}

	var landed []string
	if _, err := taking(t, dir, []Item{item}, spoken(t, spoke{sent: theirs, whole: theirs}), &landed); err != nil {
		t.Fatalf("receiving: %v", err)
	}

	if got := read(t, filepath.Join(dir, "notes.txt")); !bytes.Equal(got, []byte("mine")) {
		t.Fatalf("the file that was here is now %q", got)
	}
	if got := read(t, filepath.Join(dir, "notes-1.txt")); !bytes.Equal(got, theirs) {
		t.Fatalf("what arrived landed as %q", got)
	}
	if len(landed) != 1 || landed[0] != "notes-1.txt" {
		t.Fatalf("reported %v, expected the name it actually landed under", landed)
	}
}

// One name twice in an offer means the second landing on the first, so the offer is refused whole.
func TestAnOfferCannotNameOneFileTwice(t *testing.T) {
	dir := t.TempDir()
	item := Item{Name: "a.txt", Size: 1}

	out, err := taking(t, dir, []Item{item, item}, spoken(t), nil)
	if err == nil {
		t.Fatal("an offer naming one file twice was taken")
	}

	conn := wire.NewConn(readWriter{out, &bytes.Buffer{}})
	kind, _, err := conn.ReadFrame()
	if err != nil {
		t.Fatalf("reading the answer: %v", err)
	}
	if kind != wire.KindReject {
		t.Fatalf("answered with frame kind %d, expected a reject", kind)
	}

	left, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading %s: %v", dir, err)
	}
	if len(left) != 0 {
		t.Fatalf("%d files were made for a refused offer", len(left))
	}
}

// The sender's permission bits are a stranger's opinion: all that survives is whether it is a
// program, and the directory is this user's alone.
func TestWhatLandsIsNotTheSendersMode(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "in")

	program, plain := []byte("#!/bin/sh\n"), []byte("plain")
	items := []Item{
		{Name: "run.sh", Size: int64(len(program)), Mode: 0o777},
		{Name: "plain.txt", Size: int64(len(plain)), Mode: 0o666},
	}

	sent := spoken(t, spoke{sent: program, whole: program}, spoke{sent: plain, whole: plain})
	if _, err := taking(t, dir, items, sent, nil); err != nil {
		t.Fatalf("receiving: %v", err)
	}

	for name, want := range map[string]os.FileMode{"run.sh": 0o700, "plain.txt": 0o600} {
		stat, err := os.Stat(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("looking at %s: %v", name, err)
		}
		if stat.Mode().Perm() != want {
			t.Fatalf("%s landed as %o, expected %o", name, stat.Mode().Perm(), want)
		}
	}

	stat, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("looking at %s: %v", dir, err)
	}
	if stat.Mode().Perm() != 0o700 {
		t.Fatalf("the receiving directory is %o, expected 700", stat.Mode().Perm())
	}
}

// The .part path is guessable, so a symlink planted there must not be written through.
func TestASymlinkedPartIsNotWrittenThrough(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "target")
	if err := os.WriteFile(outside, []byte("untouched"), 0o600); err != nil {
		t.Fatalf("writing the file outside: %v", err)
	}

	item := Item{Name: "notes", Size: wire.SizeUnknown}
	if err := os.Symlink(outside, filepath.Join(dir, partName(item))); err != nil {
		t.Fatalf("planting a symlink: %v", err)
	}

	body := []byte("written through")
	if _, err := taking(t, dir, []Item{item}, spoken(t, spoke{sent: body, whole: body}), nil); err == nil {
		t.Fatal("a session wrote through a planted symlink")
	}
	if got := read(t, outside); !bytes.Equal(got, []byte("untouched")) {
		t.Fatalf("the file outside is now %q", got)
	}
}

// A symlink where the file would land is something already there, so the item goes beside it.
func TestASymlinkAtTheNameIsNotLandedOn(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "target")
	if err := os.WriteFile(outside, []byte("untouched"), 0o600); err != nil {
		t.Fatalf("writing the file outside: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "notes.txt")); err != nil {
		t.Fatalf("planting a symlink: %v", err)
	}

	body := []byte("landed")
	item := Item{Name: "notes.txt", Size: int64(len(body))}
	if _, err := taking(t, dir, []Item{item}, spoken(t, spoke{sent: body, whole: body}), nil); err != nil {
		t.Fatalf("receiving: %v", err)
	}

	if got := read(t, outside); !bytes.Equal(got, []byte("untouched")) {
		t.Fatalf("the file outside is now %q", got)
	}
	if got := read(t, filepath.Join(dir, "notes-1.txt")); !bytes.Equal(got, body) {
		t.Fatalf("what arrived landed as %q", got)
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
		{"noext", 3, "noext-3"},
	} {
		if got := numbered(at.name, at.n); got != at.want {
			t.Fatalf("numbered(%q, %d) = %q, want %q", at.name, at.n, got, at.want)
		}
	}
}
