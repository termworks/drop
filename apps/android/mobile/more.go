package mobile

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/bresilla/drop/src/cmd"
	"github.com/bresilla/drop/src/pkg/convo"
	"github.com/bresilla/drop/src/pkg/tui"
)

// more is what the interface can do beyond what the full-screen one draws.
func (n *Node) more() (cmd.More, error) {
	more, ok := n.back.(cmd.More)
	if !ok {
		return nil, errors.New("this build cannot do that")
	}
	return more, nil
}

type chat struct {
	Machine string `json:"machine"`
	Last    said   `json:"last"`
	// In is when each message from them arrived, newest last, so the app can count what it has
	// not shown yet against when it last looked.
	In []int64 `json:"in"`
}

// mostCounted bounds how many arrivals a conversation reports, which is how many unread it can
// show before it says "many".
const mostCounted = 99

// Conversations is every machine something has been said with, most recent first.
func (n *Node) Conversations() (string, error) {
	peers, err := n.back.Peers()
	if err != nil {
		return "", err
	}

	var out []chat
	for _, p := range peers {
		all, err := n.back.History(p)
		if err != nil || len(all) == 0 {
			continue
		}
		waiting, _ := n.back.Waiting(p)

		last := all[len(all)-1]
		c := chat{Machine: p.Name, Last: said{
			ID: last.ID, Out: last.Dir == convo.Out, Kind: kindOf(last.Kind), Body: last.Body,
			Extra: last.Extra, At: last.When().UnixMilli(), Waiting: waiting[last.ID],
		}}
		for _, m := range all {
			if m.Dir == convo.In && m.Kind != convo.KindEvent {
				c.In = append(c.In, m.When().UnixMilli())
			}
		}
		if len(c.In) > mostCounted {
			c.In = c.In[len(c.In)-mostCounted:]
		}
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Last.At > out[j].Last.At })
	return encode(out), nil
}

type managed struct {
	Name     string   `json:"name"`
	Person   string   `json:"person"`
	ID       string   `json:"id"`
	User     string   `json:"user"`
	Machines int      `json:"machines"`
	Paired   bool     `json:"paired"`
	Trusted  bool     `json:"trusted"`
	Reaching bool     `json:"reaching"`
	Allowed  []string `json:"allowed"`
	Refused  []string `json:"refused"`
}

// Managed is everything known about somebody: who they are, whether they are trusted, and which
// of this phone's paths they have been let into or kept out of.
func (n *Node) Managed(name string) (string, error) {
	m, err := n.back.Managed(name)
	if err != nil {
		return "", err
	}
	return encode(managed{
		Name: m.Name, Person: m.Person, ID: m.ID, User: m.User, Machines: m.Machines,
		Paired: m.Paired, Trusted: m.Trusted, Reaching: m.Reaching,
		Allowed: m.Allowed, Refused: m.Refused,
	}), nil
}

type who struct {
	Name     string `json:"name"`
	Person   bool   `json:"person"`
	Machines int    `json:"machines"`
	// At is "allowed", "refused", or empty for somebody the path says nothing about.
	At       string `json:"at"`
	InConfig bool   `json:"config"`
}

type asking struct {
	Who  string `json:"who"`
	Why  string `json:"why"`
	When string `json:"when"`
}

type rule struct {
	Path     string   `json:"path"`
	Anyone   bool     `json:"anyone"`
	Paired   bool     `json:"paired"`
	Password bool     `json:"password"`
	Who      []who    `json:"who"`
	Asked    []asking `json:"asked"`
}

// Access is who may reach one of this phone's paths, and who has asked to.
func (n *Node) Access(path string) (string, error) {
	r, err := n.back.Access(path)
	if err != nil {
		return "", err
	}
	out := rule{Path: r.Path, Anyone: r.Anyone, Paired: r.Paired, Password: r.Password}
	for _, w := range r.Who {
		at := ""
		switch w.At {
		case tui.Allowed:
			at = "allowed"
		case tui.Refused:
			at = "refused"
		}
		out.Who = append(out.Who, who{Name: w.Name, Person: w.Person, Machines: w.Machines, At: at, InConfig: w.InConfig})
	}
	for _, a := range r.Asked {
		out.Asked = append(out.Asked, asking{Who: a.Who, Why: a.Why, When: a.When})
	}
	return encode(out), nil
}

