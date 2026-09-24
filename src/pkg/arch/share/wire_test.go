package share

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/bresilla/drop/src/pkg/wire"
)

func TestShareWireDecodersRefuseTrailingBytes(t *testing.T) {
	offerBody := append(offer{ID: testTransferID}.encode(), 0)
	if _, err := decodeOffer(offerBody); err == nil {
		t.Fatal("decodeOffer() accepted trailing bytes")
	}

	resumeBody := append(resume{}.encode(), 0)
	if _, err := decodeResume(resumeBody); err == nil {
		t.Fatal("decodeResume() accepted trailing bytes")
	}
}

func TestOfferDecoderRefusesAnEmptyTransferIdentity(t *testing.T) {
	if _, err := decodeOffer(offer{}.encode()); err == nil {
		t.Fatal("decodeOffer() accepted an empty transfer identity")
	}
}

func TestSeparateTransfersHaveSeparateIdentities(t *testing.T) {
	first, err := NewTransfer(nil)
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewTransfer(nil)
	if err != nil {
		t.Fatal(err)
	}
	if !first.id.valid() || !second.id.valid() || first.id == second.id {
		t.Fatalf("transfer identities = %x and %x", first.id, second.id)
	}
}

func TestOfferDecoderRefusesModeOverflow(t *testing.T) {
	w := wire.NewWriter()
	w.Bytes(testTransferID[:])
	w.Uint(1)
	w.String("item")
	w.Int(0)
	w.Uint(uint64(^uint32(0)) + 1)
	if _, err := decodeOffer(w.Body()); err == nil {
		t.Fatal("decodeOffer() accepted a mode wider than uint32")
	}
}

func TestResumeDecoderRefusesNegativeOffsets(t *testing.T) {
	if _, err := decodeResume(resume{At: []int64{-1}}.encode()); err == nil {
		t.Fatal("decodeResume() accepted a negative offset")
	}
}

func TestSendRefusesAnInvalidResume(t *testing.T) {
	tests := []struct {
		name    string
		sources []Source
		resume  resume
	}{
		{"short answer", []Source{{Name: "one", Size: 1}}, resume{}},
		{"long answer", nil, resume{At: []int64{0}}},
		{"past end", []Source{{Name: "one", Size: 1}}, resume{At: []int64{2}}},
		{"unknown item", []Source{{Name: "pipe", Size: wire.SizeUnknown}}, resume{At: []int64{1}}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var answer bytes.Buffer
			if err := wire.NewConn(readWriter{&answer, &answer}).WriteFrame(wire.KindAccept, test.resume.encode()); err != nil {
				t.Fatalf("writing answer: %v", err)
			}
			var sent bytes.Buffer
			conn := wire.NewConn(readWriter{&answer, &sent})
			if err := Send(conn, test.sources, nil); err == nil {
				t.Fatal("Send() accepted an invalid resume")
			}
		})
	}
}

func TestSendRefusesAnInvalidSourceBeforeWriting(t *testing.T) {
	var sent bytes.Buffer
	conn := wire.NewConn(readWriter{&bytes.Buffer{}, &sent})
	if err := Send(conn, []Source{{Name: "bad", Size: wire.SizeUnknown - 1}}, nil); err == nil {
		t.Fatal("Send() accepted an invalid source size")
	}
	if sent.Len() != 0 {
		t.Fatalf("Send() wrote %d bytes before rejecting its source", sent.Len())
	}
}

func TestSourcesMustRemainRegularFiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "source")
	if err := os.WriteFile(path, []byte("body"), 0o600); err != nil {
		t.Fatal(err)
	}
	src, err := FileFromPath(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := unix.Mkfifo(path, 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := FileFromPath(path); err == nil {
		t.Fatal("FileFromPath() accepted a pipe")
	}
	var sent bytes.Buffer
	conn := wire.NewConn(readWriter{&bytes.Buffer{}, &sent})
	if err := sendOne(conn, src, 0, nil); err == nil {
		t.Fatal("sendOne() opened a path that became a pipe")
	}
	if sent.Len() != 0 {
		t.Fatalf("sendOne() wrote %d bytes from a pipe", sent.Len())
	}
}

func TestASelectedSourceCannotBeReplacedBeforeSending(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "source")
	if err := os.WriteFile(path, []byte("first"), 0o600); err != nil {
		t.Fatal(err)
	}
	src, err := FileFromPath(path)
	if err != nil {
		t.Fatal(err)
	}
	replacement := filepath.Join(dir, "replacement")
	if err := os.WriteFile(replacement, []byte("other"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(replacement, path); err != nil {
		t.Fatal(err)
	}

	var sent bytes.Buffer
	err = sendOne(wire.NewConn(readWriter{&bytes.Buffer{}, &sent}), src, 0, nil)
	if err == nil || !strings.Contains(err.Error(), "changed before") {
		t.Fatalf("a replaced source returned %v", err)
	}
	if sent.Len() != 0 {
		t.Fatalf("a replaced source sent %d bytes", sent.Len())
	}
}

func TestAFileChangingWhileSentIsNotConfirmed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "source")
	body := bytes.Repeat([]byte("a"), wire.DataChunk*2)
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	src, err := FileFromPath(path)
	if err != nil {
		t.Fatal(err)
	}

	var answer bytes.Buffer
	if err := wire.NewConn(readWriter{&answer, &answer}).WriteFrame(wire.KindAck, wire.Ack{OK: true}.Encode()); err != nil {
		t.Fatal(err)
	}
	var sent bytes.Buffer
	var once sync.Once
	var changeErr error
	progress := func(string, int64, int64) {
		once.Do(func() {
			file, err := os.OpenFile(path, os.O_WRONLY, 0)
			if err == nil {
				_, err = file.WriteAt([]byte("x"), 0)
			}
			if file != nil {
				if closeErr := file.Close(); err == nil {
					err = closeErr
				}
			}
			if err == nil {
				err = os.Chtimes(path, time.Now().Add(time.Hour), time.Now().Add(time.Hour))
			}
			changeErr = err
		})
	}
	err = sendOne(wire.NewConn(readWriter{&answer, &sent}), src, 0, progress)
	if changeErr != nil {
		t.Fatal(changeErr)
	}
	if err == nil || !strings.Contains(err.Error(), "changed while") {
		t.Fatalf("a changing source returned %v", err)
	}
}

type emptyReader struct{}

func (emptyReader) Read([]byte) (int, error) { return 0, nil }

func TestASourceThatMakesNoProgressIsStopped(t *testing.T) {
	tests := []struct {
		name string
		src  Source
		at   int64
	}{
		{"body", FileFromReader("stuck", emptyReader{}), 0},
		{"resumed prefix", Source{Name: "stuck", Size: 2, Reader: emptyReader{}}, 1},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var sent bytes.Buffer
			err := sendOne(wire.NewConn(readWriter{&bytes.Buffer{}, &sent}), test.src, test.at, nil)
			if !errors.Is(err, io.ErrNoProgress) {
				t.Fatalf("a stuck source returned %v", err)
			}
		})
	}
}
