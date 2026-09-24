package cmd

import (
	"bufio"
	"context"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/bresilla/drop/src/pkg/made"
	"github.com/bresilla/drop/src/pkg/ns"
)

// declaring is a config file and a config directory of this test's own.
func declaring(t *testing.T, body string) string {
	t.Helper()

	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "config"))

	config := filepath.Join(dir, "init.lua")
	if err := os.WriteFile(config, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DROP_CONFIG", config)
	return config
}

// A path in the config carries a rule somebody wrote by hand over something of theirs. A command
// must not stand in for it, and must say where to go instead -- the same answer a handoff gives.
func TestCreatingOverADeclaredPathIsRefusedAndNamesTheConfig(t *testing.T) {
	config := declaring(t, `require("drop").mount("/work", { type = "chat", access = "paired" })`)

	entry := made.Entry{Archetype: "chat", Access: made.Access{Paired: true}}
	err := runCreate(context.Background(), reading(), "/work", entry, false)
	if err == nil {
		t.Fatal("it was created over the config")
	}
	if !strings.Contains(err.Error(), config) {
		t.Errorf("the refusal does not name the config: %v", err)
	}
}

func TestRemovingADeclaredPathIsRefusedAndNamesTheConfig(t *testing.T) {
	config := declaring(t, `require("drop").mount("/work", { type = "chat", access = "paired" })`)

	err := runRemove(t.Context(), "/work")
	if err == nil {
		t.Fatal("a path in the config was removed")
	}
	if !strings.Contains(err.Error(), config) {
		t.Errorf("the refusal does not name the config: %v", err)
	}
}

// A namespace put up without saying who may reach it is one anybody paired could open, and a
// setting may name a command to run.
func TestCreatingWithNoAccessIsRefused(t *testing.T) {
	if _, err := admitting("", ""); err == nil {
		t.Fatal("a namespace was created open to whoever the default is")
	}
}

