package term

import (
	"strings"
	"testing"
	"time"
)

// A watched terminal is somebody else's program writing bytes at this one. Time spent parsing them
// is time this process is not doing anything else, so a short input that costs a long time is a way
// to hold a machine down from the far end of a terminal it was invited to watch.
func TestNoSequenceCostsMoreThanItsLength(t *testing.T) {
	nasty := map[string]string{
		"a huge cursor move":     "\x1b[999999999;999999999H",
		"a huge column move":     strings.Repeat("\x1b[99999999C", 200),
		"a huge delete":          strings.Repeat("\x1b[99999999P", 200),
		"a huge insert":          strings.Repeat("\x1b[99999999@", 200),
		"a huge erase":           strings.Repeat("\x1b[99999999X", 200),
		"a huge scroll up":       strings.Repeat("\x1b[99999999S", 200),
		"a huge scroll down":     strings.Repeat("\x1b[99999999T", 200),
		"huge insert lines":      strings.Repeat("\x1b[99999999L", 200),
		"huge delete lines":      strings.Repeat("\x1b[99999999M", 200),
		"a huge repeat":          strings.Repeat("x\x1b[99999999b", 200),
		"many parameters":        "\x1b[" + strings.Repeat("1;", 20000) + "1m",
		"a scroll region abused": "\x1b[1;99999999r" + strings.Repeat("\x1b[99999999M", 200),
		"tab stops":              strings.Repeat("\x1bH", 5000) + strings.Repeat("\t", 5000),
		"an unterminated osc":    "\x1b]0;" + strings.Repeat("a", 200000),
		"an unterminated dcs":    "\x1bP" + strings.Repeat("a", 200000),
		"a long parameter run":   "\x1b[" + strings.Repeat("9", 100000) + "H",
	}

	for what, out := range nasty {
		s := New(200, 60)
		start := time.Now()
		s.Write([]byte(out))
		took := time.Since(start)

		if took > 250*time.Millisecond {
			t.Errorf("%s: %d bytes took %v", what, len(out), took)
		} else {
			t.Logf("%-24s %7d bytes  %v", what, len(out), took)
		}
	}
}
