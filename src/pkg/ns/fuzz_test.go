package ns

import (
	"strings"
	"testing"
)

// Every path a peer names goes through Clean before anything is decided about it.
//
// What it must never do is hand back something that leaves the namespace it was resolved under. A
// path that climbs out with .. or a separator nobody expected is a path that reaches a directory
// the rule was never written for.
func FuzzClean(f *testing.F) {
	f.Add("/notes")
	f.Add("/a/../b")
	f.Add("../../etc/passwd")
	f.Add("//a//b//")
	f.Add("/a/./b")
	f.Add("")
	f.Add("/")
	f.Add("\\a\\b")
	f.Add("/a\x00b")
	f.Add("/a/b/../../..")

	f.Fuzz(func(t *testing.T, path string) {
		clean, err := Clean(path)
		if err != nil {
			return
		}

		switch {
		case clean == "":
			t.Fatalf("%q cleaned to nothing", path)
		case !strings.HasPrefix(clean, "/"):
			t.Fatalf("%q cleaned to %q, which is not rooted", path, clean)
		case strings.Contains(clean, "\x00"):
			t.Fatalf("%q cleaned to %q, which carries a nul", path, clean)
		}

		// Nothing that climbs, however it was spelt.
		for _, part := range strings.Split(clean, "/") {
			if part == ".." || part == "." {
				t.Fatalf("%q cleaned to %q, which still has %q in it", path, clean, part)
			}
		}

		// And cleaning what is already clean changes nothing, or two spellings of one path are two
		// different namespaces and a rule written for one does not cover the other.
		again, err := Clean(clean)
		if err != nil {
			t.Fatalf("%q cleaned to %q, which will not clean: %v", path, clean, err)
		}
		if again != clean {
			t.Fatalf("%q cleaned to %q and then to %q", path, clean, again)
		}
	})
}
