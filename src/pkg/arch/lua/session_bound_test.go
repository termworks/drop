package lua

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	rt "github.com/arnodel/golua/runtime"

	"github.com/bresilla/drop/src/pkg/wire"
)

func TestSweepRemovesMoreThanOneBatch(t *testing.T) {
	where := t.TempDir()
	mark := "123456789abc"
	for i := range sweepBatch*3 + 17 {
		name := itoa(i) + "." + mark
		if err := os.WriteFile(filepath.Join(where, name), nil, 0o600); err != nil {
			t.Fatalf("writing marked file %d: %v", i, err)
		}
	}
	if err := os.WriteFile(filepath.Join(where, "kept"), nil, 0o600); err != nil {
		t.Fatalf("writing unmarked file: %v", err)
	}

	dir, err := os.OpenRoot(where)
	if err != nil {
		t.Fatalf("opening root: %v", err)
	}
	s := &session{dir: dir, mark: mark}
	s.sweep()
	_ = dir.Close()

	entries, err := os.ReadDir(where)
	if err != nil {
		t.Fatalf("reading swept directory: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "kept" {
		t.Fatalf("sweep left %d entries: %v", len(entries), entries)
	}
}

func TestMineAcceptsOnlyOneBoundedFileName(t *testing.T) {
	p := written(t, `
		drop.archetype{
			name  = "namer",
			read  = function(d) return {} end,
			note  = function(c) return {} end,
			serve = function(s, c)
				while true do
					local name = s:read()
					if not name then return end
					local ok = pcall(function() s:mine(name) end)
					s:write(tostring(ok))
				end
			end,
		}
	`)

	conn, client, done := opened(t, p, nil)
	defer func() { _ = client.Close() }()

	for name, want := range map[string]string{
		"image.jpg": "true",
		strings.Repeat("x", MaxName-markLength-1): "true",
		"deeper/image.jpg":                        "false",
		"":                                        "false",
		".":                                       "false",
		"..":                                      "false",
		strings.Repeat("x", MaxName-markLength):   "false",
	} {
		if err := conn.WriteFrame(wire.KindItem, []byte(name)); err != nil {
			t.Fatalf("sending name %q: %v", name, err)
		}
		if _, body := said(t, conn); string(body) != want {
			t.Errorf("s:mine(%q) said %q, wanted %q", name, body, want)
		}
	}
	_ = client.Close()
	if err := ended(t, done, 2*time.Second); err != nil {
		t.Fatalf("Serve(): %v", err)
	}
}

func TestCommandWordsAreBounded(t *testing.T) {
	tooMany := rt.NewTable()
	for i := 1; i <= MaxArgs+1; i++ {
		tooMany.Set(rt.IntValue(int64(i)), rt.StringValue("x"))
	}
	if _, err := words(tooMany); err == nil || !strings.Contains(err.Error(), "at most 256 words") {
		t.Fatalf("too many command words ended with %v", err)
	}

	tooLarge := rt.NewTable()
	tooLarge.Set(rt.IntValue(1), rt.StringValue(strings.Repeat("x", MaxArgBytes)))
	if _, err := words(tooLarge); err == nil || !strings.Contains(err.Error(), "at most 65536 bytes") {
		t.Fatalf("an oversized command ended with %v", err)
	}

	atLimit := rt.NewTable()
	atLimit.Set(rt.IntValue(1), rt.StringValue(strings.Repeat("x", MaxArgBytes-1)))
	got, err := words(atLimit)
	if err != nil || len(got) != 1 || len(got[0]) != MaxArgBytes-1 {
		t.Fatalf("a command at the byte limit was %d words, %v", len(got), err)
	}
}
