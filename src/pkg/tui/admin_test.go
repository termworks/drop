package tui

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/bresilla/drop/src/pkg/book"
	"github.com/bresilla/drop/src/pkg/proto"
)

// adminFake is what the fake answers about who may open what, and what it was asked to change.
type adminFake struct {
	reach          map[string][]Reachable
	decided        []string
	renamed        []string
	joinedMachine  string
	offeredMachine bool
}

func (f *fake) Rename(old, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.admin.renamed = append(f.admin.renamed, old+"→"+name)
	return nil
}

func (f *fake) OfferMachine(ctx context.Context) (string, <-chan string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.admin.offeredMachine = true
	return "7b9773d9#abcd-efgh-ijkl", make(chan string, 1), nil
}

func (f *fake) JoinMachine(ctx context.Context, code string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.admin.joinedMachine = code
	return "tron", nil
}

func (f *fake) Reachable(ctx context.Context, who string) ([]Reachable, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.admin.reach[who], nil
}

// Ask is the fake's whole grant store: a path's detail, changed the way the real one changes it.
func (f *fake) Ask(ctx context.Context, machine string, m proto.Manage) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.details == nil {
		f.details = map[string]PathDetail{}
	}
	key := machine + m.Path
	d := f.details[key]
	d.Path = m.Path
	switch m.Op {
	case proto.ManageLevel:
		d.Level, d.Chosen = m.Level, m.Level != ""
	case proto.ManageShown:
		d.Shown = m.Shown
	case proto.ManageAllow, proto.ManageDeny, proto.ManageUnset:
		at := map[string]string{proto.ManageAllow: "allowed", proto.ManageDeny: "refused", proto.ManageUnset: ""}[m.Op]
		f.admin.decided = append(f.admin.decided, machine+":"+m.Path+":"+m.Who+":"+at)
		found := false
		for i := range d.Who {
			if d.Who[i].Name == m.Who {
				d.Who[i].At, found = at, true
			}
		}
		if !found {
			d.Who = append(d.Who, WhoState{Name: m.Who, At: at})
		}
		var still []AskingState
		for _, a := range d.Asking {
			if a.Who != m.Who {
				still = append(still, a)
			}
		}
		d.Asking = still
	}
	f.details[key] = d
	return json.Marshal(d)
}

// press is one key, the way the keyboard sends it.
func press(t *testing.T, m Model, keys ...string) Model {
	t.Helper()
	for _, k := range keys {
		switch k {
		case "enter":
			m = settle(t, m, tea.KeyMsg{Type: tea.KeyEnter})
		case "esc":
			m = settle(t, m, tea.KeyMsg{Type: tea.KeyEsc})
		case "down":
			m = settle(t, m, tea.KeyMsg{Type: tea.KeyDown})
		case " ":
			m = settle(t, m, tea.KeyMsg{Type: tea.KeySpace, Runes: []rune(" ")})
		default:
			m = settle(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)})
		}
	}
	return m
}

// onRow puts the cursor on the admin row with a label.
func onRow(t *testing.T, m *Model, label string) {
	t.Helper()
	for i, item := range m.list.Items() {
		if it, ok := item.(manageItem); ok && it.label == label {
			m.list.Select(i)
			return
		}
	}
	t.Fatalf("no row for %q in:\n%s", label, m.View())
}

// labels is every row on the list, headings in capitals, whatever fits on the screen.
func labels(m Model) string {
	var rows []string
	for _, item := range m.list.Items() {
		switch it := item.(type) {
		case dividerItem:
			rows = append(rows, strings.ToUpper(it.label))
		case manageItem:
			rows = append(rows, it.label+" ("+it.note+")")
		}
	}
	return strings.Join(rows, " | ")
}

func standingOf(m Model, name string) string {
	for _, w := range m.detail.Who {
		if w.Name == name {
			return w.At
		}
	}
	return ""
}

// Who may open one of your paths is the ladder the phone shows, and the names let in or kept out.
func TestWhoMayOpenAPathIsShownAndChanged(t *testing.T) {
	back := &fake{
		self: Identity{Name: "tron", ID: "e88c42df318c…", User: "ssh-ed25519 MINE"},
		mine: []proto.Served{{Path: "/work", Archetype: "chat"}},
		details: map[string]PathDetail{"/work": {
			PathState: PathState{Path: "/work", Level: "paired"},
			Who:       []WhoState{{Name: "bob", Person: true}, {Name: "carol", Person: true, At: "allowed", InConfig: true}},
		}},
	}

	m := intoSelf(t, start(t, back))
	m = press(t, m, "w")
	if m.at != levelAccess {
		t.Fatalf("w did not open who may open it, at level %d", m.at)
	}
	got := labels(m)
	for _, want := range []string{"WHO MAY OPEN IT", "Only me", "Trusted", "Paired", "Public", "Others may see it and ask", "BY NAME", "bob", "carol"} {
		if !strings.Contains(got, want) {
			t.Errorf("who may open it is missing %q:\n  %s", want, got)
		}
	}

	onRow(t, &m, "bob")
	m = press(t, m, "a")
	if got := standingOf(m, "bob"); got != "allowed" {
		t.Errorf("bob stands at %q after being let in", got)
	}
	onRow(t, &m, "bob")
	m = press(t, m, "x")
	if got := standingOf(m, "bob"); got != "refused" {
		t.Errorf("bob stands at %q after being kept out", got)
	}
	onRow(t, &m, "bob")
	m = press(t, m, "d")
	if got := standingOf(m, "bob"); got != "" {
		t.Errorf("bob stands at %q after being left to the step", got)
	}

	// A step is a row: enter puts the path on it.
	onRow(t, &m, "Trusted")
	m = press(t, m, "enter")
	if m.detail.Level != "trusted" {
		t.Errorf("the path is on %q after choosing Trusted", m.detail.Level)
	}
	onRow(t, &m, "Others may see it and ask")
	m = press(t, m, "enter")
	if !m.detail.Shown {
		t.Error("enter did not let others see it")
	}
}

