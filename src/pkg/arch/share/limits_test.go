package share

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bresilla/drop/src/pkg/arch"
	"github.com/bresilla/drop/src/pkg/node"
	"github.com/bresilla/drop/src/pkg/wire"
)

type limitDeclaration struct {
	text      map[string]string
	mentioned map[string]bool
}

func (d limitDeclaration) String(key string) (string, bool) {
	value, ok := d.text[key]
	return value, ok
}

func (d limitDeclaration) Bool(key string) (bool, bool) {
	return false, d.mentioned[key]
}

func (limitDeclaration) Strings(string) ([]string, bool) { return nil, false }

func TestShareTransferLimitsHaveProductionDefaults(t *testing.T) {
	quota := quotaFor(Config{})
	if quota.item != defaultMaxItemBytes || quota.session != defaultMaxSessionBytes {
		t.Fatalf("quotaFor(Config{}) = %+v", quota)
	}
}

func TestShareReadsTransferLimits(t *testing.T) {
	value, err := (&Share{}).Read(limitDeclaration{text: map[string]string{
		"dir": "/x", "max_item": "2 MiB", "max_session": "3 GiB",
	}})
	if err != nil {
		t.Fatal(err)
	}
	config := value.(Config)
	if config.Dir != "/x" || config.MaxItemBytes != 2<<20 || config.MaxSessionBytes != 3<<30 || config.instance == nil {
		t.Fatalf("Read() = %+v", config)
	}
}

func TestShareRefusesInvalidTransferLimits(t *testing.T) {
	for _, declaration := range []limitDeclaration{
		{text: map[string]string{"dir": "/x", "max_item": "none"}},
		{text: map[string]string{"dir": "/x", "max_item": "2 GiB", "max_session": "1 GiB"}},
		{text: map[string]string{"dir": "/x"}, mentioned: map[string]bool{"max_item": true}},
	} {
		if _, err := (&Share{}).Read(declaration); err == nil {
			t.Fatalf("Read(%+v) accepted invalid limits", declaration)
		}
	}
}

func TestKnownShareLimitsAreRefusedBeforeData(t *testing.T) {
	for _, test := range []struct {
		name    string
		items   []Item
		reason  string
		item    int64
		session int64
	}{
		{"item", []Item{{Name: "large", Size: 5}}, "item limit", 4, 8},
		{"session", []Item{{Name: "one", Size: 3}, {Name: "two", Size: 3}}, "session limit", 4, 5},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			config := Config{
				Dir: dir, MaxItemBytes: test.item, MaxSessionBytes: test.session, instance: newConfigInstance(),
			}
			var out bytes.Buffer
			err := serveBatch(t, New(Into{}), config, "/share", test.items, spoken(t), &out)
			if err == nil || !strings.Contains(err.Error(), test.reason) {
				t.Fatalf("oversized offer returned %v", err)
			}
			kind, body, err := wire.NewConn(readWriter{&out, &bytes.Buffer{}}).ReadFrame()
			if err != nil || kind != wire.KindReject {
				t.Fatalf("refusal frame = %d, %v", kind, err)
			}
			reject, err := wire.DecodeReject(body)
			if err != nil || !strings.Contains(reject.Reason, test.reason) {
				t.Fatalf("refusal = %+v, %v", reject, err)
			}
			if entries, err := os.ReadDir(dir); err != nil || len(entries) != 0 {
				t.Fatalf("refused offer left %d entries, %v", len(entries), err)
			}
		})
	}
}

func TestUnknownShareItemStopsAtItsLimit(t *testing.T) {
	dir := t.TempDir()
	item := Item{Name: "stream", Size: wire.SizeUnknown}
	config := Config{Dir: dir, MaxItemBytes: 4, MaxSessionBytes: 8, instance: newConfigInstance()}
	var out bytes.Buffer
	err := serveBatch(t, New(Into{}), config, "/share", []Item{item}, spoken(t, spoke{
		sent: []byte("12345"), whole: []byte("12345"),
	}), &out)
	if err == nil || !strings.Contains(err.Error(), "item limit") {
		t.Fatalf("unknown-size overflow returned %v", err)
	}
	answer := wire.NewConn(readWriter{&out, &bytes.Buffer{}})
	if kind, _, err := answer.ReadFrame(); err != nil || kind != wire.KindAccept {
		t.Fatalf("offer answer = %d, %v", kind, err)
	}
	kind, body, err := answer.ReadFrame()
	if err != nil || kind != wire.KindAck {
		t.Fatalf("overflow answer = %d, %v", kind, err)
	}
	ack, err := wire.DecodeAck(body)
	if err != nil || ack.OK || !strings.Contains(ack.Reason, "item limit") {
		t.Fatalf("overflow ack = %+v, %v", ack, err)
	}
	if entries, err := os.ReadDir(dir); err != nil || len(entries) != 0 {
		t.Fatalf("overflow left %d entries, %v", len(entries), err)
	}
}

