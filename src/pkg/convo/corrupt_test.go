package convo

import (
	"encoding/binary"
	"os"
	"testing"

	"github.com/bresilla/drop/src/pkg/node"
)

// A conversation on the disk is bytes, and bytes go bad: a disk that lied, a write that stopped
// halfway, a file somebody else got at. Whatever is in there, reading it is a report about the
// disk — never a panic, which for the daemon is every conversation gone rather than one.
func TestAConversationThatWentBadIsReadRatherThanFatal(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	s, err := Open(node.ID{})
	if err != nil {
		t.Fatalf("Open(): %v", err)
	}
	if _, err := s.Add(Message{Dir: Out, Kind: KindText, Body: "hello"}); err != nil {
		t.Fatalf("Add(): %v", err)
	}

	good, err := os.ReadFile(s.history)
	if err != nil {
		t.Fatal(err)
	}

	for what, raw := range map[string][]byte{
		"a length longer than a number holds": append(binary.AppendUvarint(nil, 1<<64-1), 1, 2, 3),
		"a length longer than the file":       append(binary.AppendUvarint(nil, 1<<40), 1, 2, 3),
		"a length of nearly everything":       append(binary.AppendUvarint(nil, 1<<63), 1, 2, 3),
		"a half-written record":               good[:len(good)/2],
		"nothing but noise":                   {0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x7f},
		"a good record then rubbish":          append(append([]byte{}, good...), 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x7f),
	} {
		if err := os.WriteFile(s.history, raw, 0o600); err != nil {
			t.Fatal(err)
		}
		// The only requirement is that it comes back at all. Refusing is a fine answer; so is an
		// empty conversation. Taking the process down is not.
		if _, err := s.History(); err != nil {
			t.Logf("%s: refused, which is fine — %v", what, err)
		}
	}
}
