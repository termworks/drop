package cmd

import (
	"testing"
	"time"

	"github.com/bresilla/drop/src/pkg/book"
	"github.com/bresilla/drop/src/pkg/node"
	"github.com/bresilla/drop/src/pkg/seen"
)

func TestStrangerPersistenceHasAGlobalWriteBound(t *testing.T) {
	pinned := &book.Book{}
	now := time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)
	writes := 0
	note := notingWith(pinned, func() time.Time { return now }, func(node.ID, string, string, time.Time) error {
		writes++
		return nil
	})

	note(idFor(1), "/work", "refused")
	now = now.Add(notingEvery - time.Second)
	for i := 2; i <= seen.Most; i++ {
		note(idFor(byte(i)), "/work", "refused")
	}
	note(idFor(100), "/work", "refused")
	if writes != seen.Most {
		t.Fatalf("unique strangers caused %d writes inside thirty seconds, want %d", writes, seen.Most)
	}

	now = now.Add(time.Second)
	note(idFor(100), "/work", "refused")
	if writes != seen.Most+1 {
		t.Fatal("persistence did not resume when the oldest write left the window")
	}
	note(idFor(101), "/work", "refused")
	if writes != seen.Most+1 {
		t.Fatal("the sliding window admitted more than its global ceiling")
	}
}

func TestRepeatedStrangerKnocksAreCoalesced(t *testing.T) {
	pinned := &book.Book{}
	now := time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)
	writes := 0
	note := notingWith(pinned, func() time.Time { return now }, func(node.ID, string, string, time.Time) error {
		writes++
		return nil
	})

	for range 10 {
		note(idFor(1), "/work", "refused")
	}
	if writes != 1 {
		t.Fatalf("one stranger caused %d writes inside one window", writes)
	}

	now = now.Add(notingEvery)
	note(idFor(1), "/work", "refused")
	if writes != 2 {
		t.Fatal("the stranger could not be recorded again in the next window")
	}
}
