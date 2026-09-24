package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/bresilla/drop/src/pkg/proto"
)

// Who may open what, on every machine of yours, and what each person may open.
//
// The same answers the phone draws, asked the same way — a machine of yours is asked, this one
// included, and answers with what it holds — so the two interfaces cannot come to disagree about
// who is let in where.

// PathState is one path, and who may reach it.
type PathState struct {
	Path      string `json:"path"`
	Archetype string `json:"archetype"`
	About     string `json:"about"`
	// Level is the step it stands on, and Chosen says it was put there from an interface rather
	// than by the config, whose own step is Config.
	Level  string `json:"level"`
	Chosen bool   `json:"chosen"`
	Config string `json:"config"`
	// Shown says those who may not open it may see it is there, and ask.
	Shown    bool `json:"shown"`
	Password bool `json:"password"`
	// Allowed is who is let in beyond the step, and Refused who is kept out whatever it says.
	Allowed []string `json:"allowed"`
	Refused []string `json:"refused"`
	Asked   int      `json:"asked"`
}

// PathDetail is one path with everybody who might be let in or kept out, and who asked.
type PathDetail struct {
	PathState
	Who    []WhoState    `json:"who"`
	Asking []AskingState `json:"asking"`
}

// WhoState is somebody in the address book, and how they stand with a path.
type WhoState struct {
	Name     string `json:"name"`
	Person   bool   `json:"person"`
	Trusted  bool   `json:"trusted"`
	At       string `json:"at"`
	InConfig bool   `json:"inConfig"`
}

// AskingState is somebody waiting to be let in.
type AskingState struct {
	Who  string `json:"who"`
	Why  string `json:"why"`
	When string `json:"when"`
}

// ReachState is what one person may open on one machine: what that machine calls them, whether it
// knows them at all, and each of its paths.
type ReachState struct {
	Called string     `json:"called"`
	Known  bool       `json:"known"`
	Paths  []PathOpen `json:"paths"`
}

// PathOpen is one path as one person stands with it: whether they open it, and whether that is the
// step it stands on or a name let in or kept out.
type PathOpen struct {
	Path      string `json:"path"`
	Archetype string `json:"archetype"`
	About     string `json:"about"`
	Level     string `json:"level"`
	Opens     bool   `json:"opens"`
	At        string `json:"at"`
}

// Reachable is what one person may open on one machine of yours, the empty machine being this one,
// or why that machine could not say.
type Reachable struct {
	Machine string `json:"machine"`
	ReachState
	Err string `json:"err,omitempty"`
}

// Near is a device on this network that nobody here has connected with yet: what it calls itself,
// and whose it says it is when its badge is one this machine knows.
type Near struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Whose is who it belongs to as the address book knows them, empty for a stranger.
	Whose string `json:"whose"`
}

// Invited is a device asking this one to connect, waiting for a yes: what it asks, and the number
// both screens show.
type Invited struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Whose string `json:"whose"`
	Kind  string `json:"kind"`
	Check string `json:"check"`
	When  int64  `json:"when"`
}

// A step on the ladder, as both interfaces name it.
type ladderStep struct{ level, title, says string }

var ladder = []ladderStep{
	{"me", "Only me", "your own machines, and nobody else"},
	{"trusted", "Trusted", "you, and the people you trust"},
	{"paired", "Paired", "everybody you paired with"},
	{"anyone", "Public", "anyone who knows this machine's id"},
}

// stepTitle is a step as a word.
func stepTitle(level string) string {
	for _, s := range ladder {
		if s.level == level {
			return s.title
		}
	}
	if level == "custom" {
		return "Custom"
	}
	return level
}

// ---------------------------------------------------------------- asking

// detailLoaded carries one path's detail back, from whichever machine was asked.
type detailLoaded struct {
	machine string
	detail  PathDetail
	err     error
}

// manageWithin bounds one question to a machine of yours.
const manageWithin = 20 * time.Second

// askPath asks a machine of yours about one path, or changes it and reads it back.
func askPath(back Backend, machine string, m proto.Manage) tea.Cmd {
	return func() tea.Msg {
		ctx, stop := context.WithTimeout(context.Background(), manageWithin)
		defer stop()

		raw, err := back.Ask(ctx, machine, m)
		if err != nil {
			return detailLoaded{machine: machine, err: err}
		}
		var out PathDetail
		if err := json.Unmarshal(raw, &out); err != nil {
			return detailLoaded{machine: machine, err: fmt.Errorf("%s answered something unreadable: %w", machineOr(machine), err)}
		}
		return detailLoaded{machine: machine, detail: out}
	}
}