func TestTheThreeSetFlagsSayThreeDifferentThings(t *testing.T) {
	got, err := settings([]string{"dir=~/notes", "word=true"}, []string{"writable", "hidden=false"}, []string{"only=a, b"})
	if err != nil {
		t.Fatalf("reading the flags: %v", err)
	}

	want := made.Settings{
		"dir":      "~/notes",
		"word":     "true",
		"writable": true,
		"hidden":   false,
		"only":     []string{"a", "b"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("came out %#v", got)
	}
}

func TestAFlagThatIsNeitherOnNorOffIsRefused(t *testing.T) {
	if _, err := settings(nil, []string{"writable=maybe"}, nil); err == nil {
		t.Fatal("a flag was set to something that is not on or off")
	}
}

func TestAnAccessRuleReadsTheWordsAConfigUses(t *testing.T) {
	for rule, want := range map[string]made.Access{
		"paired":         {Paired: true},
		"trusted":        {Trusted: true},
		"anyone":         {Anyone: true},
		"bob,carol@work": {Named: []string{"bob", "carol@work"}},
	} {
		got, err := admitting(rule, "")
		if err != nil {
			t.Fatalf("--access %s: %v", rule, err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("--access %s came out %#v", rule, got)
		}
	}
}

// A held namespace is served for exactly the lifetime of its command.
func TestAHeldNamespaceStopsBeingServed(t *testing.T) {
	mounts := ns.NewTable()
	host := newMountHost(mounts, reading())

	up := made.Line{Path: "/notes", Entry: made.Entry{
		Archetype: "files",
		Settings:  map[string]any{"dir": t.TempDir()},
		Access:    made.Access{Paired: true},
	}}

	if err := host.begin(up); err != nil {
		t.Fatalf("putting /notes up: %v", err)
	}
	if _, _, ok := mounts.Lookup("/notes"); !ok {
		t.Fatal("/notes was not put up at all")
	}
	host.end("/notes")

	if _, _, ok := mounts.Lookup("/notes"); ok {
		t.Fatal("/notes is still served after being taken off the list")
	}
	if err := mounts.Add(ns.Mount{Path: "/notes", Archetype: "chat"}); err != nil {
		t.Fatal(err)
	}
	host.end("/notes")
	if mount, _, ok := mounts.Lookup("/notes"); !ok || mount.Archetype != "chat" {
		t.Fatal("the old command took down a replacement namespace")
	}
}

func TestAHeldNamespaceTeardownNormalizesItsPath(t *testing.T) {
	mounts := ns.NewTable()
	host := newMountHost(mounts, reading())
	up := made.Line{Path: "//notes/", Entry: made.Entry{
		Archetype: "chat",
		Access:    made.Access{Paired: true},
	}}

	if err := host.begin(up); err != nil {
		t.Fatalf("putting /notes up: %v", err)
	}
	host.end(up.Path)

	if _, _, ok := mounts.Lookup("/notes"); ok {
		t.Fatal("the normalized mount survived its command")
	}
}

func TestAWrittenNamespaceCanBeUpdatedAndRemoved(t *testing.T) {
	mounts := ns.NewTable()
	host := newMountHost(mounts, reading())

	first := made.Line{Path: "/notes", Keep: true, Entry: made.Entry{
		Archetype: "chat",
		Access:    made.Access{Paired: true},
	}}
	second := first
	second.Access = made.Access{Trusted: true}

	if err := host.begin(first); err != nil {
		t.Fatal(err)
	}
	if err := host.begin(second); err != nil {
		t.Fatalf("updating the written namespace: %v", err)
	}
	mount, _, ok := mounts.Lookup("/notes")
	if !ok || !mount.Access.AnyTrusted || mount.Access.AnyPaired {
		t.Fatalf("the updated rule is %+v", mount.Access)
	}
	if !host.removeWritten("/notes") {
		t.Fatal("the updated written namespace was not removed")
	}
}

func TestAStartupWrittenNamespaceStopsBeingServed(t *testing.T) {
	mounts := ns.NewTable()
	if err := mounts.Add(ns.Mount{Path: "/notes", Source: ns.Written, Archetype: "chat"}); err != nil {
		t.Fatal(err)
	}
	host := newMountHost(mounts, reading())
	server, client := net.Pipe()
	defer func() { _ = client.Close() }()

	done := make(chan error, 1)
	go func() {
		defer func() { _ = server.Close() }()
		done <- takeUnmount(host, server, "/notes")
	}()

	if line, err := bufio.NewReader(client).ReadString('\n'); err != nil || line != "ok\n" {
		t.Fatalf("unmount reply = %q, %v", line, err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if _, _, ok := mounts.Lookup("/notes"); ok {
		t.Fatal("the startup-loaded namespace is still served")
	}
}

func TestUnmountDoesNotRemoveAHeldNamespace(t *testing.T) {
	mounts := ns.NewTable()
	if err := mounts.Add(ns.Mount{Path: "/notes", Source: ns.Held, Archetype: "chat"}); err != nil {
		t.Fatal(err)
	}
	host := newMountHost(mounts, reading())
	server, client := net.Pipe()
	defer func() { _ = client.Close() }()

	done := make(chan error, 1)
	go func() {
		defer func() { _ = server.Close() }()
		done <- takeUnmount(host, server, "/notes")
	}()

	line, err := bufio.NewReader(client).ReadString('\n')
	if err != nil || line != "no something is holding that open\n" {
		t.Fatalf("unmount reply = %q, %v", line, err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if _, _, ok := mounts.Lookup("/notes"); !ok {
		t.Fatal("the held namespace was removed")
	}
}

func TestUnmountDoesNotRemoveAConfiguredNamespace(t *testing.T) {
	mounts := ns.NewTable()
	if err := mounts.Add(ns.Mount{Path: "/notes", Source: ns.Configured, Archetype: "chat"}); err != nil {
		t.Fatal(err)
	}
	host := newMountHost(mounts, reading())
	server, client := net.Pipe()
	defer func() { _ = client.Close() }()

	done := make(chan error, 1)
	go func() {
		defer func() { _ = server.Close() }()
		done <- takeUnmount(host, server, "/notes")
	}()

	line, err := bufio.NewReader(client).ReadString('\n')
	if err != nil || line != "no this node did not put that up\n" {
		t.Fatalf("unmount reply = %q, %v", line, err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if _, _, ok := mounts.Lookup("/notes"); !ok {
		t.Fatal("the configured namespace was removed")
	}
}
