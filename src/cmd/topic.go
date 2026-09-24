package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/bresilla/drop/src/pkg/arch"
	"github.com/bresilla/drop/src/pkg/conf"
	"github.com/bresilla/drop/src/pkg/convo"
	"github.com/bresilla/drop/src/pkg/made"
	"github.com/bresilla/drop/src/pkg/ns"
	"github.com/bresilla/drop/src/pkg/proto"
)

// Topics: what a machine offers — a chat, a folder, an inbox, a note, a terminal — added and taken
// away from any machine of its user's.
//
// A person picks a kind and a name, and that is all. Where a folder lives or which shell a terminal
// runs is worked out on the machine the topic goes on, because that is where the home directory is,
// and a phone adding a folder to a laptop has no business knowing the laptop's paths.

// topicHome is where the folders and files topics keep live on this machine.
func topicHome() string {
	if home, err := os.UserHomeDir(); err == nil && home != "" && home != "/" {
		return filepath.Join(home, "drop")
	}
	if dir, err := convo.DataDir(); err == nil {
		return filepath.Join(dir, "topics")
	}
	return "drop"
}

// topicEntry is a topic of one kind, as this machine writes it down: the folder or file it keeps
// worked out here, and made so the topic opens onto something.
func topicEntry(at, kind, command, level string) (made.Entry, error) {
	k, ok := made.KindNamed(kind)
	if !ok {
		return made.Entry{}, fmt.Errorf("%q is no kind of topic: it is one of %s", kind, kindList())
	}
	name := strings.Trim(at, "/")
	settings := made.Settings{}
	switch k.Name {
	case "inbox":
		settings["dir"] = filepath.Join(topicHome(), name)
	case "folder":
		settings["dir"], settings["writable"] = filepath.Join(topicHome(), name), true
	case "note":
		settings["file"] = filepath.Join(topicHome(), name+".md")
	case "terminal":
		settings["input"], settings["private"] = true, true
	case "links":
		settings["action"] = linkOpener()
	case "stream":
		if strings.TrimSpace(command) == "" {
			return made.Entry{}, errors.New("a stream needs the command it shows")
		}
		settings["command"] = command
	}
	if dir, ok := settings["dir"].(string); ok {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return made.Entry{}, err
		}
	}
	if file, ok := settings["file"].(string); ok {
		if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
			return made.Entry{}, err
		}
		if f, err := os.OpenFile(file, os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
			_ = f.Close()
		}
	}
	access, err := admitting(level, "")
	if err != nil {
		return made.Entry{}, err
	}
	return made.Entry{Archetype: k.Archetype, Settings: settings, Access: access}, nil
}

// linkOpener is what opens a link on this machine.
func linkOpener() string {
	if runtime.GOOS == "darwin" {
		return "open"
	}
	return "xdg-open"
}

func kindList() string {
	names := make([]string, 0, len(made.Kinds))
	for _, k := range made.Kinds {
		names = append(names, k.Name)
	}
	return strings.Join(names, ", ")
}

// putUp writes a topic down and serves it at once: with the mounts this process holds, or through
// the daemon when that is what serves this machine.
func putUp(ctx context.Context, known *arch.Registry, put *mountHost, at string, entry made.Entry) error {
	at, err := ns.Clean(at)
	if err != nil {
		return err
	}
	answers, ok := known.Lookup(entry.Archetype, entry.Version)
	if !ok {
		return known.Missing(entry.Archetype, entry.Version)
	}
	if _, err := answers.Read(made.Declared(entry.Settings)); err != nil {
		return fmt.Errorf("%s: %w", at, err)
	}
	cfg, err := conf.Load(known)
	if err != nil {
		return err
	}
	declared := declares(cfg, at)
	cfg.Close()
	if declared {
		return fmt.Errorf("%s is already a topic here", at)
	}
	store, err := made.Load()
	if err != nil {
		return err
	}
	if _, held := store.Get(at); held {
		return fmt.Errorf("%s is already a topic here", at)
	}
	if err := store.Add(at, entry); err != nil {
		return err
	}
	line := made.Line{Path: at, Keep: true, Entry: entry}
	if put != nil {
		return put.begin(line)
	}
	conn, err := asking(ctx)
	if errors.Is(err, errNoNode) {
		return nil
	}
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	_, err = tell(conn, line)
	return err
}

// takeDown takes a topic that was added off the list and down. One the config declares stays: it
// is somebody's own file, and changes there.
func takeDown(ctx context.Context, known *arch.Registry, put *mountHost, at string) error {
	at, err := ns.Clean(at)
	if err != nil {
		return err
	}
	cfg, err := conf.Load(known)
	if err != nil {
		return err
	}
	declared, file, defaults := declares(cfg, at), where(cfg), cfg.Defaults
	cfg.Close()
	if declared && defaults {
		return fmt.Errorf("%s is one drop keeps on every machine, and stays: `drop topic who %s me` keeps it to you", at, strings.TrimPrefix(at, "/"))
	}
	if declared {
		return fmt.Errorf("%s is written in %s, so it is removed there", at, file)
	}
	store, err := made.Load()
	if err != nil {
		return err
	}
	had, err := store.Remove(at)
	if err != nil {
		return err
	}
	if !had {
		return fmt.Errorf("%s is not a topic here", at)
	}
	if put != nil {
		put.removeWritten(at)
		return nil
	}
	if err := unmounted(ctx, at); err != nil && !errors.Is(err, errNoNode) {
		return err
	}
	return nil
}

// manageTopic adds or removes a topic on this machine, asked by another machine of its user's or by
// an interface here, and answers with what the path now is.
func manageTopic(ctx context.Context, known *arch.Registry, put *mountHost, m proto.Manage) ([]byte, error) {
	if m.Op == proto.ManageRemove {
		if err := takeDown(ctx, known, put, m.Path); err != nil {
			return nil, err
		}
		return json.Marshal(map[string]string{"removed": m.Path})
	}
	var asked proto.TopicBody
	if err := json.Unmarshal([]byte(m.Body), &asked); err != nil {
		return nil, fmt.Errorf("what topic to add is unreadable: %w", err)
	}
	entry, err := topicEntry(m.Path, asked.Kind, asked.Command, m.Level)
	if err != nil {
		return nil, err
	}
	if err := putUp(ctx, known, put, m.Path, entry); err != nil {
		return nil, err
	}
	detail, err := pathDetail(known, m.Path)
	if err != nil {
		// Up and written down, whatever the detail says: a machine that has it in its file serves it.
		return json.Marshal(map[string]string{"added": m.Path})
	}
	return json.Marshal(detail)
}
