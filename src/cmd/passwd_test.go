package cmd

import (
	"io"
	"strings"
	"testing"

	"github.com/bresilla/drop/src/pkg/passwd"
)

func TestPipedPasswordsAreBounded(t *testing.T) {
	tooLong := strings.NewReader(strings.Repeat("x", passwd.MaxPlain+1) + "\n")
	if _, err := readPasswordLine(tooLong); err == nil {
		t.Fatal("an oversized piped password was accepted")
	}
}

func TestPipedPasswordsMayEndWithoutANewline(t *testing.T) {
	plain, err := readPasswordLine(strings.NewReader("secret"))
	if err != nil || plain != "secret" {
		t.Fatalf("readPasswordLine() = %q, %v", plain, err)
	}
}

func TestPipedPasswordReadFailuresAreReported(t *testing.T) {
	plain, err := readPasswordLine(io.MultiReader(strings.NewReader("partial"), brokenReader{}))
	if err == nil || plain != "" {
		t.Fatalf("readPasswordLine() = %q, %v", plain, err)
	}
}
