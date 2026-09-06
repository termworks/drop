package cmd

import (
	"strings"
	"testing"
)

func TestResealRefusesWhileTheDaemonIsRunning(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())

	path, err := castSocket()
	if err != nil {
		t.Fatal(err)
	}
	guard, err := localGuard(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = guard.Close() }()

	if err := reseal(false); err == nil {
		t.Fatal("reseal() rewrote history while the daemon was running")
	} else if !strings.Contains(err.Error(), "stop drop") {
		t.Fatalf("reseal() = %v", err)
	}
}
