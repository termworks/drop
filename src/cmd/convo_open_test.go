package cmd

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestBrowserOpenersHaveAProcessWideLimit(t *testing.T) {
	gate := newBrowserGate()

	dir := t.TempDir()
	release := filepath.Join(dir, "release")
	opener := filepath.Join(dir, "opener")
	script := "#!/bin/sh\nwhile [ ! -e \"$DROP_TEST_RELEASE\" ]; do sleep 0.01; done\n"
	if err := os.WriteFile(opener, []byte(script), 0o700); err != nil {
		t.Fatalf("writing opener: %v", err)
	}
	t.Setenv("DROP_OPENER", opener)
	t.Setenv("DROP_TEST_RELEASE", release)
	defer func() {
		_ = os.WriteFile(release, nil, 0o600)
		waitForBrowserProcesses(t, gate)
	}()

	for i := 0; i < cap(gate.processes); i++ {
		if !openWithBrowser("https://example.com/"+strconv.Itoa(i), gate) {
			t.Fatalf("browser process %d was refused before the limit", i)
		}
	}
	if openWithBrowser("https://example.com/over", gate) {
		t.Fatal("a browser process started above the limit")
	}

	if err := os.WriteFile(release, nil, 0o600); err != nil {
		t.Fatalf("releasing openers: %v", err)
	}
	waitForBrowserProcesses(t, gate)
	if !openWithBrowser("https://example.com/after", gate) {
		t.Fatal("a browser process stayed refused after capacity returned")
	}
	waitForBrowserProcesses(t, gate)
}

func waitForBrowserProcesses(t *testing.T, gate *browserGate) {
	t.Helper()

	until := time.Now().Add(2 * time.Second)
	for len(gate.processes) != 0 && time.Now().Before(until) {
		time.Sleep(time.Millisecond)
	}
	if len(gate.processes) != 0 {
		t.Fatalf("%d browser processes kept their capacity", len(gate.processes))
	}
}

func TestBrowserLinksAreBounded(t *testing.T) {
	link := "https://example.com/" + strings.Repeat("x", maxOpenedLink)
	if openWithBrowser(link, newBrowserGate()) {
		t.Fatal("an oversized link started a browser process")
	}
}

func TestBrowserLaunchesAreRateLimited(t *testing.T) {
	gate := newBrowserGate()
	now := time.Unix(1_000_000, 0)

	for i := 0; i < maxBrowserStarts; i++ {
		if !gate.take(now) {
			t.Fatalf("browser launch %d was refused before the rate limit", i)
		}
		gate.give()
	}
	if gate.take(now) {
		gate.give()
		t.Fatal("a browser launch crossed the rate limit")
	}
	if !gate.take(now.Add(browserWindow)) {
		t.Fatal("a browser launch stayed refused after the rate window")
	}
	gate.give()
}
