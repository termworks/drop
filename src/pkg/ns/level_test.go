package ns

import "testing"

// stepped puts every path on one step, lets some names in and keeps some out.
type stepped struct {
	level       string
	shown       *bool
	allow, deny []string
}

func (s stepped) For(string) (allow, deny []string) { return s.allow, s.deny }
func (s stepped) Level(string) (string, *bool)      { return s.level, s.shown }

func TestAStepReplacesTheRuleTheConfigWrote(t *testing.T) {
	written := Access{AnyPaired: true, Named: []string{"bob"}, Password: "x"}

	got, found := merge(stepped{level: LevelMe, allow: []string{"carol"}}, "/chat", written, true)
	if !found || got.AnyPaired || got.Password != "" {
		t.Fatalf("only me still let the config's rule in: %+v", got)
	}
	me := Caller{ID: "a", Name: "phone", UserName: LevelMe, Paired: true, Trusted: true}
	carol := Caller{ID: "b", Name: "carol", UserName: "carol", Paired: true}
	bob := Caller{ID: "c", Name: "bob", UserName: "bob", Paired: true}
	for _, c := range []Caller{me, carol} {
		if ok, why := got.Admits(c); !ok {
			t.Fatalf("%s refused: %s", c.Name, why)
		}
	}
	if ok, _ := got.Admits(bob); ok {
		t.Fatal("bob, named only by the config, was let in past only me")
	}
	if LevelOf(got) != LevelMe {
		t.Fatalf("LevelOf = %q, want me", LevelOf(got))
	}
}

func TestAKeptOutNameStaysOutOnEveryStep(t *testing.T) {
	got, _ := merge(stepped{level: LevelAnyone, deny: []string{"bob"}}, "/open", Access{}, true)
	bob := Caller{ID: "c", Name: "bob", UserName: "bob", Paired: true}
	if ok, _ := got.Admits(bob); ok {
		t.Fatal("somebody kept out was let in by a public step")
	}
	if LevelOf(got) != LevelAnyone {
		t.Fatalf("LevelOf = %q, want anyone", LevelOf(got))
	}
}

func TestBeingSeenCanBeTurnedOff(t *testing.T) {
	off := false
	got, _ := merge(stepped{shown: &off}, "/vault", Access{Named: []string{LevelMe}, AnyVisible: true, Visible: []string{"bob"}}, true)
	if got.Shows() {
		t.Fatalf("a path set not to be seen still shows: %+v", got)
	}
}