func TestShareSessionLimitCoversAWholeUnknownBatch(t *testing.T) {
	dir := t.TempDir()
	landed, completed := 0, 0
	share := New(Into{
		Landed:    func(_ node.ID, _ string, _ int64) { landed++ },
		Completed: func(_ node.ID, _ string, _ Config) { completed++ },
	})
	config := Config{Dir: dir, MaxItemBytes: 4, MaxSessionBytes: 5, instance: newConfigInstance()}
	items := []Item{{Name: "one", Size: wire.SizeUnknown}, {Name: "two", Size: wire.SizeUnknown}}
	sent := spoken(t,
		spoke{sent: []byte("123"), whole: []byte("123")},
		spoke{sent: []byte("456"), whole: []byte("456")},
	)
	err := serveBatch(t, share, config, "/share", items, sent, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "session limit") {
		t.Fatalf("batch overflow returned %v", err)
	}
	if landed != 1 || completed != 0 {
		t.Fatalf("batch landed %d and completed %d times", landed, completed)
	}
	if got := read(t, filepath.Join(dir, "one")); string(got) != "123" {
		t.Fatalf("first item = %q", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "two")); !os.IsNotExist(err) {
		t.Fatalf("second item exists: %v", err)
	}
}

func TestShareSessionLimitCountsAResumedPartial(t *testing.T) {
	dir := t.TempDir()
	items := []Item{
		{Name: "one", Size: wire.SizeUnknown},
		{Name: "two", Size: wire.SizeUnknown},
	}
	parts := []string{
		partName(idFor(9), testTransferID, items[0]),
		partName(idFor(9), testTransferID, items[1]),
	}
	if err := os.WriteFile(filepath.Join(dir, parts[0]), []byte("123"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, parts[1]), []byte("45"), 0o600); err != nil {
		t.Fatal(err)
	}
	config := Config{Dir: dir, MaxItemBytes: 6, MaxSessionBytes: 6, instance: newConfigInstance()}
	var out bytes.Buffer
	err := serveBatch(t, New(Into{}), config, "/share", items, spoken(t, spoke{
		sent: []byte("67"), whole: []byte("12367"),
	}), &out)
	if err == nil || !strings.Contains(err.Error(), "session limit") {
		t.Fatalf("resumed overflow returned %v", err)
	}
	picked, err := decodeResume(answered(t, &out))
	if err != nil || picked.At[0] != 3 || picked.At[1] != 2 {
		t.Fatalf("resume = %+v, %v", picked, err)
	}
	if _, err := os.Stat(filepath.Join(dir, parts[0])); !os.IsNotExist(err) {
		t.Fatalf("overflow part exists: %v", err)
	}
	if got := read(t, filepath.Join(dir, parts[1])); string(got) != "45" {
		t.Fatalf("untouched part = %q", got)
	}
}

func TestShareLimitsIncludeTheirExactBoundary(t *testing.T) {
	dir := t.TempDir()
	item := Item{Name: "stream", Size: wire.SizeUnknown}
	part := partName(idFor(9), testTransferID, item)
	if err := os.WriteFile(filepath.Join(dir, part), []byte("12"), 0o600); err != nil {
		t.Fatal(err)
	}
	config := Config{Dir: dir, MaxItemBytes: 4, MaxSessionBytes: 4, instance: newConfigInstance()}
	err := serveBatch(t, New(Into{}), config, "/share", []Item{item}, spoken(t, spoke{
		sent: []byte("34"), whole: []byte("1234"),
	}), &bytes.Buffer{})
	if err != nil {
		t.Fatalf("exact limit returned %v", err)
	}
	if got := read(t, filepath.Join(dir, "stream")); string(got) != "1234" {
		t.Fatalf("landed = %q", got)
	}
}

func TestShareServeRefusesInvalidRuntimeLimits(t *testing.T) {
	var out bytes.Buffer
	err := New(Into{}).Serve(context.Background(), arch.Session{
		Config: Config{Dir: t.TempDir(), MaxItemBytes: -1},
		Conn:   wire.NewConn(readWriter{&bytes.Buffer{}, &out}),
	})
	if err != nil {
		t.Fatal(err)
	}
	kind, _, err := wire.NewConn(readWriter{&out, &bytes.Buffer{}}).ReadFrame()
	if err != nil || kind != wire.KindReject {
		t.Fatalf("runtime limit answer = %d, %v", kind, err)
	}
}
