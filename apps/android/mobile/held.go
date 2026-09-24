package mobile

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/bresilla/drop/src/pkg/made"
	"github.com/bresilla/drop/src/pkg/weave"
)

// holdWithin is how long taking something up is given, catching up on it included.
const holdWithin = 2 * reachWithin

// Hold keeps a copy of a note or a folder another machine holds on this phone, kept level with
// theirs from then on, and says the path it is held at here.
func (n *Node) Hold(name, path, archetype string) (string, error) {
	more, err := n.more()
	if err != nil {
		return "", err
	}
	on, err := entry(name)
	if err != nil {
		return "", err
	}
	settings, err := n.keeping(path, archetype)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(n.ctx, holdWithin)
	defer cancel()

	at, err := more.Hold(ctx, on, path, settings)
	changed(n.events)
	return at, err
}

// keeping is where this phone keeps a copy of something: a note among the app's own files, and a
// folder where a file manager finds it — beside the folder this phone shares, and never inside it,
// so what one person shares with it is not handed on to everybody the phone shares with.
func (n *Node) keeping(path, archetype string) (made.Settings, error) {
	called := strings.ReplaceAll(strings.Trim(path, "/"), "/", "-")
	if called == "" {
		return nil, fmt.Errorf("%q is not a namespace", path)
	}

	var dir string
	switch archetype {
	case "note":
		dir = filepath.Join(n.data, "notes")
	case "files":
		if n.downloads == "" {
			return nil, errors.New("this phone has nowhere to keep a folder")
		}
		dir = filepath.Join(n.downloads, "drop-kept", called)
	default:
		return nil, fmt.Errorf("a %s is not something a phone keeps a copy of", archetype)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	if archetype == "note" {
		return made.Settings{"file": filepath.Join(dir, called+".md")}, nil
	}
	return made.Settings{"dir": dir}, nil
}

// Release stops keeping a copy. What is on the phone stays where it is.
func (n *Node) Release(path string) error {
	more, err := n.more()
	if err != nil {
		return err
	}
	return n.changed(more.Release(path))
}

type kept struct {
	Path      string `json:"path"`
	Archetype string `json:"archetype"`
	// Shared is what every machine holding it calls it, which is how a path somebody else serves
	// is known to be this one.
	Shared string `json:"shared"`
	// Where is the file or directory it is kept in.
	Where string `json:"where"`
}

// Kept is every copy this phone keeps.
func (n *Node) Kept() (string, error) {
	more, err := n.more()
	if err != nil {
		return "", err
	}
	all, err := more.Kept()
	if err != nil {
		return "", err
	}

	out := make([]kept, 0, len(all))
	for at, e := range all {
		if !e.Shared.Declared() {
			continue
		}
		where, _ := e.Settings["file"].(string)
		if where == "" {
			where, _ = e.Settings["dir"].(string)
		}
		out = append(out, kept{Path: at, Archetype: e.Archetype, Shared: e.Shared.ID(), Where: where})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return encode(out), nil
}

// noteFile is the file a note kept here is written in.
func (n *Node) noteFile(path string) (string, error) {
	more, err := n.more()
	if err != nil {
		return "", err
	}
	all, err := more.Kept()
	if err != nil {
		return "", err
	}
	e, ok := all[path]
	if !ok || e.Archetype != "note" {
		return "", fmt.Errorf("%s is not a note kept here", path)
	}
	file, _ := e.Settings["file"].(string)
	if file == "" {
		return "", fmt.Errorf("%s has no file", path)
	}
	return file, nil
}

// Written is a note kept here, as it stands.
func (n *Node) Written(path string) (string, error) {
	file, err := n.noteFile(path)
	if err != nil {
		return "", err
	}
	raw, err := os.ReadFile(file)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	return string(raw), err
}

// Write saves what was typed into a note kept here, and says what the note now is.
//
// Typed against base, the note as it was when the typing started. Whatever came in since is in the
// file already, so what is written is the two merged against base — what was typed and what
// arrived both survive, and the note takes it from there the same as any other save.
func (n *Node) Write(path, base, typed string) (string, error) {
	file, err := n.noteFile(path)
	if err != nil {
		return "", err
	}
	now, err := os.ReadFile(file)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}

	body, aside := weave.Bytes([]byte(base), []byte(typed), now, "this phone", "elsewhere")
	if len(aside) > 0 {
		body = []byte(typed)
	}

	next := file + ".typed"
	if err := os.WriteFile(next, body, 0o600); err != nil {
		return "", err
	}
	if err := os.Rename(next, file); err != nil {
		_ = os.Remove(next)
		return "", err
	}
	return string(body), nil
}
