package cmd

import (
	stdbytes "bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bresilla/drop/src/pkg/convo"
	"github.com/bresilla/drop/src/pkg/history"
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

func TestResealRewritesConversationsAndSharedHistories(t *testing.T) {
	asSomebody(t)
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	writeVaultConfig(t)
	convo.Unlock(nil)
	history.Unlock(nil)
	t.Cleanup(func() {
		convo.Unlock(nil)
		history.Unlock(nil)
	})

	peer := idFor(31)
	store, err := convo.Open(peer)
	if err != nil {
		t.Fatal(err)
	}
	message, err := convo.New(convo.KindText, "conversation plaintext secret", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Add(message); err != nil {
		t.Fatal(err)
	}

	logs := make(map[string]*history.Log)
	want := make(map[string][]byte)
	for i, thing := range []string{"shared-alpha", "shared-zeta"} {
		log, err := history.Open(thing)
		if err != nil {
			t.Fatal(err)
		}
		change, err := history.Sign(thing, []byte(fmt.Sprintf("shared plaintext secret %d", i)), nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := log.Add(change); err != nil {
			t.Fatal(err)
		}
		logs[thing] = log
		want[thing] = append([]byte(nil), change.Encode()...)
	}

	before := dataOnDisk(t)
	for _, secret := range []string{"conversation plaintext secret", "shared plaintext secret 0", "shared plaintext secret 1"} {
		if !stdbytes.Contains(before, []byte(secret)) {
			t.Fatalf("%q was not plaintext before sealing", secret)
		}
	}

	var output stdbytes.Buffer
	if err := resealTo(&output, true); err != nil {
		t.Fatalf("reseal(true): %v", err)
	}
	if got := output.String(); !strings.Contains(got, "1 conversation(s) sealed") ||
		!strings.Contains(got, "2 shared history(s) sealed") {
		t.Fatalf("reseal(true) output:\n%s", got)
	}
	after := dataOnDisk(t)
	for _, secret := range []string{"conversation plaintext secret", "shared plaintext secret 0", "shared plaintext secret 1"} {
		if stdbytes.Contains(after, []byte(secret)) {
			t.Fatalf("%q remains plaintext after sealing", secret)
		}
	}
	assertResealedRecords(t, store, message, logs, want)

	output.Reset()
	if err := resealTo(&output, false); err != nil {
		t.Fatalf("reseal(false): %v", err)
	}
	if got := output.String(); !strings.Contains(got, "1 conversation(s) put back in the clear") ||
		!strings.Contains(got, "2 shared history(s) put back in the clear") {
		t.Fatalf("reseal(false) output:\n%s", got)
	}
	cleared := dataOnDisk(t)
	for _, secret := range []string{"conversation plaintext secret", "shared plaintext secret 0", "shared plaintext secret 1"} {
		if !stdbytes.Contains(cleared, []byte(secret)) {
			t.Fatalf("%q is not plaintext after clearing", secret)
		}
	}
	assertResealedRecords(t, store, message, logs, want)
}

func TestResealPrintsNothingWhenASharedHistoryFails(t *testing.T) {
	asSomebody(t)
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	writeVaultConfig(t)
	convo.Unlock(nil)
	history.Unlock(nil)
	t.Cleanup(func() {
		convo.Unlock(nil)
		history.Unlock(nil)
	})

	bad := filepath.Join(os.Getenv("XDG_DATA_HOME"), "drop", "history", "broken", "log")
	if err := os.MkdirAll(bad, 0o700); err != nil {
		t.Fatal(err)
	}
	var output stdbytes.Buffer
	err := resealTo(&output, true)
	if err == nil || !strings.Contains(err.Error(), "shared history broken") {
		t.Fatalf("reseal(true) = %v", err)
	}
	if output.Len() != 0 {
		t.Fatalf("failed reseal printed success:\n%s", output.String())
	}
}

func writeVaultConfig(t *testing.T) {
	t.Helper()
	dir := filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "drop")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	key := filepath.Join(dir, "vault.key")
	body := "local drop = require(\"drop\")\n" +
		fmt.Sprintf("drop.vault = %q\n", key) +
		"drop.mount(\"/chat\", { type = \"chat\" })\n"
	if err := os.WriteFile(filepath.Join(dir, "init.lua"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func dataOnDisk(t *testing.T) []byte {
	t.Helper()
	root := filepath.Join(os.Getenv("XDG_DATA_HOME"), "drop")
	var out []byte
	if err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		raw, err := os.ReadFile(path)
		out = append(out, raw...)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return out
}

func assertResealedRecords(t *testing.T, store *convo.Store, message convo.Message, logs map[string]*history.Log, want map[string][]byte) {
	t.Helper()
	messages, err := store.History()
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1 || messages[0] != message {
		t.Fatalf("conversation changed while resealing: %+v", messages)
	}
	for thing, log := range logs {
		changes, err := log.Ordered()
		if err != nil {
			t.Fatal(err)
		}
		if len(changes) != 1 || !stdbytes.Equal(changes[0].Encode(), want[thing]) {
			t.Fatalf("shared history %s changed while resealing", thing)
		}
	}
}
