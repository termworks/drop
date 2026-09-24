package tui

import (
	"context"
	"time"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
)

// Devices on this network, devices asking this one to connect, and changing what a device you
// added is to you.
//
// Adding is the one way in, for everybody: a device added is paired, whoever owns it. What it is to
// you comes after — one of your machines, or this one one of theirs — and is asked of the device,
// whose person says yes with the same number on both screens.

// What asking a device asks for, the same words the node uses.
const (
	askMine = "mine"
	askPair = "pair"
	askJoin = "join"
)

// actNear is a row standing for a device on this network.
const actNear = "near"

// nearRows are the devices on this network nobody here has connected with yet.
func nearRows(near []Near) []list.Item {
	if len(near) == 0 {
		return nil
	}
	items := []list.Item{dividerItem{label: "nearby"}}
	for _, n := range near {
		items = append(items, manageItem{
			what: "near", label: n.Name, note: "on this network — enter adds it, o makes it one of your machines",
			act: actNear, who: n.ID,
		})
	}
	return items
}

// polled is the slow look at what is nearby and who is asking, for as long as the interface is open.
type polled struct {
	near     []Near
	asked    []Invited
	renewing int
}

const pollEvery = 2 * time.Second

func poll(back Backend) tea.Cmd {
	return tea.Tick(pollEvery, func(time.Time) tea.Msg {
		near, _ := back.Nearby()
		asked, _ := back.Invited()
		return polled{near: near, asked: asked, renewing: back.Renewing()}
	})
}

// renewed is fresh badges signed for machines of yours, and how many.
type renewed struct {
	n   int
	err error
}

func renew(back Backend) tea.Cmd {
	return func() tea.Msg {
		ctx, stop := context.WithTimeout(context.Background(), 10*time.Minute)
		defer stop()
		n, err := back.Renew(ctx)
		return renewed{n: n, err: err}
	}
}

// invitedDone says a device asked came, or did not.
type invitedDone struct {
	with string
	kind string
	err  error
}

// inviteTo asks a device to connect, and waits for its person.
func inviteTo(back Backend, id, kind string) tea.Cmd {
	return func() tea.Msg {
		ctx, stop := context.WithTimeout(context.Background(), 4*time.Minute)
		defer stop()
		with, err := back.Invite(ctx, id, kind)
		return invitedDone{with: with, kind: kind, err: err}
	}
}

// decided is an answer given to a device asking.
type decided struct{ err error }

func decide(back Backend, id string, yes bool) tea.Cmd {
	return func() tea.Msg { return decided{err: back.Decide(id, yes)} }
}

// askedSays is what an ask asks of this machine, as the question ends.
func askedSays(kind string) string {
	switch kind {
	case askMine:
		return "to make this machine one of theirs"
	case askJoin:
		return "to become one of your machines"
	}
	return "to be added"
}

// askedLine is the question a device asking puts to whoever is looking.
func (m Model) askedLine() string {
	if len(m.asked) == 0 {
		return ""
	}
	a := m.asked[0]
	who := a.Name
	if a.Whose != "" && a.Whose != "you" {
		who += " (" + a.Whose + "'s)"
	}
	return who + " asks " + askedSays(a.Kind) + " — check " + a.Check + " is on its screen too — y yes, n no"
}

// askHasKeys is whether a device's ask has the keyboard: it does whenever one is waiting and nothing
// else is being typed.
func (m Model) askHasKeys() bool {
	return len(m.asked) > 0 && m.prompt == nil && m.confirm == nil && !m.writing && !m.joining && !m.putting && !m.atKeyboard
}
