// Package shares remembers what each device said it shares.
//
// Asking a device costs a round trip over somebody else's network, and a device that is off cannot
// be asked at all. Without a memory of the answer, a conversation sitting on this disk has no way
// in: the list of paths comes from the far end, and with nothing to show there is nothing to enter.
// Reading and queueing to an absent device is the whole point of having a queue.
//
// What is kept here is a convenience and never a decision. A path in this file is not permission to
// reach it: the far end decides that on every connection, and a stale entry only means a request
// that comes back refused.
package shares

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/bresilla/drop/src/pkg/convo"
	"github.com/bresilla/drop/src/pkg/node"
	"github.com/bresilla/drop/src/pkg/ns"
	"github.com/bresilla/drop/src/pkg/proto"
)

// stored is one path, as it is written down. The wire form is not reused: a file that has to be
// migrated whenever a protocol field moves is a file that breaks on an upgrade.
type stored struct {
	Path     string `json:"path"`
	Kind     string `json:"kind"`
	Writable bool   `json:"writable,omitempty"`
}

func at(peer node.ID) (string, error) {
	base, err := convo.DataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "shares", peer.String()+".json"), nil
}

// Remember writes down what a device answered.
func Remember(peer node.ID, what []proto.Served) error {
	file, err := at(peer)
	if err != nil {
		return err
	}

	out := make([]stored, 0, len(what))
	for _, s := range what {
		out = append(out, stored{Path: s.Path, Kind: s.Kind.String(), Writable: s.Writable})
	}

	raw, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding what %s shares: %w", node.Brief(peer), err)
	}
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		return fmt.Errorf("creating %s: %w", filepath.Dir(file), err)
	}
	if err := os.WriteFile(file, append(raw, '\n'), 0o600); err != nil {
		return fmt.Errorf("writing %s: %w", file, err)
	}
	return nil
}

// Recall is what it last said, and nothing at all for a device that has never answered.
func Recall(peer node.ID) ([]proto.Served, error) {
	file, err := at(peer)
	if err != nil {
		return nil, err
	}

	raw, err := os.ReadFile(file)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", file, err)
	}

	var onDisk []stored
	if err := json.Unmarshal(raw, &onDisk); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", file, err)
	}

	out := make([]proto.Served, 0, len(onDisk))
	for _, s := range onDisk {
		kind, err := ns.ParseKind(s.Kind)
		if err != nil {
			// A kind this version does not know is a path it could not open anyway. Skipping it
			// beats refusing to read the rest of what a device shares.
			continue
		}
		out = append(out, proto.Served{Path: s.Path, Kind: kind, Writable: s.Writable})
	}
	return out, nil
}

// Forget drops what a device said, for a device that is no longer in the address book.
func Forget(peer node.ID) error {
	file, err := at(peer)
	if err != nil {
		return err
	}
	if err := os.Remove(file); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("removing %s: %w", file, err)
	}
	return nil
}
