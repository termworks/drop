package cmd

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/bresilla/drop/src/pkg/book"
	"github.com/bresilla/drop/src/pkg/convo"
	"github.com/bresilla/drop/src/pkg/proto"
	"github.com/bresilla/drop/src/pkg/shares"
)

func TestCurrentSharesSurviveCacheWriteFailure(t *testing.T) {
	blocked := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blocked, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_DATA_HOME", blocked)

	with := book.Entry{Name: "beta", ID: idFor(66)}
	want := []proto.Served{{Path: "/current", Archetype: "chat"}}
	got, err := availableServes(with, want, nil)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("available shares = %+v", got)
	}
	var current interface{ CurrentData() bool }
	if !errors.As(err, &current) || !current.CurrentData() {
		t.Fatalf("cache failure = %v", err)
	}
}

func TestCurrentSharesAreCached(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	with := book.Entry{Name: "beta", ID: idFor(67)}
	want := []proto.Served{{Path: "/current", Archetype: "chat"}}
	if got, err := availableServes(with, want, nil); err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("available shares = %+v, %v", got, err)
	}
	if got, err := shares.Recall(with.ID); err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("cached shares = %+v, %v", got, err)
	}
}

func TestOfflineSharesUseTheCache(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	with := book.Entry{Name: "beta", ID: idFor(69)}
	want := []proto.Served{{Path: "/old", Archetype: "chat"}}
	if err := shares.Remember(with.ID, want); err != nil {
		t.Fatal(err)
	}
	offline := errors.New("beta is unreachable")
	got, err := availableServes(with, nil, offline)
	if !reflect.DeepEqual(got, want) || !errors.Is(err, offline) {
		t.Fatalf("offline shares = %+v, %v", got, err)
	}
}

func TestOfflineCacheErrorKeepsBothFailures(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	with := book.Entry{Name: "beta", ID: idFor(68)}
	if err := shares.Remember(with.ID, []proto.Served{{Path: "/old"}}); err != nil {
		t.Fatal(err)
	}
	base, err := convo.DataDir()
	if err != nil {
		t.Fatal(err)
	}
	cache := filepath.Join(base, "shares", with.ID.String()+".json")
	if err := os.WriteFile(cache, []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}

	offline := errors.New("beta is unreachable")
	got, err := availableServes(with, nil, offline)
	if got != nil || !errors.Is(err, offline) || !strings.Contains(err.Error(), "reading cached namespaces") {
		t.Fatalf("offline cache result = %+v, %v", got, err)
	}
}
