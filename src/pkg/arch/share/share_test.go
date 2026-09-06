package share

import (
	"bytes"
	"context"
	"io"
	"testing"

	"github.com/bresilla/drop/src/pkg/arch"
	"github.com/bresilla/drop/src/pkg/node"
	"github.com/bresilla/drop/src/pkg/wire"
)

// no is a declaration that says nothing at all.
type no struct{}

func (no) String(string) (string, bool)    { return "", false }
func (no) Bool(string) (bool, bool)        { return false, false }
func (no) Strings(string) ([]string, bool) { return nil, false }

// A share with nowhere to put anything cannot work, and saying so at load time is the difference
// between a config error and silence months later.
func TestAShareNeedsADir(t *testing.T) {
	if _, err := (&Share{}).Read(no{}); err == nil {
		t.Fatal("Read() accepted a share with no dir")
	}
}

func TestAShareReadsItsDir(t *testing.T) {
	got, err := (&Share{}).Read(one{"dir": "/tmp/in"})
	if err != nil {
		t.Fatalf("Read(): %v", err)
	}
	cfg, ok := got.(Config)
	if !ok || cfg.Dir != "/tmp/in" {
		t.Fatalf("Read() = %+v", got)
	}
}

func TestEachShareConfigHasItsOwnIdentity(t *testing.T) {
	share := &Share{}
	firstValue, err := share.Read(one{"dir": "/tmp/in"})
	if err != nil {
		t.Fatal(err)
	}
	secondValue, err := share.Read(one{"dir": "/tmp/in"})
	if err != nil {
		t.Fatal(err)
	}
	first, firstOK := firstValue.(Config)
	second, secondOK := secondValue.(Config)
	if !firstOK || !secondOK || first.instance == nil || second.instance == nil || first == second {
		t.Fatalf("separate reads produced %+v and %+v", firstValue, secondValue)
	}
	var copied arch.Config = first
	if copied.(Config).instance != first.instance {
		t.Fatal("copying a config changed its identity")
	}
}

type failAfterWrites struct {
	left int
	out  bytes.Buffer
}

func (w *failAfterWrites) Write(body []byte) (int, error) {
	if w.left == 0 {
		return 0, io.ErrClosedPipe
	}
	w.left--
	return w.out.Write(body)
}

func serveBatch(t *testing.T, share *Share, config Config, path string, items []Item, sent *bytes.Buffer, out io.Writer) error {
	t.Helper()

	var input bytes.Buffer
	if err := wire.NewConn(readWriter{&input, &input}).WriteFrame(wire.KindItem, offer{Items: items}.encode()); err != nil {
		t.Fatal(err)
	}
	input.Write(sent.Bytes())
	return share.Serve(context.Background(), arch.Session{
		Path: path, Config: config, From: idFor(9), Conn: wire.NewConn(readWriter{&input, out}),
	})
}

func TestShareCompletesOnlyWholeNonEmptyBatches(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		completed := 0
		share := New(Into{Completed: func(node.ID, string, Config) { completed++ }})
		value, err := share.Read(one{"dir": t.TempDir()})
		if err != nil {
			t.Fatal(err)
		}
		if err := serveBatch(t, share, value.(Config), "/share", nil, spoken(t), &bytes.Buffer{}); err != nil {
			t.Fatal(err)
		}
		if completed != 0 {
			t.Fatalf("empty batch completed %d times", completed)
		}
	})

	t.Run("partial", func(t *testing.T) {
		landed, completed := 0, 0
		share := New(Into{
			Landed:    func(node.ID, string, int64) { landed++ },
			Completed: func(node.ID, string, Config) { completed++ },
		})
		value, err := share.Read(one{"dir": t.TempDir()})
		if err != nil {
			t.Fatal(err)
		}
		items := []Item{{Name: "one", Size: 3}, {Name: "two", Size: 3}}
		sent := spoken(t, spoke{sent: []byte("one"), whole: []byte("one")}, spoke{sent: []byte("bad"), whole: []byte("two")})
		if err := serveBatch(t, share, value.(Config), "/share", items, sent, &bytes.Buffer{}); err == nil {
			t.Fatal("partial batch returned no error")
		}
		if landed != 1 || completed != 0 {
			t.Fatalf("partial batch landed %d and completed %d times", landed, completed)
		}
	})

	t.Run("success", func(t *testing.T) {
		var completed []struct {
			from   node.ID
			path   string
			config Config
		}
		share := New(Into{Completed: func(from node.ID, path string, config Config) {
			completed = append(completed, struct {
				from   node.ID
				path   string
				config Config
			}{from, path, config})
		}})
		value, err := share.Read(one{"dir": t.TempDir()})
		if err != nil {
			t.Fatal(err)
		}
		config := value.(Config)
		body := []byte("whole")
		item := Item{Name: "one", Size: int64(len(body))}
		if err := serveBatch(t, share, config, "/share/subpath", []Item{item}, spoken(t, spoke{sent: body, whole: body}), &bytes.Buffer{}); err != nil {
			t.Fatal(err)
		}
		if len(completed) != 1 || completed[0].from != idFor(9) || completed[0].path != "/share/subpath" || completed[0].config != config {
			t.Fatalf("completion = %+v", completed)
		}
	})
}

func TestShareDoesNotCompleteWhenTheFinalAckFails(t *testing.T) {
	landed, completed := 0, 0
	share := New(Into{
		Landed:    func(node.ID, string, int64) { landed++ },
		Completed: func(node.ID, string, Config) { completed++ },
	})
	dir := t.TempDir()
	value, err := share.Read(one{"dir": dir})
	if err != nil {
		t.Fatal(err)
	}
	body := []byte("whole")
	item := Item{Name: "one", Size: int64(len(body))}
	out := &failAfterWrites{left: 2}
	if err := serveBatch(t, share, value.(Config), "/share", []Item{item}, spoken(t, spoke{sent: body, whole: body}), out); err == nil {
		t.Fatal("failed final ack returned no error")
	}
	if landed != 0 || completed != 0 {
		t.Fatalf("failed final ack landed %d and completed %d times", landed, completed)
	}
}

// one is a declaration of a handful of settings.
type one map[string]string

func (o one) String(key string) (string, bool) { v, ok := o[key]; return v, ok }
func (o one) Bool(string) (bool, bool)         { return false, false }
func (o one) Strings(string) ([]string, bool)  { return nil, false }