// reachLoaded carries what somebody may open, on every machine of yours.
type reachLoaded struct {
	who   string
	reach []Reachable
	err   error
}

func loadReach(back Backend, who string) tea.Cmd {
	return func() tea.Msg {
		ctx, stop := context.WithTimeout(context.Background(), manageWithin)
		defer stop()

		reach, err := back.Reachable(ctx, who)
		return reachLoaded{who: who, reach: reach, err: err}
	}
}

// decidedFor lets somebody into a path on a machine of yours, keeps them out, or leaves them to the
// step, and reads what they may open back.
func decidedFor(back Backend, machine, path, called, who string, to Standing) tea.Cmd {
	return func() tea.Msg {
		ctx, stop := context.WithTimeout(context.Background(), manageWithin)
		defer stop()

		op := proto.ManageUnset
		switch to {
		case Allowed:
			op = proto.ManageAllow
		case Refused:
			op = proto.ManageDeny
		}
		if _, err := back.Ask(ctx, machine, proto.Manage{Op: op, Path: path, Who: called}); err != nil {
			return reachLoaded{who: who, err: err}
		}
		reach, err := back.Reachable(ctx, who)
		return reachLoaded{who: who, reach: reach, err: err}
	}
}

// machineOr is a machine as a sentence says it.
func machineOr(machine string) string {
	if machine == "" {
		return "this machine"
	}
	return machine
}

// ---------------------------------------------------------------- the access screen

// Row kinds on the admin screens, which is what enter and the letters do to them.
const (
	actStep    = "step"
	actShown   = "shown"
	actWho     = "who"
	actAsking  = "asking"
	actTrust   = "trust"
	actOpen    = "open"
	actRename  = "rename"
	actForget  = "forget"
	actNothing = ""
)

// accessOf arranges one path's detail: the step it stands on, whether others see it, who asked,
// and everybody who could be let in or kept out by name.
func accessOf(d PathDetail) []list.Item {
	var items []list.Item

	items = append(items, dividerItem{label: "who may open it"})
	for _, s := range ladder {
		items = append(items, manageItem{
			what: "step", label: s.title, note: s.says, act: actStep, level: s.level,
			on: d.Level == s.level,
		})
	}
	if d.Level == "custom" {
		items = append(items, manageItem{what: "step", label: "Custom", note: "its config names who, in its own way", act: actNothing, on: true})
	}
	if d.Chosen {
		items = append(items, manageItem{
			what: "step", label: "As its config says", note: "hand it back to " + stepTitle(d.Config) + ", the step the config puts it on",
			act: actStep, level: "",
		})
	}

	shown := "hidden from everybody else"
	if d.Shown {
		shown = "those who may not open it see it is there, and may ask"
	}
	items = append(items, manageItem{what: "shown", label: "Others may see it and ask", note: shown, act: actShown, on: d.Shown})

	if len(d.Asking) > 0 {
		items = append(items, dividerItem{label: "asking to be let in"})
		for _, a := range d.Asking {
			note := "asked " + a.When
			if a.Why != "" {
				note = a.Why + " · " + note
			}
			items = append(items, manageItem{what: "asking", label: a.Who, note: note + " — a lets them in, x keeps them out", act: actAsking, who: a.Who})
		}
	}

	if len(d.Who) > 0 {
		items = append(items, dividerItem{label: "by name"})
		for _, w := range d.Who {
			note := "left to the step"
			on, off := false, false
			switch w.At {
			case "allowed":
				note, on = "let in by name", true
			case "refused":
				note, off = "kept out, whatever the step says", true
			}
			if w.InConfig {
				note += " · the config names them"
			}
			items = append(items, manageItem{what: "who", label: w.Name, note: note, act: actWho, who: w.Name, on: on, off: off})
		}
	}
	return items
}

// showAccess puts one path's detail in the list, keeping the cursor where it was.
func (m *Model) showAccess() {
	at := m.list.Index()
	m.fill("access\x00"+m.onMachine+"\x00"+m.detail.Path, accessOf(m.detail))
	m.list.Select(at)
	m.offHeading()
	m.list.SetSize(m.listWidth(), m.listHeight())
}

// offHeading moves the cursor off a heading onto the row under it: a heading is nothing to act on.
func (m *Model) offHeading() {
	items := m.list.Items()
	for at := m.list.Index(); at < len(items); at++ {
		if _, heading := items[at].(dividerItem); !heading {
			m.list.Select(at)
			return
		}
	}
}

// ---------------------------------------------------------------- what somebody may open

