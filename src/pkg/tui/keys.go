package tui

import (
	"os"
	"os/exec"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// Choosing your key: the SSH keys you already have, the one in your YubiKey, or a file somewhere
// else, picked from a list rather than typed.

// keyFetched says the YubiKey's keys were taken, or why not.
type keyFetched struct{ err error }

// keyMenu offers every key this user could be.
func (m Model) keyMenu() Model {
	var items []hint
	var ids []string
	for _, k := range m.back.Keys() {
		if k.Current {
			continue
		}
		does := shortPath(k.Path) + "  " + briefPrint(k.Print)
		if k.Note != "" {
			does += " — " + k.Note
		}
		items, ids = append(items, hint{k.Kind, does}), append(ids, "path:"+k.Path)
	}
	items = append(items,
		hint{"YubiKey", "take the key from your YubiKey: its PIN, and a touch"},
		hint{"YubiKey", "make a new key for drop on your YubiKey"},
		hint{"a file", "an SSH key somewhere else: type where"},
	)
	ids = append(ids, "yubikey", "yubikey-new", "file")

	m.menu = &menuState{
		title: "who you are: " + briefPrint(firstWord(m.me.Key)),
		items: items,
		ids:   ids,
		pick: func(id string) tea.Cmd {
			return func() tea.Msg { return keyPicked{id: id} }
		},
	}
	return m
}

// keyPicked is a choice made in the key menu.
type keyPicked struct{ id string }

// pickKey goes on with a key picked: asked about first, since it is a new you.
func (m Model) pickKey(id string) (Model, tea.Cmd) {
	switch {
	case strings.HasPrefix(id, "path:"):
		at := strings.TrimPrefix(id, "path:")
		m.confirm = &confirming{
			ask: "be " + shortPath(at) + "? every machine of yours that holds it finds this one by itself",
			yes: usingKey(m.back, at),
		}
		return m, nil
	case id == "yubikey" || id == "yubikey-new":
		self, err := os.Executable()
		if err != nil {
			m.trouble = err.Error()
			return m, nil
		}
		args := []string{"me", "key", "yubikey"}
		if id == "yubikey-new" {
			args = append(args, "--new")
		}
		return m, tea.ExecProcess(exec.Command(self, args...), func(err error) tea.Msg { return keyFetched{err: err} })
	case id == "file":
		m.prompt = &prompting{
			title: "use your own key",
			says:  "The file: an SSH key like ~/.ssh/id_ed25519, or the .pub of a key in a YubiKey.",
			done:  func(at string) tea.Cmd { return usingKey(m.back, at) },
		}
	}
	return m, nil
}

// rekeying takes up the key another process chose.
func rekeying(back Backend) tea.Cmd {
	return func() tea.Msg {
		if err := back.Rekey(); err != nil {
			return adminDone{err: err}
		}
		return adminDone{said: "you are the key in your YubiKey now — your machines holding it find this one by themselves"}
	}
}

// shortPath is a path with the home directory written as ~.
func shortPath(at string) string {
	if home, err := os.UserHomeDir(); err == nil && strings.HasPrefix(at, home+"/") {
		return "~" + strings.TrimPrefix(at, home)
	}
	return at
}

func firstWord(s string) string {
	word, _, _ := strings.Cut(strings.TrimSpace(s), " ")
	return word
}

// briefPrint is the start of a fingerprint, which is what a person compares.
func briefPrint(print string) string {
	if len(print) > 19 {
		return print[:19] + "…"
	}
	return print
}
