package grant

import "testing"

func TestALevelCoversWhatIsBelowItAndKeepsTheNamesAllowed(t *testing.T) {
	s := empty(t)

	if err := s.Allow("/work", "carol"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetLevel("/work", "trusted"); err != nil {
		t.Fatal(err)
	}
	if level, _ := s.Level("/work/notes"); level != "trusted" {
		t.Fatalf("level below /work = %q, want trusted", level)
	}
	if allow, _ := s.For("/work"); len(allow) != 1 || allow[0] != "carol" {
		t.Fatalf("setting a level lost who was let in: %v", allow)
	}

	if err := s.SetLevel("/work", ""); err != nil {
		t.Fatal(err)
	}
	if level, _ := s.Level("/work"); level != "" {
		t.Fatalf("level handed back to the config = %q, want none", level)
	}
	if err := s.SetLevel("/work", "everybody"); err == nil {
		t.Fatal("a level that is not one was taken")
	}
}

func TestBeingSeenIsSetOnItsOwn(t *testing.T) {
	s := empty(t)

	off := false
	if err := s.SetShown("/vault", &off); err != nil {
		t.Fatal(err)
	}
	if _, shown := s.Level("/vault"); shown == nil || *shown {
		t.Fatalf("shown = %v, want false", shown)
	}
}

func TestARenameCarriesEveryGrantWithIt(t *testing.T) {
	s := empty(t)

	for _, who := range []string{"bob", "bob@laptop", "carol@laptop"} {
		if err := s.Allow("/work", who); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Deny("/vault", "bob"); err != nil {
		t.Fatal(err)
	}

	if err := s.Rename("bob", "robert", false); err != nil {
		t.Fatal(err)
	}
	allow, _ := s.For("/work")
	want := []string{"carol@laptop", "robert", "robert@laptop"}
	if len(allow) != len(want) {
		t.Fatalf("allowed %v, want %v", allow, want)
	}
	for i := range want {
		if allow[i] != want[i] {
			t.Fatalf("allowed %v, want %v", allow, want)
		}
	}
	if _, deny := s.For("/vault"); len(deny) != 1 || deny[0] != "robert" {
		t.Fatalf("refused %v, want robert", deny)
	}

	if err := s.Rename("laptop", "desk", true); err != nil {
		t.Fatal(err)
	}
	if allow, _ := s.For("/work"); allow[0] != "carol@desk" {
		t.Fatalf("a machine rename left %v", allow)
	}
}
