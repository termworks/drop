package tui

import (
	"context"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/bresilla/drop/src/pkg/book"
)

// Rule is who may reach one of this machine's own paths.
//
// Two sources, kept apart the way they are kept apart on disk: what the config says, which is
// structure somebody wrote, and what has been granted here, which is data drop owns. The interface
// shows them together because that is how a caller is judged, and edits only the second.
type Rule struct {
	Path string
	// Anyone admits whoever knows this device's id, and Paired admits the address book. Both come
	// from the config and are shown so that the list is not read as the whole story.
	Anyone bool
	Paired bool
	// Password says a secret guards this path.
	Password bool
	// Who is everybody the list can say something about: named in the config, granted here, or
	// simply in the address book and therefore somebody you might want to let in.
	Who []Who
	// Seen is who may know this path exists without being able to open it.
	Seen bool
	// Asked is who has rung the bell on it and is waiting for an answer.
	Asked []Wanting
}

// Wanting is one request to be let into a path.
type Wanting struct {
	// Who is the name a grant would be written against: a person if one is known, else the device.
	Who string
	// Why is what they said about it, and may be empty.
	Why  string
	When string
}

// Standing is how somebody stands with a path.
type Standing int

const (
	// NotNamed is somebody the path says nothing about. Whether they get in is decided by the
	// wider rules, if there are any.
	NotNamed Standing = iota
	// Allowed is somebody named, in the config or by a grant made here.
	Allowed
	// Refused is somebody on the refusal list, which beats everything else.
	Refused
)

// Who is one person or machine, and how they stand with a path.
type Who struct {
	// Name is how a rule spells them: "bob", "bob@laptop", or an endpoint id.
	Name string
	// Person marks somebody rather than a machine.
	Person bool
	// Machines is how many machines they have, for a person.
	Machines int
	At       Standing
	// InConfig marks somebody the config names, whom the interface cannot un-name -- only refuse.
	InConfig bool
}

// rang says a request was sent, or says why it was not.
type rang struct {
	path string
	err  error
}

// askFor rings the bell on a path that is visible but not open.
func ringFor(back Backend, on book.Entry, path string) tea.Cmd {
	return func() tea.Msg {
		ctx, stop := context.WithTimeout(context.Background(), askWithin)
		defer stop()

		return rang{path: path, err: back.AskFor(ctx, on, path, "")}
	}
}

// askWithin bounds one request, which is a dial and a sentence.
const askWithin = 90 * time.Second
