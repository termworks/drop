package share

import (
	"bytes"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bresilla/drop/src/pkg/arch"
	"github.com/bresilla/drop/src/pkg/wire"
)

func TestSharePartNamesAreFixedAndReserved(t *testing.T) {
	item := Item{Name: strings.Repeat("x", maxLandingNameBytes), Size: 42}
	name := partName(idFor(1), transferID{1}, item)
	if len(name) != len(partPrefix)+partDigestBytes*2+len(partSuffix) || !receiverPart(name) {
		t.Fatalf("part name = %q", name)
	}
	if other := partName(idFor(1), transferID{2}, item); other == name {
		t.Fatal("separate transfers share a part name")
	}
	if other := partName(idFor(2), transferID{1}, item); other == name {
		t.Fatal("separate senders share a part name")
	}
}

func TestOnlyExactReceiverPartNamesAreRecognized(t *testing.T) {
	valid := partName(idFor(1), transferID{1}, Item{Name: "one"})
	for _, name := range []string{
		".notes.abcdef012345.part",
		strings.ToUpper(valid),
		valid + ".more",
		valid[:len(valid)-1],
		partPrefix + strings.Repeat("g", partDigestBytes*2) + partSuffix,
	} {
		if receiverPart(name) {
			t.Fatalf("recognized %q", name)
		}
	}
}

func TestRetainedSharePartsArePrunedOldestFirst(t *testing.T) {
	dir := t.TempDir()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()

	item := Item{Name: "one", Size: wire.SizeUnknown}
	names := []string{
		partName(idFor(1), transferID{1}, item),
		partName(idFor(1), transferID{2}, item),
		partName(idFor(1), transferID{3}, item),
	}
	base := time.Now().Add(-time.Hour)
	for i, name := range names {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("1234"), 0o600); err != nil {
			t.Fatal(err)
		}
		at := base.Add(time.Duration(i) * time.Minute)
		if err := os.Chtimes(filepath.Join(dir, name), at, at); err != nil {
			t.Fatal(err)
		}
	}
	user := ".notes.abcdef012345.part"
	if err := os.WriteFile(filepath.Join(dir, user), []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := trimParts(root, 8); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, names[0])); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("oldest part exists: %v", err)
	}
	for _, name := range append(names[1:], user) {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatalf("retained %s: %v", name, err)
		}
	}
}

func TestRetainedSharePartCountIsBounded(t *testing.T) {
	dir := t.TempDir()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()

	item := Item{Name: "one", Size: wire.SizeUnknown}
	for i := range maxRetainedParts + 1 {
		var id transferID
		id[0] = byte(i >> 8)
		id[1] = byte(i)
		if err := os.WriteFile(filepath.Join(dir, partName(idFor(1), id, item)), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := trimParts(root, math.MaxInt64); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != maxRetainedParts {
		t.Fatalf("retained %d empty parts", len(entries))
	}
}

func TestAReceiveSessionBoundsOlderRetainedParts(t *testing.T) {
	dir := t.TempDir()
	item := Item{Name: "stream", Size: wire.SizeUnknown}
	base := time.Now().Add(-time.Hour)
	for i, id := range []transferID{{2}, {3}} {
		name := partName(idFor(9), id, item)
		if err := os.WriteFile(filepath.Join(dir, name), []byte("1234"), 0o600); err != nil {
			t.Fatal(err)
		}
		at := base.Add(time.Duration(i) * time.Minute)
		if err := os.Chtimes(filepath.Join(dir, name), at, at); err != nil {
			t.Fatal(err)
		}
	}

	var input bytes.Buffer
	writer := wire.NewConn(readWriter{&input, &input})
	if err := writer.WriteFrame(wire.KindItem, offer{ID: testTransferID, Items: []Item{item}}.encode()); err != nil {
		t.Fatal(err)
	}
	if err := writer.WriteData([]byte("123")); err != nil {
		t.Fatal(err)
	}
	config := Config{Dir: dir, MaxItemBytes: 4, MaxSessionBytes: 4, instance: newConfigInstance()}
	err := New(Into{}).Serve(t.Context(), arch.Session{
		Path: "/share", Config: config, From: idFor(9),
		Conn: wire.NewConn(readWriter{&input, io.Discard}),
	})
	if err == nil {
		t.Fatal("interrupted transfer returned no error")
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var parts, size int
	for _, entry := range entries {
		if !receiverPart(entry.Name()) {
			continue
		}
		stat, err := entry.Info()
		if err != nil {
			t.Fatal(err)
		}
		parts++
		size += int(stat.Size())
	}
	if parts != 2 || size != 7 {
		t.Fatalf("retained %d parts using %d bytes", parts, size)
	}
}

func TestShareLandingNamesAreBounded(t *testing.T) {
	tooLong := strings.Repeat("x", maxLandingNameBytes+1)
	out, err := taking(t, t.TempDir(), []Item{{Name: tooLong, Size: 0}}, spoken(t), nil)
	if err == nil {
		t.Fatal("overlong landing name was accepted")
	}
	kind, _, err := wire.NewConn(readWriter{out, io.Discard}).ReadFrame()
	if err != nil || kind != wire.KindReject {
		t.Fatalf("overlong answer = %d, %v", kind, err)
	}

	dir := t.TempDir()
	atLimit := strings.Repeat("x", maxLandingNameBytes)
	if _, err := taking(t, dir, []Item{{Name: atLimit, Size: 0}}, spoken(t, spoke{}), nil); err != nil {
		t.Fatalf("boundary landing name returned %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, atLimit)); err != nil {
		t.Fatalf("boundary landing: %v", err)
	}
}
