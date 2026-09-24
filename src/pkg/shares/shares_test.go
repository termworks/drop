package shares

import (
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"

	"github.com/tmc/go-iroh/key"

	"github.com/bresilla/drop/src/pkg/node"
	"github.com/bresilla/drop/src/pkg/proto"
)

func peerFor(seed byte) node.ID {
	var raw [32]byte
	for i := range raw {
		raw[i] = seed
	}
	return key.NewSecretKey(raw).Public().EndpointID()
}

func TestSharesRoundTrip(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	peer := peerFor(1)
	want := []proto.Served{{Path: "/notes", Archetype: "chat", Shape: "chat", Version: 2, Writable: true, About: "notes"}}
	if err := Remember(peer, want); err != nil {
		t.Fatal(err)
	}
	got, err := Recall(peer)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("Recall() = %+v, %v", got, err)
	}

	file, err := at(peer)
	if err != nil {
		t.Fatal(err)
	}
	stat, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	if stat.Mode().Perm() != 0o600 {
		t.Fatalf("stored mode = %v", stat.Mode().Perm())
	}
}

func TestUnknownAndForgottenSharesAreEmpty(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	peer := peerFor(2)
	if got, err := Recall(peer); err != nil || got != nil {
		t.Fatalf("Recall() = %+v, %v", got, err)
	}
	if err := Forget(peer); err != nil {
		t.Fatal(err)
	}
	if err := Remember(peer, []proto.Served{{Path: "/one"}}); err != nil {
		t.Fatal(err)
	}
	if err := Forget(peer); err != nil {
		t.Fatal(err)
	}
	if got, err := Recall(peer); err != nil || got != nil {
		t.Fatalf("Recall() after Forget = %+v, %v", got, err)
	}
}

func TestMalformedSharesAreReported(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	peer := peerFor(3)
	file, err := at(peer)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Recall(peer); err == nil {
		t.Fatal("Recall() accepted malformed storage")
	}
}

func TestSharesStayWholeDuringConcurrentReplacement(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	peer := peerFor(4)
	choices := [][]proto.Served{{{Path: "/one"}}, {{Path: "/two"}}}

	var writers sync.WaitGroup
	for i := range 8 {
		writers.Add(1)
		go func() {
			defer writers.Done()
			for range 20 {
				if err := Remember(peer, choices[i%len(choices)]); err != nil {
					t.Errorf("Remember(): %v", err)
				}
			}
		}()
	}
	for range 100 {
		if got, err := Recall(peer); err != nil || len(got) > 1 {
			t.Fatalf("Recall() = %+v, %v", got, err)
		}
	}
	writers.Wait()
}

func TestSharesAreBounded(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	peer := peerFor(5)
	if err := Remember(peer, make([]proto.Served, proto.MaxServed+1)); err == nil {
		t.Fatal("Remember() accepted too many namespaces")
	}
}
