package tui

import (
	"context"
	"encoding/json"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/bresilla/drop/src/pkg/made"
	"github.com/bresilla/drop/src/pkg/ns"
	"github.com/bresilla/drop/src/pkg/proto"
)

// The topics on a machine of yours: added with a name and a kind, taken away, and opened up to
// people, from the same screen that lists them.

// topicNamed is a name typed for a new topic, waiting for its kind.
type topicNamed struct{ machine, name string }

// topicKind is a kind picked for a new topic.
type topicKind struct {
	machine, name string
	kind          made.Kind
}

// topicDone says a topic was added or taken away, and on which machine.
type topicDone struct {
	said string
	err  error
}

// topicsKey is what a key does on the topics of a machine of yours. Anybody else's are theirs to
// change, so every key is left to do what it does everywhere else.
func (m Model) topicsKey(key string) (tea.Model, tea.Cmd, bool) {
	if !m.mineOpen() {
		return m, nil, false
	}
	row, onTopic := m.list.SelectedItem().(pathItem)
	onTopic = onTopic && row.step.is

	switch key {
	case "w":
		return m.openAccess()
	case "a":
		return m.topicPrompt(), nil, true
	case "x":
		if !onTopic {
			return m, nil, true
		}
		at, machine := row.step.served.Path, m.topicMachine()
		m.confirm = &confirming{
			ask: "remove " + at + " from " + machineOr(machine) + "?",
			yes: topicChange(m.back, machine, proto.Manage{Op: proto.ManageRemove, Path: at}, at+" is gone from "+machineOr(machine)),
		}
		return m, nil, true
	}
	return m, nil, false
}

// topicMachine is which machine of yours the topics on screen are on, empty for this one.
func (m Model) topicMachine() string {
	if m.onSelf {
		return ""
	}
	with, _ := m.peer()
	return with.Name
}

// topicPrompt asks what a new topic is called.
func (m Model) topicPrompt() Model {
	machine := m.topicMachine()
	m.prompt = &prompting{
		title: "add a topic on " + machineOr(machine),
		says:  "A name for it, like work or photos. What kind of topic it is comes next.",
		done: func(name string) tea.Cmd {
			return func() tea.Msg { return topicNamed{machine: machine, name: strings.Trim(name, "/ ")} }
		},
	}
	return m
}

// kindMenu offers every kind a topic can be, and picking one goes on with the topic being added.
func (m Model) kindMenu(named topicNamed) Model {
	items := make([]hint, 0, len(made.Kinds))
	for _, k := range made.Kinds {
		items = append(items, hint{k.Name, k.About})
	}
	m.menu = &menuState{
		title: "what is " + named.name + "?",
		items: items,
		pick: func(picked string) tea.Cmd {
			kind, _ := made.KindNamed(picked)
			return func() tea.Msg { return topicKind{machine: named.machine, name: named.name, kind: kind} }
		},
	}
	return m
}

// addTopic adds the topic once its kind is known, asking first for the command a stream shows.
func (m Model) addTopic(picked topicKind) (Model, tea.Cmd) {
	add := func(command string) tea.Cmd {
		body, _ := json.Marshal(proto.TopicBody{Kind: picked.kind.Name, Command: command})
		at := "/" + picked.name
		return topicChange(m.back, picked.machine,
			proto.Manage{Op: proto.ManageAdd, Path: at, Level: ns.LevelMe, Body: string(body)},
			at+" is a "+picked.kind.Name+" on "+machineOr(picked.machine)+" — w says who may open it")
	}
	if picked.kind.Command {
		m.prompt = &prompting{
			title: "the command /" + picked.name + " shows",
			says:  "Run on " + machineOr(picked.machine) + ", and what it prints is the stream.",
			done:  add,
		}
		return m, nil
	}
	m.loading, m.trouble = true, ""
	return m, add("")
}

// topicChange adds or takes away a topic on a machine of yours.
func topicChange(back Backend, machine string, change proto.Manage, said string) tea.Cmd {
	return func() tea.Msg {
		ctx, stop := context.WithTimeout(context.Background(), manageWithin)
		defer stop()

		if _, err := back.Ask(ctx, machine, change); err != nil {
			return topicDone{err: err}
		}
		return topicDone{said: said}
	}
}

// reloadTopics asks again what the machine on screen has.
func (m Model) reloadTopics() tea.Cmd {
	if m.onSelf {
		return loadMine(m.back)
	}
	if with, ok := m.peer(); ok {
		return loadPaths(m.back, with)
	}
	return nil
}