// reachRows is everything one person may open, machine by machine.
func reachRows(reach []Reachable) []list.Item {
	var items []list.Item
	for _, r := range reach {
		items = append(items, dividerItem{label: "on " + machineOr(r.Machine)})
		switch {
		case r.Err != "":
			items = append(items, manageItem{what: "reach", label: "could not ask", note: r.Err})
			continue
		case !r.Known:
			items = append(items, manageItem{what: "reach", label: "never met them", note: "it does not know them, so only what is Public opens for them there"})
		}
		for _, p := range r.Paths {
			note := stepTitle(p.Level)
			switch p.At {
			case "allowed":
				note = "let in by name"
			case "refused":
				note = "kept out by name"
			}
			if p.Opens {
				note = "opens · " + note
			} else {
				note = "shut · " + note
			}
			items = append(items, manageItem{
				what: "reach", label: p.Path, note: note, path: p.Path, machine: r.Machine, called: r.Called,
				act: actOpen, on: p.Opens, off: !p.Opens && p.At == "refused",
			})
		}
	}
	return items
}

// ---------------------------------------------------------------- prompts

// prompting is one line being typed for something: a name, a code.
type prompting struct {
	title, says string
	text        string
	done        func(string) tea.Cmd
}

// confirming is a question waiting for a yes.
type confirming struct {
	ask string
	yes tea.Cmd
}

// promptKey takes a line a character at a time, a paste arriving as one run of runes.
func (m Model) promptKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc, tea.KeyCtrlC:
		m.prompt = nil
		return m, nil
	case tea.KeyEnter:
		text := strings.TrimSpace(m.prompt.text)
		if text == "" {
			return m, nil
		}
		done := m.prompt.done
		m.prompt, m.trouble, m.loading = nil, "", true
		return m, done(text)
	case tea.KeyBackspace:
		if n := len(m.prompt.text); n > 0 {
			m.prompt.text = m.prompt.text[:n-1]
		}
	case tea.KeyRunes:
		m.prompt.text += string(msg.Runes)
	case tea.KeySpace:
		m.prompt.text += " "
	}
	return m, nil
}

// promptView is the line being typed, in a panel of its own.
func (m Model) promptView() string {
	var out strings.Builder
	out.WriteString(dimStyle.Render(m.prompt.says) + "\n\n")
	if m.prompt.text == "" {
		out.WriteString(faintStyle.Render("…") + "\n")
	}
	for _, at := range fold(m.prompt.text, m.panelWidth()-4) {
		out.WriteString(kindStyle.Render(at) + "\n")
	}
	out.WriteString("\n" + faintStyle.Render("press ") + keyStyle.Render("enter") +
		faintStyle.Render(" to go ahead, ") + keyStyle.Render("esc") + faintStyle.Render(" to go back"))
	return m.middle(panel(m.prompt.title, m.panelWidth(), 0, out.String()))
}

// adminDone says a change to somebody or something was made, and what to read back.
type adminDone struct {
	said string
	// gone says what was changed is not there to go back to.
	gone bool
	err  error
}

// renaming files somebody or a machine under another name.
func renaming(back Backend, old, name string) tea.Cmd {
	return func() tea.Msg {
		if err := back.Rename(old, name); err != nil {
			return adminDone{err: err}
		}
		return adminDone{said: old + " is " + name + " now", gone: true}
	}
}

// removing forgets somebody, or takes a machine out of yours.
func removingIt(back Backend, name, said string) tea.Cmd {
	return func() tea.Msg {
		if err := back.Forget(name); err != nil {
			return adminDone{err: err}
		}
		return adminDone{said: said, gone: true}
	}
}

// joinMachine makes this machine one of the machines of whoever is showing a code.
func joinMachine(back Backend, code string) tea.Cmd {
	return func() tea.Msg {
		ctx, stop := context.WithTimeout(context.Background(), time.Minute)
		defer stop()

		with, err := back.JoinMachine(ctx, code)
		if err != nil {
			return adminDone{err: err}
		}
		return adminDone{said: "this machine is one of yours now, alongside " + with, gone: true}
	}
}

// offerMachine shows a code another machine of yours joins with.
func offerMachine(back Backend) tea.Cmd {
	return func() tea.Msg {
		ctx, stop := context.WithCancel(context.Background())

		ticket, waited, err := back.OfferMachine(ctx)
		if err != nil {
			stop()
			return pairStarted{err: err}
		}
		return pairStarted{at: drawn(&pairing{ticket: ticket, waited: waited, stop: stop, machine: true})}
	}
}
