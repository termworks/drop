package share

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
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
	if err := wire.NewConn(readWriter{&input, &input}).WriteFrame(wire.KindItem, offer{ID: testTransferID, Items: items}.encode()); err != nil {
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

func TestLostAckRetryDoesNotLandAnItemTwice(t *testing.T) {
	for _, test := range []struct {
		name   string
		source func() Source
	}{
		{
			name: "known file",
			source: func() Source {
				path := filepath.Join(t.TempDir(), "report.txt")
				if err := os.WriteFile(path, []byte("whole"), 0o600); err != nil {
					t.Fatal(err)
				}
				src, err := FileFromPath(path)
				if err != nil {
					t.Fatal(err)
				}
				return src
			},
		},
		{
			name:   "unknown reader",
			source: func() Source { return FileFromReader("report.txt", bytes.NewReader([]byte("whole"))) },
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			landed, completed := 0, 0
			share := New(Into{
				Landed:    func(node.ID, string, int64) { landed++ },
				Completed: func(node.ID, string, Config) { completed++ },
			})
			value, err := share.Read(one{"dir": dir})
			if err != nil {
				t.Fatal(err)
			}
			config := value.(Config)
			transfer, err := NewTransfer([]Source{test.source()})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = transfer.Close() })

			first, err := scriptedTransfer(transfer, resume{At: []int64{0}, Done: []bool{false}}, false)
			if !Unconfirmed(err) {
				t.Fatalf("lost ack returned %v", err)
			}
			if err := serveTransfer(share, config, first, &failAfterWrites{left: 2}); err == nil {
				t.Fatal("receiver reported a successfully written ack")
			}
			if landed != 0 || completed != 0 {
				t.Fatalf("failed ack landed %d and completed %d times", landed, completed)
			}

			retry, err := scriptedTransfer(transfer, resume{At: []int64{5}, Done: []bool{true}}, true)
			if err != nil {
				t.Fatal(err)
			}
			var retryAnswer bytes.Buffer
			if err := serveTransfer(share, config, retry, &retryAnswer); err != nil {
				t.Fatal(err)
			}
			picked, err := decodeResume(answered(t, &retryAnswer))
			if err != nil || len(picked.Done) != 1 || !picked.Done[0] || picked.At[0] != 5 {
				t.Fatalf("retry answer = %+v (%v)", picked, err)
			}
			if landed != 1 || completed != 1 {
				t.Fatalf("retry landed %d and completed %d times", landed, completed)
			}

			separate, err := NewTransfer([]Source{test.source()})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = separate.Close() })
			second, err := scriptedTransfer(separate, resume{At: []int64{0}, Done: []bool{false}}, true)
			if err != nil {
				t.Fatal(err)
			}
			if err := serveTransfer(share, config, second, &bytes.Buffer{}); err != nil {
				t.Fatal(err)
			}
			if landed != 2 || completed != 2 {
				t.Fatalf("separate send landed %d and completed %d times", landed, completed)
			}
			for _, name := range []string{"report.txt", "report-1.txt"} {
				if got, err := os.ReadFile(filepath.Join(dir, name)); err != nil || string(got) != "whole" {
					t.Fatalf("%s = %q (%v)", name, got, err)
				}
			}
		})
	}
}

func TestLostMiddleAckResumesTheWholeBatch(t *testing.T) {
	dir := t.TempDir()
	landed, completed := 0, 0
	share := New(Into{
		Landed:    func(node.ID, string, int64) { landed++ },
		Completed: func(node.ID, string, Config) { completed++ },
	})
	value, err := share.Read(one{"dir": dir})
	if err != nil {
		t.Fatal(err)
	}
	config := value.(Config)
	transfer, err := NewTransfer([]Source{
		FileFromReader("one", bytes.NewReader([]byte("1"))),
		FileFromReader("two", bytes.NewReader([]byte("2"))),
		FileFromReader("three", bytes.NewReader([]byte("3"))),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = transfer.Close() })

	first, err := scriptedTransferAcks(transfer, resume{
		At:   []int64{0, 0, 0},
		Done: []bool{false, false, false},
	}, 1)
	if !Unconfirmed(err) {
		t.Fatalf("lost middle ack returned %v", err)
	}
	if err := serveTransfer(share, config, first, &failAfterWrites{left: 4}); err == nil {
		t.Fatal("receiver reported a successfully written middle ack")
	}

	retry, err := scriptedTransferAcks(transfer, resume{
		At:   []int64{1, 1, 0},
		Done: []bool{true, true, false},
	}, 3)
	if err != nil {
		t.Fatal(err)
	}
	if err := serveTransfer(share, config, retry, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if landed != 3 || completed != 1 {
		t.Fatalf("batch landed %d and completed %d times", landed, completed)
	}
	for name, want := range map[string]string{"one": "1", "two": "2", "three": "3"} {
		if got, err := os.ReadFile(filepath.Join(dir, name)); err != nil || string(got) != want {
			t.Fatalf("%s = %q (%v)", name, got, err)
		}
	}
}

func scriptedTransfer(transfer *Transfer, picked resume, acknowledge bool) ([]byte, error) {
	acks := 0
	if acknowledge {
		acks = len(picked.At)
	}
	return scriptedTransferAcks(transfer, picked, acks)
}

func scriptedTransferAcks(transfer *Transfer, picked resume, acks int) ([]byte, error) {
	var replies bytes.Buffer
	answer := wire.NewConn(readWriter{&replies, &replies})
	if err := answer.WriteFrame(wire.KindAccept, picked.encode()); err != nil {
		return nil, err
	}
	for range acks {
		if err := answer.WriteFrame(wire.KindAck, wire.Ack{OK: true}.Encode()); err != nil {
			return nil, err
		}
	}
	var sent bytes.Buffer
	err := transfer.Send(wire.NewConn(readWriter{&replies, &sent}), nil)
	return sent.Bytes(), err
}

func serveTransfer(share *Share, config Config, sent []byte, out io.Writer) error {
	return share.Serve(context.Background(), arch.Session{
		Path: "/share", Config: config, From: idFor(9), Conn: wire.NewConn(readWriter{bytes.NewReader(sent), out}),
	})
}

// one is a declaration of a handful of settings.
type one map[string]string

func (o one) String(key string) (string, bool) { v, ok := o[key]; return v, ok }
func (o one) Bool(string) (bool, bool)         { return false, false }
func (o one) Strings(string) ([]string, bool)  { return nil, false }
