package stream

import "testing"

// no is a declaration that says nothing at all.
type no struct{}

func (no) String(string) (string, bool)    { return "", false }
func (no) Bool(string) (bool, bool)        { return false, false }
func (no) Strings(string) ([]string, bool) { return nil, false }

// A stream with nothing to run is a mount that can only ever answer with silence.
func TestAStreamNeedsACommand(t *testing.T) {
	if _, err := (&Stream{}).Read(no{}); err == nil {
		t.Fatal("Read() accepted a stream with no command")
	}
}
