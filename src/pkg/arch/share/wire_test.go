package share

import (
	"bytes"
	"testing"

	"github.com/bresilla/drop/src/pkg/wire"
)

func TestShareWireDecodersRefuseTrailingBytes(t *testing.T) {
	offerBody := append(offer{}.encode(), 0)
	if _, err := decodeOffer(offerBody); err == nil {
		t.Fatal("decodeOffer() accepted trailing bytes")
	}

	resumeBody := append(resume{}.encode(), 0)
	if _, err := decodeResume(resumeBody); err == nil {
		t.Fatal("decodeResume() accepted trailing bytes")
	}
}

func TestOfferDecoderRefusesModeOverflow(t *testing.T) {
	w := wire.NewWriter()
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
