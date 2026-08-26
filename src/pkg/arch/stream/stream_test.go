package stream

import (
	"strings"
	"testing"
)

// A command's output comes through a pipe, where nothing translates line endings. Drawn on a
// terminal screen at the far end, a bare line feed moves down without moving back, and every line
// starts further right than the one before it.
func TestAStreamsNewlinesBecomeTerminalOnes(t *testing.T) {
	var got strings.Builder

	if _, err := (asTerminal{&got}).Write([]byte("one\ntwo\n")); err != nil {
		t.Fatalf("Write(): %v", err)
	}
	if got.String() != "one\r\ntwo\r\n" {
		t.Errorf("wrote %q", got.String())
	}
}

// A command that already writes CRLF must not end up with two carriage returns.
func TestAlreadyTerminalNewlinesAreLeftAlone(t *testing.T) {
	var got strings.Builder

	if _, err := (asTerminal{&got}).Write([]byte("one\r\ntwo\r\n")); err != nil {
		t.Fatalf("Write(): %v", err)
	}
	if got.String() != "one\r\ntwo\r\n" {
		t.Errorf("wrote %q", got.String())
	}
}

// The count returned is what the caller handed over, not what was written: io.Copy treats a short
// write as an error, and every translated newline makes the write longer than the read.
func TestTheTranslatorReportsWhatItWasGiven(t *testing.T) {
	var got strings.Builder

	n, err := (asTerminal{&got}).Write([]byte("one\ntwo\n"))
	if err != nil {
		t.Fatalf("Write(): %v", err)
	}
	if n != len("one\ntwo\n") {
		t.Errorf("reported %d bytes, was given %d", n, len("one\ntwo\n"))
	}
}
