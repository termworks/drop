//go:build cross

// Two machines, two chips.
//
// Everything else here starts both ends itself, on one host, on one architecture. This one talks
// to a drop that is already serving somewhere else — which is the only way to find out that what
// goes over the wire means the same thing on an arm64 daemon as it does on an amd64 one.
//
//	DROP_PORT=48222 go test -tags cross -count=1 -v ./src/e2e/
//
// $CROSS is the device to reach, by the name it is filed under, and it must be paired already.
// Give the test its own DROP_PORT: a node that fights a running daemon for the default one
// cannot be reached at all, and the failure looks like the far end being down.
//
// The far end must serve a writable files namespace at /work, a read-only one at /read over the
// same directory, and that directory must hold note.txt and deep/inner.txt.
package e2e

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/bresilla/drop/src/pkg/arch/files"
	"github.com/bresilla/drop/src/pkg/book"
	"github.com/bresilla/drop/src/pkg/dial"
	"github.com/bresilla/drop/src/pkg/discovery"
	"github.com/bresilla/drop/src/pkg/node"
	"github.com/bresilla/drop/src/pkg/proto"
)

// far is the device this test talks to.
func far() string {
	if named := os.Getenv("CROSS"); named != "" {
		return named
	}
	return "orin"
}

// browsing opens a files namespace over there, and hands back the way to close everything.
func browsing(t *testing.T, path string) (*files.Browsing, func()) {
	t.Helper()

	entry, err := book.Resolve(far())
	if err != nil {
		t.Skipf("%s is not paired with this machine: %v", far(), err)
	}

	ctx := context.Background()
	n, err := node.Start(ctx)
	if err != nil {
		t.Fatalf("starting a node: %v", err)
	}
	lan, _ := discovery.StartLAN(ctx, n)

	find, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	done, s, err := dial.To(find, n, lan, nil, entry, node.ALPNSession)
	if err != nil {
		n.Close()
		t.Fatalf("reaching %s: %v", far(), err)
	}

	conn, err := proto.Open(s, path, "files", 0, "", node.DisplayName())
	if err != nil {
		done.Close()
		n.Close()
		t.Fatalf("opening %s: %v", path, err)
	}
	b, err := files.Browse(conn)
	if err != nil {
		done.Close()
		n.Close()
		t.Fatalf("browsing %s: %v", path, err)
	}
	return b, func() { done.Close(); n.Close() }
}

func names(of []files.Entry) []string {
	out := make([]string, 0, len(of))
	for _, e := range of {
		out = append(out, e.Name)
	}
	sort.Strings(out)
	return out
}

// The whole of what a files namespace is, against a daemon on another architecture.
func TestAFilesNamespaceIsWalkedAcrossArchitectures(t *testing.T) {
	b, stop := browsing(t, "/work")
	defer stop()

	if !b.Writable() {
		t.Fatal("/work came back read-only")
	}

	at, err := b.List("")
	if err != nil {
		t.Fatalf("listing the root: %v", err)
	}
	t.Logf("/work holds %v", names(at))

	deep, err := b.List("deep")
	if err != nil {
		t.Fatalf("listing deep: %v", err)
	}
	if got := names(deep); len(got) == 0 {
		t.Fatal("deep came back empty")
	}

	into := filepath.Join(t.TempDir(), "note.txt")
	if err := b.Get("note.txt", into, nil); err != nil {
		t.Fatalf("reading note.txt out: %v", err)
	}
	got, err := os.ReadFile(into)
	if err != nil || len(got) == 0 {
		t.Fatalf("what arrived: %q %v", got, err)
	}

	// What a file lands as is decided here, not by the sender.
	stat, _ := os.Stat(into)
	if perm := stat.Mode().Perm(); perm&0o077 != 0 {
		t.Errorf("a downloaded file landed readable by others: %o", perm)
	}

	up := filepath.Join(t.TempDir(), "from-the-other-side.txt")
	if err := os.WriteFile(up, []byte("written on the other architecture\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := b.PutFile("from-the-other-side.txt", up, nil); err != nil {
		t.Fatalf("writing a file in: %v", err)
	}
	if err := b.Mkdir("made-from-here"); err != nil {
		t.Fatalf("making a directory: %v", err)
	}

	after, err := b.List("")
	if err != nil {
		t.Fatalf("listing again: %v", err)
	}
	held := map[string]bool{}
	for _, e := range after {
		held[e.Name] = e.Dir
	}
	if _, ok := held["from-the-other-side.txt"]; !ok {
		t.Error("what was written is not in the listing")
	}
	if dir, ok := held["made-from-here"]; !ok || !dir {
		t.Error("the directory made is not there, or is not a directory")
	}

	if err := b.Remove("from-the-other-side.txt"); err != nil {
		t.Errorf("removing the file: %v", err)
	}
	if err := b.Remove("made-from-here"); err != nil {
		t.Errorf("removing the directory: %v", err)
	}
}

// The containment check, against a real filesystem on the far end rather than a temp dir here.
func TestNothingOutsideAFilesNamespaceIsReachable(t *testing.T) {
	b, stop := browsing(t, "/work")
	defer stop()

	for _, leaving := range []string{
		"../../../etc/passwd",
		"/etc/passwd",
		"deep/../../..",
		"..",
		"deep/../../.ssh",
	} {
		if _, err := b.List(leaving); err == nil {
			t.Errorf("listing %q was allowed", leaving)
		}
	}
	if err := b.Get("../../.ssh/id_ed25519", filepath.Join(t.TempDir(), "taken"), nil); err == nil {
		t.Error("reading outside the namespace was allowed")
	}
	if err := b.Mkdir("../made-outside"); err == nil {
		t.Error("making a directory outside the namespace was allowed")
	}
}

// A namespace the config did not mark writable is read, and nothing else.
func TestAReadOnlyFilesNamespaceRefusesEveryWrite(t *testing.T) {
	b, stop := browsing(t, "/read")
	defer stop()

	if b.Writable() {
		t.Fatal("/read came back writable")
	}
	if _, err := b.List(""); err != nil {
		t.Fatalf("a read-only namespace could not be listed: %v", err)
	}

	up := filepath.Join(t.TempDir(), "nope.txt")
	if err := os.WriteFile(up, []byte("no"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := b.PutFile("nope.txt", up, nil); err == nil {
		t.Error("a read-only namespace took an upload")
	}
	if err := b.Remove("note.txt"); err == nil {
		t.Error("a read-only namespace took a delete")
	}
	if err := b.Mkdir("nope"); err == nil {
		t.Error("a read-only namespace took a directory")
	}
}
