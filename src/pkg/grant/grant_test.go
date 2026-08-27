package grant

import (
	"fmt"
	"sync"
	"testing"

	"github.com/bresilla/drop/src/pkg/ns"
)

func empty(t *testing.T) *Store {
	t.Helper()

	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	s, err := Load()
	if err != nil {
		t.Fatalf("Load(): %v", err)
	}
	return s
}

func TestAGrantSurvivesBeingWrittenAndReadBack(t *testing.T) {
	s := empty(t)

	if err := s.Allow("/work", "carol@laptop"); err != nil {
		t.Fatal(err)
	}
	if err := s.Deny("/work", "bob@phone"); err != nil {
		t.Fatal(err)
	}

	back, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	allow, deny := back.For("/work")
	if len(allow) != 1 || allow[0] != "carol@laptop" {
		t.Errorf("allowed %v", allow)
	}
	if len(deny) != 1 || deny[0] != "bob@phone" {
		t.Errorf("denied %v", deny)
	}
}

// Allowing somebody who is refused is how somebody undoes a revocation, and it has to leave them
// allowed rather than both at once.
func TestAllowingSomebodyRefusedLiftsTheRefusal(t *testing.T) {
	s := empty(t)

	if err := s.Deny("/work", "bob"); err != nil {
		t.Fatal(err)
	}
	if err := s.Allow("/work", "bob"); err != nil {
		t.Fatal(err)
	}

	allow, deny := s.For("/work")
	if len(deny) != 0 {
		t.Errorf("still refused: %v", deny)
	}
	if len(allow) != 1 {
		t.Errorf("not allowed: %v", allow)
	}
}

// A grant covers everything under the path it is written at, and a refusal at the root covers
// everything there is.
func TestGrantsInheritDownwards(t *testing.T) {
	s := empty(t)

	if err := s.Allow("/work", "carol"); err != nil {
		t.Fatal(err)
	}
	if err := s.Deny("/", "bob@phone"); err != nil {
		t.Fatal(err)
	}

	allow, deny := s.For("/work/notes/today")
	if len(allow) != 1 || allow[0] != "carol" {
		t.Errorf("a grant did not reach a path below it: %v", allow)
	}
	if len(deny) != 1 || deny[0] != "bob@phone" {
		t.Errorf("a refusal at the root did not reach a path below it: %v", deny)
	}

	if _, deny = s.For("/elsewhere"); len(deny) != 1 {
		t.Errorf("a refusal at the root missed another path entirely: %v", deny)
	}
	if allow, _ = s.For("/elsewhere"); len(allow) != 0 {
		t.Errorf("a grant on /work reached /elsewhere: %v", allow)
	}
}

// The whole point of a refusal: it takes effect against a rule somebody wrote by hand.
func TestARefusalBeatsTheConfig(t *testing.T) {
	s := empty(t)

	table := ns.NewTable()
	if err := table.Add(ns.Mount{Path: "/work", Archetype: "chat", Access: ns.Access{Named: []string{"bob"}}}); err != nil {
		t.Fatal(err)
	}
	table.Granted(s)

	bob := ns.Caller{ID: "abc", Name: "phone", UserName: "bob", Paired: true}
	if ok, why := table.Admits("/work", bob); !ok {
		t.Fatalf("bob was refused before anything was revoked: %s", why)
	}

	if err := s.Deny("/work", "bob@phone"); err != nil {
		t.Fatal(err)
	}
	if ok, _ := table.Admits("/work", bob); ok {
		t.Fatal("bob's phone was admitted after being revoked")
	}

	// His other machines are untouched: one machine was revoked, not the person.
	laptop := ns.Caller{ID: "def", Name: "laptop", UserName: "bob", Paired: true}
	if ok, why := table.Admits("/work", laptop); !ok {
		t.Errorf("revoking bob's phone shut out his laptop: %s", why)
	}
}

// A grant is the other half: somebody the config never mentioned, let in from the interface.
func TestAGrantOpensAPathTheConfigDidNot(t *testing.T) {
	s := empty(t)

	table := ns.NewTable()
	if err := table.Add(ns.Mount{Path: "/work", Archetype: "chat", Access: ns.Access{Named: []string{"bob"}}}); err != nil {
		t.Fatal(err)
	}
	table.Granted(s)

	carol := ns.Caller{ID: "abc", Name: "laptop", UserName: "carol", Paired: true}
	if ok, _ := table.Admits("/work", carol); ok {
		t.Fatal("carol got in before she was granted anything")
	}

	if err := s.Allow("/work", "carol@laptop"); err != nil {
		t.Fatal(err)
	}
	if ok, why := table.Admits("/work", carol); !ok {
		t.Fatalf("carol was refused after being granted the path: %s", why)
	}
}

// A refusal names an endpoint id as readily as a person, because the device being shut out is very
// often one that is in the address book and holds a pairing secret.
func TestARefusalNamesABareID(t *testing.T) {
	s := empty(t)

	table := ns.NewTable()
	if err := table.Add(ns.Mount{Path: "/wide", Archetype: "chat", Access: ns.Access{Anyone: true}}); err != nil {
		t.Fatal(err)
	}
	table.Granted(s)

	stranger := ns.Caller{ID: "7b97"}
	if ok, why := table.Admits("/wide", stranger); !ok {
		t.Fatalf("a public path refused a stranger: %s", why)
	}

	if err := s.Deny("/wide", "7b97"); err != nil {
		t.Fatal(err)
	}
	if ok, _ := table.Admits("/wide", stranger); ok {
		t.Fatal("a refused id reached a public path")
	}
}

// A grant made in another process has to be noticed by one that is already running, or revoking
// somebody looks exactly like revoking not working. Nobody has to ask: reading the grants is what
// notices the change.
func TestARefusalMadeElsewhereIsNoticed(t *testing.T) {
	s := empty(t)

	serving, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if _, deny := serving.For("/work"); len(deny) != 0 {
		t.Fatal("something was refused before anything was written")
	}

	if err := s.Deny("/work", "bob"); err != nil {
		t.Fatal(err)
	}
	if _, deny := serving.For("/work"); len(deny) != 1 {
		t.Errorf("the running store missed the refusal: %v", deny)
	}

	if err := s.Forget("/work", "bob"); err != nil {
		t.Fatal(err)
	}
	if _, deny := serving.For("/work"); len(deny) != 0 {
		t.Errorf("the running store missed the refusal being lifted: %v", deny)
	}
}

// The interface grants from one process and `drop path grant` from another. A decision lost
// between them is somebody let in who was shut out, or shut out who was let in, with nothing at all
// to say it happened.
func TestTwoProcessesGrantingDoNotLoseEachOther(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

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
			_ = s.Allow(fmt.Sprintf("/p%d", i), fmt.Sprintf("bob%d", i))
		}()
	}
	wg.Wait()

	after, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	for i := range stores {
		allow, _ := after.For(fmt.Sprintf("/p%d", i))
		if len(allow) == 0 {
			t.Errorf("/p%d was granted and the grant is gone", i)
		}
	}
}
