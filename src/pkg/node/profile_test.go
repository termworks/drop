package node

import (
	"path/filepath"
	"strings"
	"testing"
)

// The ordinary profile is where it always was, so setting nothing changes nothing.
func TestNoProfileKeepsTheOrdinaryDirectory(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/tmp/x")

	dir, err := ConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	if dir != filepath.Join("/tmp/x", "drop") {
		t.Errorf("config went to %s", dir)
	}
	if Profile() != "" {
		t.Error("a profile appeared from nowhere")
	}
}

// A profile keeps everything of its own somewhere of its own.
func TestAProfileGetsItsOwnDirectory(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/tmp/x")
	t.Setenv("DROP_PROFILE", "bob")

	dir, err := ConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	if dir != filepath.Join("/tmp/x", "drop", "profiles", "bob") {
		t.Errorf("config went to %s", dir)
	}
}

// A profile name is a directory name. One with a slash or a dot in it would climb out and write
// over the ordinary profile's keys, which is the one thing this must not do.
func TestAProfileCannotClimbOutOfItsDirectory(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/tmp/x")

	for _, bad := range []string{"../../elsewhere", "..", "a/b", "with space", "dot.dot", "~"} {
		t.Setenv("DROP_PROFILE", bad)

		dir, err := ConfigDir()
		if err == nil {
			t.Errorf("DROP_PROFILE=%q was accepted and went to %s", bad, dir)
		}
		if !strings.Contains(err.Error(), "letters, digits") {
			t.Errorf("DROP_PROFILE=%q said %v", bad, err)
		}
	}
}

// Two profiles have to be able to run at once, so they cannot share a port. The ordinary one keeps
// the port anybody has written down.
func TestEachProfileListensSomewhereOfItsOwn(t *testing.T) {
	if got := profilePort(); got != DefaultPort {
		t.Errorf("no profile listens on %d, wanted %d", got, DefaultPort)
	}

	seen := map[uint16]string{}
	for _, name := range []string{"bob", "carol", "dave", "work", "test"} {
		t.Setenv("DROP_PROFILE", name)

		at := profilePort()
		if at == DefaultPort {
			t.Errorf("%s took the ordinary port", name)
		}
		if was, clash := seen[at]; clash {
			t.Errorf("%s and %s both want port %d", name, was, at)
		}
		seen[at] = name

		// Stable, or an address written down for a profile stops working.
		if again := profilePort(); again != at {
			t.Errorf("%s moved from %d to %d", name, at, again)
		}
	}
}

// $DROP_PORT still wins, because a profile on a machine behind a forwarded port has to be able to
// say where it is.
func TestAnExplicitPortStillWins(t *testing.T) {
	t.Setenv("DROP_PROFILE", "bob")
	t.Setenv("DROP_PORT", "50505")

	if got := Port(); got != 50505 {
		t.Errorf("port = %d, wanted the one that was asked for", got)
	}
}
