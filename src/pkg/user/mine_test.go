package user

import (
	"bytes"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestStoredBadgeKeepsBodyAndSignatureTogether(t *testing.T) {
	where := filepath.Join(t.TempDir(), "badge")
	badge, sig, err := Sign(aKey(t), someDevice, "laptop", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(where, append(badge.Bytes(), sig...), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(where+".sig", []byte("stale"), 0o600); err != nil {
		t.Fatal(err)
	}

	body, stored, err := readStored(where)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(body, badge.Bytes()) || !bytes.Equal(stored, sig) {
		t.Fatal("the stored badge was split incorrectly")
	}
}

func TestLegacyBadgeFilesAreStillRead(t *testing.T) {
	where := filepath.Join(t.TempDir(), "badge")
	badge, sig, err := Sign(aKey(t), someDevice, "laptop", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(where, badge.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(where+".sig", sig, 0o600); err != nil {
		t.Fatal(err)
	}

	body, stored, err := readStored(where)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(body, badge.Bytes()) || !bytes.Equal(stored, sig) {
		t.Fatal("the legacy badge files were not read together")
	}
}

func TestConcurrentCallersWearOneStoredBadge(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	Use("")
	SignWith("")
	now := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)

	type result struct {
		badge Badge
		sig   []byte
		err   error
	}
	got := [8]result{}
	var wg sync.WaitGroup
	for i := range got {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got[i].badge, got[i].sig, got[i].err = Mine(now)
		}()
	}
	wg.Wait()

	for i, result := range got {
		if result.err != nil {
			t.Fatalf("caller %d: Mine(): %v", i, result.err)
		}
		if !bytes.Equal(result.badge.Bytes(), got[0].badge.Bytes()) || !bytes.Equal(result.sig, got[0].sig) {
			t.Fatalf("caller %d wore a different badge", i)
		}
	}

	where, err := badgeAt()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(where)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(raw, signatureMarker) {
		t.Fatal("the stored badge does not contain its signature")
	}
}
