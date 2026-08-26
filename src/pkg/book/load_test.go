package book

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// write puts a peers.json in place, whatever it says.
func write(t *testing.T, body string) {
	t.Helper()

	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

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

// One line nobody can read must not turn every paired peer in the file into a stranger.
//
// It used to: Load gave up on the first bad id, so a book with one mangled entry came back empty
// and the node would not start -- a single bad character costing every pairing on the machine.
func TestOneBadEntryDoesNotLoseTheRest(t *testing.T) {
	id := testID(t)
	secret := base64.StdEncoding.EncodeToString(make([]byte, SecretBytes))

	write(t, fmt.Sprintf(`{
	  "laptop":  {"id": %q, "secret": %q},
	  "mangled": {"id": "not an id at all"},
	  "garbled": {"id": %q, "secret": "not base64 either!!"}
	}`, id, secret, id))

	b, err := Load()
	if err != nil {
		t.Fatalf("Load() with one bad entry: %v", err)
	}

	entry, ok := b.Lookup("laptop")
	if !ok {
		t.Fatal("the good entry went with the bad one")
	}
	if entry.ID != id || !entry.Paired() {
		t.Fatalf("the good entry came back as %+v", entry)
	}
	if _, ok := b.Lookup("mangled"); ok {
		t.Error("an unreadable id was kept")
	}
	if _, ok := b.Lookup("garbled"); ok {
		t.Error("an unreadable secret was kept")
	}
}

// A file that is not JSON at all is still a failure: it is not one bad line, it is no book.
func TestAFileThatIsNotABookIsAnError(t *testing.T) {
	write(t, "this is not json")

	if _, err := Load(); err == nil {
		t.Fatal("rubbish loaded as an address book")
	}
}