// A request waits where the path's access is, and letting them in answers it.
func TestARequestWaitsInTheAccessPane(t *testing.T) {
	back := &fake{
		mine: []proto.Served{{Path: "/vault", Archetype: "chat"}},
		details: map[string]PathDetail{"/vault": {
			PathState: PathState{Path: "/vault", Level: "me", Shown: true},
			Asking:    []AskingState{{Who: "carol", Why: "for the thing we discussed", When: "24 Aug 21:05"}},
		}},
	}

	m := intoSelf(t, start(t, back))
	m = press(t, m, "w")
	got := labels(m)
	for _, want := range []string{"ASKING TO BE LET IN", "carol", "for the thing we discussed"} {
		if !strings.Contains(got, want) {
			t.Errorf("the request is missing %q:\n  %s", want, got)
		}
	}

	onRow(t, &m, "carol")
	m = press(t, m, "a")
	if got := standingOf(m, "carol"); got != "allowed" {
		t.Errorf("carol stands at %q after being let in", got)
	}
	if len(m.detail.Asking) != 0 {
		t.Error("carol is still waiting after being let in")
	}
}

// Managing somebody is who they are, whether you trust them, and what they may open on every
// machine of yours — changed from the same screen.
func TestSomebodyCanBeManaged(t *testing.T) {
	back := &fake{
		self:  Identity{Name: "tron", ID: "e88c…", User: "ssh-ed25519 MINE"},
		peers: []book.Entry{{Name: "bob", ID: idFor(3), Secret: make([]byte, book.SecretBytes), User: "ssh-ed25519 BOB", Person: "bob"}},
		manages: map[string]Managed{
			"bob": {Person: "bob", User: "ssh-ed25519 BOB", Machines: 2, Paired: true},
		},
	}
	back.admin.reach = map[string][]Reachable{"bob": {
		{ReachState: ReachState{Called: "bob", Known: true, Paths: []PathOpen{
			{Path: "/work", Level: "paired", Opens: true},
			{Path: "/keys", Level: "paired", At: "refused"},
		}}},
		{Machine: "laptop", Err: "laptop did not answer"},
	}}

	m := start(t, back)
	onUser(t, &m, "bob")
	m = press(t, m, "m")
	if m.at != levelManage {
		t.Fatalf("m did not open the management screen, at level %d", m.at)
	}

	got := labels(m)
	for _, want := range []string{"WHO THEY ARE", "TRUST", "not trusted", "WHAT THEY MAY OPEN", "ON THIS MACHINE",
		"/work (opens · Paired)", "/keys (shut · kept out by name)", "ON LAPTOP", "could not ask", "rename", "remove them"} {
		if !strings.Contains(got, want) {
			t.Errorf("the management screen is missing %q:\n  %s", want, got)
		}
	}
	if !strings.Contains(m.View(), "2 machines") {
		t.Errorf("it does not say how many machines they have:\n%s", m.View())
	}

	m = press(t, m, "t")
	if !m.managed.Trusted {
		t.Error("t did not trust them")
	}

	onRow(t, &m, "/keys")
	m = press(t, m, "a")
	back.mu.Lock()
	decided := append([]string(nil), back.admin.decided...)
	back.mu.Unlock()
	if len(decided) != 1 || decided[0] != ":/keys:bob:allowed" {
		t.Errorf("letting bob into /keys decided %v", decided)
	}

	m = press(t, m, "esc")
	if m.at != levelUsers {
		t.Errorf("esc left the management screen at level %d", m.at)
	}
}

// Removing somebody asks first, and goes back to the list: there is nobody left to show.
func TestForgettingSomebodyLeavesTheScreen(t *testing.T) {
	back := &fake{
		peers:   []book.Entry{{Name: "bob", ID: idFor(3), Secret: make([]byte, book.SecretBytes)}},
		manages: map[string]Managed{"bob": {Paired: true}},
	}

	m := intoUser(t, start(t, back), Anon)
	m = press(t, m, "m", "f")
	if m.confirm == nil {
		t.Fatal("removing somebody did not ask first")
	}
	m = press(t, m, "y")

	back.mu.Lock()
	forgot := append([]string(nil), back.forgot...)
	back.mu.Unlock()
	if len(forgot) != 1 || forgot[0] != "bob" {
		t.Fatalf("forgot %v", forgot)
	}
	if m.at == levelManage {
		t.Errorf("it stayed on a screen for somebody who is gone")
	}
}

