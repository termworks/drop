package made

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/bresilla/drop/src/pkg/ns"
)

// here points the store at a directory of this test's own, and hands back the file it writes.
func here(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	return filepath.Join(dir, "drop", File)
}

func write(t *testing.T, body string) {
	t.Helper()

	file := here(t)
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestANamespaceSurvivesBeingWrittenAndReadBack(t *testing.T) {
	here(t)

	s, err := Load()
	if err != nil {
		t.Fatalf("Load(): %v", err)
	}

	want := Entry{
		Archetype: "files",
		Version:   2,
		Settings:  Settings{"dir": "~/notes", "writable": true, "hide": []string{"a", "b"}},
		Access:    Access{Paired: true, Visible: []string{"carol"}},
	}
	if err := s.Add("notes/", want); err != nil {
		t.Fatalf("Add(): %v", err)
	}

	back, err := Load()
	if err != nil {
		t.Fatalf("Load(): %v", err)
	}
	got, ok := back.Get("/notes")
	if !ok {
		t.Fatalf("/notes came back as %v", back.Paths())
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("read back %#v", got)
	}
}

// A number has no accessor to come out of, so it is refused where it is written rather than read as
// text and quietly meaning something else somewhere.
func TestANumberInSettingsIsRefusedAndSaysWhere(t *testing.T) {
	write(t, `{"/film": {"type": "camera", "settings": {"device": "/dev/video0", "frames": 30}}}`)

	_, err := Load()
	if err == nil {
		t.Fatal("a number loaded")
	}
	if !strings.Contains(err.Error(), "/film") || !strings.Contains(err.Error(), "frames") {
		t.Errorf("the refusal does not name the path and the key: %v", err)
	}
	if !strings.Contains(err.Error(), "a number") {
		t.Errorf("the refusal does not say what was found: %v", err)
	}
}

func TestANumberInsideAListIsRefusedToo(t *testing.T) {
	write(t, `{"/film": {"type": "camera", "settings": {"sizes": ["big", 3]}}}`)

	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "sizes") {
		t.Fatalf("Load() = %v", err)
	}
}

func TestALoadedListIsAListOfNames(t *testing.T) {
	write(t, `{"/notes": {"type": "files", "settings": {"hide": ["a", "b"]}}}`)

	s, err := Load()
	if err != nil {
		t.Fatalf("Load(): %v", err)
	}
	entry, _ := s.Get("/notes")
	got, ok := Declared(entry.Settings).Strings("hide")
	if !ok || !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Errorf("hide came back %v %v", got, ok)
	}
}

// Everything an access rule can say has to survive being written down, or a namespace comes back
// open to somebody it was not open to before.
func TestAnAccessRuleRoundTripsWithoutLoss(t *testing.T) {
	want := ns.Access{
		AnyPaired:      true,
		AnyTrusted:     true,
		Anyone:         true,
		Named:          []string{"bob", "carol@laptop"},
		Keys:           []string{"aaaa"},
		Password:       "$argon2id$v=19$m=65536,t=3,p=4$c2FsdA$c3Vt",
		All:            true,
		Visible:        []string{"dave"},
		AnyVisible:     true,
		TrustedVisible: true,
	}

	if got := Ruled(want).Rule(); !reflect.DeepEqual(got, want) {
		t.Errorf("came back %#v", got)
	}
}

// A refusal is what revoking leaves behind. It lives in the grants, and this must not become a
// second place to write one.
func TestARefusalIsNotWrittenDown(t *testing.T) {
	rule := ns.Access{AnyPaired: true, Refused: []string{"bob"}}

	if got := Ruled(rule).Rule(); len(got.Refused) != 0 {
		t.Errorf("a refusal was written down: %v", got.Refused)
	}
}

func TestRemovingSaysWhetherItWasThere(t *testing.T) {
	here(t)

	s, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Add("/notes", Entry{Archetype: "chat", Access: Access{Paired: true}}); err != nil {
		t.Fatal(err)
	}

	had, err := s.Remove("/notes")
	if err != nil || !had {
		t.Fatalf("Remove() = %v, %v", had, err)
	}
	if had, _ := s.Remove("/notes"); had {
		t.Error("removing it twice said it was there twice")
	}
}

// Two commands putting a namespace up at once must not lose each other's work. `drop path create`
// and `drop path rm` are separate processes sharing one file, so this is the ordinary case rather
// than a rare one.
func TestTwoCommandsPuttingNamespacesUpDoNotLoseEachOther(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	// Each store is a process of its own, each holding the file as it was when it started.
	var stores []*Store
	for range 4 {
		s, err := Load()
		if err != nil {
			t.Fatal(err)
		}
		stores = append(stores, s)
	}

	var wg sync.WaitGroup
	for i, s := range stores {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = s.Add(fmt.Sprintf("/ns%d", i), Entry{Archetype: "chat"})
		}()
	}
	wg.Wait()

	after, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	for i := range stores {
		if _, held := after.Get(fmt.Sprintf("/ns%d", i)); !held {
			t.Errorf("/ns%d was lost by another command writing at the same time", i)
		}
	}
	if n := after.Len(); n != len(stores) {
		t.Fatalf("%d namespaces were put up and %d survive", len(stores), n)
	}
}
