package arch

import (
	"context"
	"strings"
	"testing"
)

// fake is an archetype that exists only to be looked up.
type fake struct {
	name    string
	version int
}

func (f fake) Name() string                         { return f.name }
func (f fake) Version() int                         { return f.version }
func (f fake) Read(Declared) (Config, error)        { return nil, nil }
func (f fake) Note(Config) Note                     { return Note{} }
func (f fake) Serve(context.Context, Session) error { return nil }

func TestLookupByNameAndVersion(t *testing.T) {
	r := NewRegistry()
	r.Register(fake{"chat", 1})
	r.Register(fake{"chat", 2})
	r.Register(fake{"tty", 1})

	for _, at := range []struct {
		name    string
		version int
		want    int
	}{
		{"chat", 1, 1},
		{"chat", 2, 2},
		{"tty", 1, 1},
		// Version zero is what a config that names none is asking for: whichever is newest.
		{"chat", 0, 2},
		{"tty", 0, 1},
	} {
		got, ok := r.Lookup(at.name, at.version)
		if !ok {
			t.Errorf("Lookup(%q, %d) found nothing", at.name, at.version)
			continue
		}
		if got.Version() != at.want {
			t.Errorf("Lookup(%q, %d) gave version %d, want %d", at.name, at.version, got.Version(), at.want)
		}
	}

	if _, ok := r.Lookup("camera", 0); ok {
		t.Error("Lookup() found an archetype nobody registered")
	}
	if _, ok := r.Lookup("chat", 9); ok {
		t.Error("Lookup() found a version nobody registered")
	}
}

// Registering the same name and version twice replaces rather than duplicates, so a test that
// swaps one out gets the one it registered.
func TestRegisteringReplaces(t *testing.T) {
	first, second := fake{"chat", 1}, fake{"chat", 1}

	r := NewRegistry()
	r.Register(first)
	r.Register(second)

	if got, _ := r.Lookup("chat", 1); got != Archetype(second) {
		t.Error("the later registration did not replace the earlier one")
	}
	if names := r.Names(); len(names) != 1 {
		t.Errorf("Names() = %v", names)
	}
}

func TestNamesAreInReadingOrder(t *testing.T) {
	r := NewRegistry()
	for _, name := range []string{"tty", "chat", "share"} {
		r.Register(fake{name, 1})
	}

	got := strings.Join(r.Names(), " ")
	if got != "chat share tty" {
		t.Errorf("Names() = %q", got)
	}
}

// A name nobody registered is a typo, and the answer is what this build does have.
func TestMissingListsWhatIsKnown(t *testing.T) {
	r := NewRegistry()
	r.Register(fake{"chat", 1})
	r.Register(fake{"share", 1})
	r.Register(fake{"tty", 1})

	err := r.Missing("camera", 0)
	for _, word := range []string{`"camera"`, "chat, share or tty"} {
		if !strings.Contains(err.Error(), word) {
			t.Errorf("Missing() said %q, which does not mention %s", err, word)
		}
	}

	// A version of something that does exist is a different mistake, and says so.
	err = r.Missing("chat", 4)
	if !strings.Contains(err.Error(), "there is no chat/4, only chat/1") {
		t.Errorf("Missing() said %q", err)
	}

	// And a build with nothing registered says that rather than trailing off.
	if err := NewRegistry().Missing("chat", 0); !strings.Contains(err.Error(), "registered no archetypes") {
		t.Errorf("Missing() said %q", err)
	}
}
