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
	if len(browserProcesses) != 0 {
		t.Fatalf("the test began with %d browser processes", len(browserProcesses))
	}

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
		waitForBrowserProcesses(t)
	}()

	for i := 0; i < cap(browserProcesses); i++ {
		if !openInBrowser("https://example.com/" + strconv.Itoa(i)) {
			t.Fatalf("browser process %d was refused before the limit", i)
		}
	}
	if openInBrowser("https://example.com/over") {
		t.Fatal("a browser process started above the limit")
	}

	if err := os.WriteFile(release, nil, 0o600); err != nil {
		t.Fatalf("releasing openers: %v", err)
	}
	waitForBrowserProcesses(t)
	if !openInBrowser("https://example.com/after") {
		t.Fatal("a browser process stayed refused after capacity returned")
	}
	waitForBrowserProcesses(t)
}

func waitForBrowserProcesses(t *testing.T) {
	t.Helper()

	until := time.Now().Add(2 * time.Second)
	for len(browserProcesses) != 0 && time.Now().Before(until) {
		time.Sleep(time.Millisecond)
	}
	if len(browserProcesses) != 0 {
		t.Fatalf("%d browser processes kept their capacity", len(browserProcesses))
	}
}

func TestBrowserLinksAreBounded(t *testing.T) {
	link := "https://example.com/" + strings.Repeat("x", maxOpenedLink)
	if openInBrowser(link) {
		t.Fatal("an oversized link started a browser process")
	}
}