// Grant lets somebody reach one of this phone's paths, Refuse keeps them out whatever else says,
// and Unset leaves them to whatever the path's own rule says.
func (n *Node) Grant(path, who string) error  { return n.changed(n.back.Grant(path, who)) }
func (n *Node) Refuse(path, who string) error { return n.changed(n.back.Refuse(path, who)) }
func (n *Node) Unset(path, who string) error  { return n.changed(n.back.Unset(path, who)) }

func (n *Node) changed(err error) error {
	changed(n.events)
	return err
}

type knock struct {
	ID    string `json:"id"`
	Brief string `json:"brief"`
	At    int64  `json:"at"`
	Asked string `json:"asked"`
	Why   string `json:"why"`
}

// Knocked is every device that dialled this phone and was turned away, most recent first.
func (n *Node) Knocked() (string, error) {
	all, err := n.back.Knocked()
	if err != nil {
		return "", err
	}
	out := make([]knock, 0, len(all))
	for _, k := range all {
		out = append(out, knock{ID: k.ID, Brief: brief(k.ID), At: k.At.UnixMilli(), Asked: k.Asked, Why: k.Why})
	}
	return encode(out), nil
}

// AskFor rings the bell on a path that can be seen and not opened. Nothing is granted by it: the
// request is written down over there, for somebody there to answer.
func (n *Node) AskFor(name, path, why string) error {
	on, err := entry(name)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(n.ctx, reachWithin)
	defer cancel()
	return n.back.AskFor(ctx, on, path, why)
}

// Remove deletes one thing from a files namespace on a machine.
func (n *Node) Remove(name, path, dir, file string) error {
	on, err := entry(name)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(n.ctx, reachWithin)
	defer cancel()
	return n.changed(n.back.Remove(ctx, on, path, dir, file))
}

// Mkdir makes a directory in a files namespace on a machine.
func (n *Node) Mkdir(name, path, dir, called string) error {
	more, err := n.more()
	if err != nil {
		return err
	}
	on, err := entry(name)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(n.ctx, reachWithin)
	defer cancel()
	return n.changed(more.Mkdir(ctx, on, path, dir, called))
}

// Move renames something in a files namespace on a machine.
func (n *Node) Move(name, path, dir, from, to string) error {
	more, err := n.more()
	if err != nil {
		return err
	}
	on, err := entry(name)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(n.ctx, reachWithin)
	defer cancel()
	return n.changed(more.Move(ctx, on, path, dir, from, to))
}

// Note is a shared note as the machine has it.
func (n *Node) Note(name, path string) (string, error) {
	more, err := n.more()
	if err != nil {
		return "", err
	}
	on, err := entry(name)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(n.ctx, reachWithin)
	defer cancel()
	body, err := more.Note(ctx, on, path)
	return string(body), err
}

// Share decides what this phone serves, by writing the config it starts from. The three it always
// serves — somewhere to send to, to talk, and to hand a link — and, when folder is set, the folder
// where what arrives lands, for paired devices to walk and take from, and to put into when writable.
//
// It takes effect the next time the node starts.
func Share(configDir, downloads string, folder, writable bool) error {
	file := filepath.Join(configDir, "drop", "init.lua")
	if !folder {
		err := os.Remove(file)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		return err
	}

	arrived := filepath.Join(downloads, "drop")
	if strings.Contains(arrived, "]==]") {
		return fmt.Errorf("%s cannot be written into a config", arrived)
	}
	if err := os.MkdirAll(arrived, 0o700); err != nil {
		return err
	}

	lua := fmt.Sprintf(`-- Written by the drop app, from what its settings say this phone shares. Changed there, not here.
local drop = require("drop")

drop.mount("/inbox", { type = "share", dir = [==[%s]==], access = "paired" })
drop.mount("/chat", { type = "chat", access = "paired" })
drop.mount("/open", { type = "link", access = "paired" })
drop.mount("/phone", { type = "files", dir = [==[%s]==], writable = %t, access = "paired" })
`, arrived, arrived, writable)

	staging := file + ".new"
	if err := os.WriteFile(staging, []byte(lua), 0o600); err != nil {
		return err
	}
	return os.Rename(staging, file)
}
