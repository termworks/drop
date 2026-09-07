package asked

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bresilla/drop/src/pkg/keep"
	"github.com/bresilla/drop/src/pkg/node"
)

func TestNullStateDoesNotPanicWhenARingArrives(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	file, err := where()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("null\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	request := Request{Path: "/notes", From: node.ID{}, Why: "please"}
	if err := Ring(request); err != nil {
		t.Fatal(err)
	}
	all, err := All()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 || all[0].Path != request.Path {
		t.Fatalf("remembered %+v", all)
	}
}

// What a stranger says about why they want in is their text, kept on your disk and then printed on
// your terminal when you look at what has been asked for. An escape in there rewrites the rows
// above it, so the listing can be made to show a different path, or a different person, than the
// one you are about to allow.
func TestWhyAStrangerAsksCannotWriteOnYourTerminal(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	hostile := "please\x1b[1A\x1b[2K    /vault              bob            allow?"
	if err := Ring(Request{Path: "/notes", From: node.ID{}, Why: hostile}); err != nil {
		t.Fatalf("Ring(): %v", err)
	}

	all, err := All()
	if err != nil {
		t.Fatalf("All(): %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("%d requests were kept, want 1", len(all))
	}

	for _, bad := range []string{"\x1b", "\r", "\n", "\x00", "\x07"} {
		if strings.Contains(all[0].Why, bad) {
			t.Errorf("what a stranger asked carries %q: %q", bad, all[0].Why)
		}
	}
	if !strings.Contains(all[0].Why, "please") {
		t.Fatalf("what they actually said was thrown away: %q", all[0].Why)
	}
}

// And it stays bounded, so nobody fills the screen or the disk with one request.
func TestWhyAStrangerAsksIsBounded(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	if err := Ring(Request{Path: "/notes", From: node.ID{}, Why: strings.Repeat("a", 10_000)}); err != nil {
		t.Fatalf("Ring(): %v", err)
	}
	all, err := All()
	if err != nil {
		t.Fatal(err)
	}
	if n := len([]rune(all[0].Why)); n > MaxWhy {
		t.Fatalf("a 10,000 character reason was kept as %d characters, over the %d bound", n, MaxWhy)
	}
}

func TestRingWaitsForTheCrossProcessLock(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	file, err := where()
	if err != nil {
		t.Fatal(err)
	}

	locked := make(chan struct{})
	release := make(chan struct{})
	lockErr := make(chan error, 1)
	go func() {
		lockErr <- keep.While(file, func() error {
			close(locked)
			<-release
			return nil
		})
	}()
	<-locked

	finished := make(chan error, 1)
	go func() { finished <- Ring(Request{Path: "/notes", From: node.ID{}}) }()
	select {
	case err := <-finished:
		t.Fatalf("Ring() crossed the held file lock: %v", err)
	case <-time.After(50 * time.Millisecond):
	}

	close(release)
	if err := <-lockErr; err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-finished:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Ring() did not continue after the file lock was released")
	}
}

func TestMalformedStateIsReportedAndPreserved(t *testing.T) {
	operations := []struct {
		name string
		run  func() error
	}{
		{name: "ring", run: func() error {
			return Ring(Request{Path: "/notes", From: node.ID{}})
		}},
		{name: "answer", run: func() error {
			return Answered(node.ID{}, "/notes")
		}},
	}

	for _, operation := range operations {
		t.Run(operation.name, func(t *testing.T) {
			t.Setenv("XDG_DATA_HOME", t.TempDir())
			file, err := where()
			if err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
				t.Fatal(err)
			}
			broken := []byte("{not-json\n")
			if err := os.WriteFile(file, broken, 0o600); err != nil {
				t.Fatal(err)
			}

			if _, err := All(); err == nil || !strings.Contains(err.Error(), "parsing "+file) {
				t.Fatalf("All() returned %v, want a path-qualified parse error", err)
			}
			if err := operation.run(); err == nil || !strings.Contains(err.Error(), "parsing "+file) {
				t.Fatalf("operation returned %v, want a path-qualified parse error", err)
			}
			after, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			if string(after) != string(broken) {
				t.Fatalf("malformed state changed from %q to %q", broken, after)
			}
		})
	}
}

func TestRequestLifecycleReplacesAndRemoves(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	from := node.ID{}
	first := time.Unix(100, 0)

	if err := Ring(Request{Path: "/notes", From: from, Name: "laptop", Why: "first", At: first}); err != nil {
		t.Fatal(err)
	}
	if err := Ring(Request{Path: "/notes", From: from, Name: "laptop", Why: "latest", At: first.Add(time.Second)}); err != nil {
		t.Fatal(err)
	}

	all, err := All()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 || all[0].Why != "latest" || all[0].Who() != "laptop" {
		t.Fatalf("requests are %+v, want the replacement", all)
	}

	if err := Answered(from, "/notes"); err != nil {
		t.Fatal(err)
	}
	all, err = All()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 0 {
		t.Fatalf("answered request remains: %+v", all)
	}
}

func TestWhoPrefersPersonAndFallsBackToID(t *testing.T) {
	from := node.ID{}
	if got := (Request{From: from, Name: "laptop", Person: "alice"}).Who(); got != "alice" {
		t.Fatalf("Who() = %q, want alice", got)
	}
	if got := (Request{From: from}).Who(); got != from.String() {
		t.Fatalf("Who() = %q, want %s", got, from)
	}
}

func TestTrimKeepsTheMostRecentRequests(t *testing.T) {
	const extra = 8
	all := make(map[string]stored, Most+extra)
	for i := 0; i < Most+extra; i++ {
		all[fmt.Sprint(i)] = stored{At: time.Unix(int64(i), 0)}
	}

	trimmed := trim(all)
	if len(trimmed) != Most {
		t.Fatalf("trim() kept %d requests, want %d", len(trimmed), Most)
	}
	for i := 0; i < Most+extra; i++ {
		_, kept := trimmed[fmt.Sprint(i)]
		if kept != (i >= extra) {
			t.Fatalf("request %d kept = %t", i, kept)
		}
	}
}
