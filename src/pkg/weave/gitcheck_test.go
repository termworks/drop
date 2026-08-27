package weave

import (
	"bytes"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// gitMerge is what git merge-file makes of one three-way merge, and whether it merged it cleanly.
func gitMerge(t *testing.T, dir string, base, ours, theirs []byte) ([]byte, bool) {
	t.Helper()
	write := func(name string, raw []byte) string {
		at := filepath.Join(dir, name)
		if err := os.WriteFile(at, raw, 0o600); err != nil {
			t.Fatal(err)
		}
		return at
	}
	o, b, th := write("ours", ours), write("base", base), write("theirs", theirs)

	cmd := exec.Command("git", "merge-file", "-p", "-q", o, b, th)
	out, err := cmd.Output()
	if err == nil {
		return out, true
	}
	if _, bad := err.(*exec.ExitError); bad {
		return out, false
	}
	t.Fatal(err)
	return nil, false
}

// triple is one randomly made base and two versions somebody made of it.
func triple(rng *rand.Rand, pool []string) (string, string, string) {
	var base []string
	for n := rng.Intn(7) + 1; n > 0; n-- {
		base = append(base, pool[rng.Intn(len(pool))])
	}

	edit := func() []string {
		out := append([]string(nil), base...)
		for n := rng.Intn(3) + 1; n > 0; n-- {
			switch at := rng.Intn(len(out) + 1); rng.Intn(3) {
			case 0:
				out = append(out[:at], append([]string{pool[rng.Intn(len(pool))]}, out[at:]...)...)
			case 1:
				if at < len(out) {
					out = append(out[:at], out[at+1:]...)
				}
			default:
				if at < len(out) {
					out[at] = pool[rng.Intn(len(pool))]
				}
			}
		}
		return out
	}
	return strings.Join(base, ""), strings.Join(edit(), ""), strings.Join(edit(), "")
}

// TestTheLineMergeAgreesWithGit walks a fixed set of random three-way merges and holds every one
// git merges without markers against what this makes of it.
func TestTheLineMergeAgreesWithGit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not here to be held up against")
	}
	dir := t.TempDir()
	pool := []string{"a\n", "b\n", "\n", "}\n", "---\n", "- eggs\n"}
	rng := rand.New(rand.NewSource(20250826))

	clean, off, spurious := 0, 0, 0
	for i := 0; i < 30000; i++ {
		base, ours, theirs := triple(rng, pool)
		want, ok := gitMerge(t, dir, []byte(base), []byte(ours), []byte(theirs))
		if !ok {
			continue
		}
		clean++

		got, _ := Bytes([]byte(base), []byte(ours), []byte(theirs), "us", "them")
		if bytes.Contains(got, []byte("<<<<<<<")) {
			spurious++
			if spurious < 4 {
				t.Logf("spurious conflict:\nbase %q\nours %q\ntheirs %q\ngot\n%s", base, ours, theirs, got)
			}
			continue
		}
		if !bytes.Equal(got, want) {
			off++
			if off < 6 {
				t.Logf("divergence:\nbase %q\nours %q\ntheirs %q\ngot %q\nwant %q", base, ours, theirs, got, want)
			}
		}
	}
	fmt.Printf("clean in git: %d, silent divergence: %d, spurious conflict: %d\n", clean, off, spurious)
	if off != 0 {
		t.Fatalf("%d of %d merges git calls clean came out differently", off, clean)
	}
	if spurious != 0 {
		t.Fatalf("%d of %d merges git calls clean were marked as conflicts", spurious, clean)
	}
}
