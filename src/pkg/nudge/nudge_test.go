package nudge

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// waited is a nudge, or nothing within long enough that nothing is the answer.
func waited(t *testing.T, e *Ear, how time.Duration) bool {
	t.Helper()

	select {
	case _, open := <-e.Heard():
		return open
	case <-time.After(how):
		return false
	}
}

func listening(t *testing.T, dirs ...string) *Ear {
	t.Helper()

	ctx, stop := context.WithCancel(context.Background())
	t.Cleanup(stop)

	e, err := Listen(ctx)
	if err != nil {
		t.Skipf("this machine will not listen for changes: %v", err)
	}
	e.Mind(dirs)
	return e
}

// A file saved under a watched directory is heard about, so a loop does not sit out its whole turn
// waiting to be told what already happened.
func TestASaveIsHeard(t *testing.T) {
	dir := t.TempDir()
	e := listening(t, dir)

	if err := os.WriteFile(filepath.Join(dir, "notes.md"), []byte("one\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !waited(t, e, 2*time.Second) {
		t.Fatal("a file saved under a watched directory was not heard")
	}
}

// Many events for one save are one nudge, because whoever is told goes and looks at everything.
func TestOneSaveIsOneNudge(t *testing.T) {
	dir := t.TempDir()
	e := listening(t, dir)

	at := filepath.Join(dir, "notes.md")
	for i := range 20 {
		if err := os.WriteFile(at, []byte{byte(i)}, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if !waited(t, e, 2*time.Second) {
		t.Fatal("twenty writes were not heard at all")
	}

	// Whatever is left over settles; after that it is quiet until something else happens.
	for waited(t, e, 300*time.Millisecond) {
	}
	if waited(t, e, 500*time.Millisecond) {
		t.Fatal("a directory nobody is touching went on nudging")
	}
}

// Minding a different set stops hearing about the old one and starts hearing about the new.
func TestWhatIsMindedIsWhatIsHeard(t *testing.T) {
	was, now := t.TempDir(), t.TempDir()
	e := listening(t, was)

	e.Mind([]string{now})
	for waited(t, e, 300*time.Millisecond) {
	}

	if err := os.WriteFile(filepath.Join(was, "x"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if waited(t, e, 700*time.Millisecond) {
		t.Fatal("a directory that is no longer minded was still heard")
	}

	if err := os.WriteFile(filepath.Join(now, "x"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !waited(t, e, 2*time.Second) {
		t.Fatal("a directory that is now minded was not heard")
	}
}

// A directory that cannot be watched is passed over rather than taking the rest down with it: it is
// one the timer will notice, which is what the timer is for.
func TestADirectoryThatCannotBeWatchedIsPassedOver(t *testing.T) {
	dir := t.TempDir()
	e := listening(t, filepath.Join(dir, "nothing-is-here"), dir)

	if err := os.WriteFile(filepath.Join(dir, "x"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !waited(t, e, 2*time.Second) {
		t.Fatal("one directory that could not be watched stopped the one that could")
	}
}

// Stopping puts everything down, and whoever was selecting on it is not left there.
func TestStoppingClosesWhatWasBeingHeard(t *testing.T) {
	dir := t.TempDir()
	ctx, stop := context.WithCancel(context.Background())

	e, err := Listen(ctx)
	if err != nil {
		t.Skipf("this machine will not listen for changes: %v", err)
	}
	e.Mind([]string{dir})

	stop()
	deadline := time.After(3 * time.Second)
	for {
		select {
		case _, open := <-e.Heard():
			if !open {
				// And minding after it is over is not a panic.
				e.Mind([]string{dir})
				return
			}
		case <-deadline:
			t.Fatal("stopping left whoever was listening waiting")
		}
	}
}
