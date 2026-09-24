package keep

import (
	"math"
	"os"
	"testing"
)

func TestIncomingWritesLeaveFreeSpace(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "space")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()

	if err := Room(file, 0); err != nil {
		t.Fatalf("Room(0): %v", err)
	}
	if err := Room(file, math.MaxInt64); err == nil {
		t.Fatal("an impossibly large incoming write was accepted")
	}
	if err := Room(file, -1); err == nil {
		t.Fatal("a negative incoming write was accepted")
	}
}
