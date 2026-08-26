package grant

import (
	"os"
	"path/filepath"
	"testing"
)

// put writes a grants file, whatever it says.
func put(t *testing.T, body string) {
	t.Helper()

	file, err := path()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// A path that will not clean must not take some of the refusals down with it.
//
// The rule set used to be built in place, entry by entry, and the loop gave up on the first bad
// path -- leaving whichever refusals happened to come after it missing, with nothing said. The map
// ranges in no order, so which ones vanished changed from run to run.
func TestABadPathLeavesTheRefusalsAlone(t *testing.T) {
	s := empty(t)

	if err := s.Deny("/work", "bob"); err != nil {
		t.Fatal(err)
	}
	if err := s.Deny("/work/notes", "carol"); err != nil {
		t.Fatal(err)
	}

	put(t, `{
	  "/work":       {"deny": ["bob"]},
	  "/work/notes": {"deny": ["carol"]},
	  "/WORK":       {"deny": ["dave"]}
	}`)

	_, deny := s.For("/work/notes")
	if !holds(deny, "bob") || !holds(deny, "carol") {
		t.Fatalf("a bad path took refusals with it: %v", deny)
	}
}

// A grants file nobody can read fails closed: whatever it might have allowed is not handed out,
// and the refusals that did load stay in force. A revocation must not lapse because somebody left
// a comma in the wrong place.
func TestAGrantsFileThatWillNotLoadStopsAllowing(t *testing.T) {
	s := empty(t)

	if err := s.Allow("/work", "carol"); err != nil {
		t.Fatal(err)
	}
	if err := s.Deny("/work", "bob"); err != nil {
		t.Fatal(err)
	}
	if allow, _ := s.For("/work"); len(allow) != 1 {
		t.Fatalf("carol was not allowed to start with: %v", allow)
	}

	// Written long enough to change the size as well as the time, so the change is noticed.
	put(t, "{ this is not json at all, not even nearly, no }")

	allow, deny := s.For("/work")
	if len(allow) != 0 {
		t.Errorf("a file nobody can read still allowed %v", allow)
	}
	if !holds(deny, "bob") {
		t.Errorf("a refusal lapsed when the file stopped loading: %v", deny)
	}
}

func holds(list []string, who string) bool {
	for _, at := range list {
		if at == who {
			return true
		}
	}
	return false
}