// A machine of yours is renamed and taken out from your own machines, each asking what it needs.
func TestAMachineOfYoursIsRenamedAndTakenOut(t *testing.T) {
	back := &fake{
		self:  Identity{Name: "tron", ID: "e88c…", User: "ssh-ed25519 MINE"},
		peers: []book.Entry{{Name: "laptop", ID: idFor(4), Secret: make([]byte, book.SecretBytes), User: "ssh-ed25519 MINE", Person: "tron"}},
	}

	m := intoUser(t, start(t, back), Me)
	m.list.Select(1)
	m = press(t, m, "n")
	if m.prompt == nil {
		t.Fatal("n did not ask for a name")
	}
	m = settle(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("desk")})
	m = press(t, m, "enter")

	m.list.Select(1)
	m = press(t, m, "x")
	if m.confirm == nil || !strings.Contains(m.confirm.ask, "out of your machines") {
		t.Fatalf("taking a machine out asked %+v", m.confirm)
	}
	m = press(t, m, "y")

	back.mu.Lock()
	renamed, forgot := append([]string(nil), back.admin.renamed...), append([]string(nil), back.forgot...)
	back.mu.Unlock()
	if len(renamed) != 1 || renamed[0] != "laptop→desk" {
		t.Errorf("renamed %v", renamed)
	}
	if len(forgot) != 1 || forgot[0] != "laptop" {
		t.Errorf("took out %v", forgot)
	}
}

// Adding a machine of yours and joining yours are each one key from the first screen.
func TestYourMachinesAreAddedAndJoinedFromTheFirstScreen(t *testing.T) {
	back := &fake{self: Identity{Name: "tron", User: "ssh-ed25519 MINE"}, peers: []book.Entry{{Name: "bob", ID: idFor(3)}}}

	m := press(t, start(t, back), "a")
	if m.linking == nil || !m.linking.machine || !strings.Contains(m.View(), "drop machine join abcd-efgh-ijkl") {
		t.Fatalf("a did not show a code for a machine of yours:\n%s", m.View())
	}
	m = press(t, m, "esc")

	m = press(t, m, "c")
	if m.prompt == nil {
		t.Fatal("c did not ask for a code")
	}
	m = settle(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("abcd-efgh-ijkl")})
	m = press(t, m, "enter")

	back.mu.Lock()
	joined := back.admin.joinedMachine
	back.mu.Unlock()
	if joined != "abcd-efgh-ijkl" {
		t.Errorf("joined your machines with %q", joined)
	}
}

// Every action is on the screen: the line along the bottom starts them, and space lists them all
// and does whichever is picked.
func TestEveryActionIsOnTheScreen(t *testing.T) {
	back := &fake{self: Identity{Name: "tron", User: "ssh-ed25519 MINE"}, peers: []book.Entry{{Name: "bob", ID: idFor(3)}}}

	m := start(t, back)
	if shown := m.View(); !strings.Contains(shown, "all actions") || !strings.Contains(shown, "machines") {
		t.Errorf("the bottom line does not say what can be done:\n%s", shown)
	}

	m = press(t, m, " ")
	if m.menu == nil {
		t.Fatal("space did not open the actions")
	}
	shown := m.View()
	for _, want := range []string{"pair with somebody", "add a machine", "join your machines"} {
		if !strings.Contains(shown, want) {
			t.Errorf("the actions are missing %q:\n%s", want, shown)
		}
	}

	for m.menu != nil && m.menu.items[m.menu.at].key != "a" {
		m = press(t, m, "down")
	}
	m = press(t, m, "enter")
	back.mu.Lock()
	offered := back.admin.offeredMachine
	back.mu.Unlock()
	if !offered {
		t.Error("picking an action from the menu did not do it")
	}
}

// nearFake is what the fake says is nearby and asking, and what it was asked to do about them.
type nearFake struct {
	near    []Near
	asking  []Invited
	invited []string
	decided []string
	left    bool
	over    bool
}

func (f *fake) Nearby() ([]Near, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.nearby.near, nil
}

func (f *fake) Invite(ctx context.Context, id, kind string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nearby.invited = append(f.nearby.invited, kind+":"+id)
	return "box-x", nil
}

func (f *fake) Invited() ([]Invited, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.nearby.asking, nil
}

func (f *fake) Decide(id string, yes bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	answer := "no"
	if yes {
		answer = "yes"
	}
	f.nearby.decided = append(f.nearby.decided, id+":"+answer)
	f.nearby.asking = nil
	return nil
}

func (f *fake) Leave(ctx context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nearby.left = true
	return nil
}

func (f *fake) StartOver(ctx context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nearby.over = true
	return nil
}
