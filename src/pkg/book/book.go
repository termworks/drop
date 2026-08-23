// Package book is the local address book: peers this machine has paired with.
package book

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/bresilla/drop/src/pkg/node"
)

// SecretBytes is the width of the shared secret two paired peers derive.
const SecretBytes = 32

// Entry is one known peer. Secret is empty for a peer that was pinned by id rather than paired;
// such a peer can be reached on the local network, but not looked up privately.
type Entry struct {
	Name   string
	ID     node.ID
	Secret []byte
	// Addrs is where this peer was last known to be, learned at pairing.
	Addrs []string
}

// Paired reports whether this entry carries a shared secret.
func (e Entry) Paired() bool {
	return len(e.Secret) == SecretBytes
}

// Book maps local names to peers. Names are this machine's own labels; they are never published and
// never trusted from the network.
type Book struct {
	entries map[string]Entry
}

// stored is the on-disk shape.
type stored struct {
	ID     string   `json:"id"`
	Secret string   `json:"secret,omitempty"`
	Addrs  []string `json:"addrs,omitempty"`
}

func path() (string, error) {
	dir, err := node.ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "peers.json"), nil
}

// Load reads the address book, returning an empty one when there is no file yet.
func Load() (*Book, error) {
	b := &Book{entries: map[string]Entry{}}

	file, err := path()
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(file)
	if errors.Is(err, os.ErrNotExist) {
		return b, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", file, err)
	}

	var onDisk map[string]stored
	if err := json.Unmarshal(raw, &onDisk); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", file, err)
	}

	for name, entry := range onDisk {
		id, err := node.ParseID(entry.ID)
		if err != nil {
			return nil, fmt.Errorf("%s: %q is not a peer id: %w", file, entry.ID, err)
		}
		var secret []byte
		if entry.Secret != "" {
			secret, err = base64.StdEncoding.DecodeString(entry.Secret)
			if err != nil {
				return nil, fmt.Errorf("%s: %s has an unreadable secret: %w", file, name, err)
			}
		}
		b.entries[name] = Entry{Name: name, ID: id, Secret: secret, Addrs: entry.Addrs}
	}
	return b, nil
}

// Save writes the address book back. The file holds pairing secrets, so it is written 0600.
func (b *Book) Save() error {
	file, err := path()
	if err != nil {
		return err
	}

	onDisk := make(map[string]stored, len(b.entries))
	for name, entry := range b.entries {
		out := stored{ID: entry.ID.String(), Addrs: entry.Addrs}
		if entry.Paired() {
			out.Secret = base64.StdEncoding.EncodeToString(entry.Secret)
		}
		onDisk[name] = out
	}
	raw, err := json.MarshalIndent(onDisk, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding address book: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		return fmt.Errorf("creating %s: %w", filepath.Dir(file), err)
	}
	if err := os.WriteFile(file, append(raw, '\n'), 0o600); err != nil {
		return fmt.Errorf("writing %s: %w", file, err)
	}
	return nil
}

// Pin records a name for a peer id, without a shared secret.
func (b *Book) Pin(name string, id node.ID) {
	b.entries[name] = Entry{Name: name, ID: id}
}

// Pair records a name, a peer id and the secret the two derived together.
func (b *Book) Pair(name string, id node.ID, secret []byte, addrs ...string) {
	b.entries[name] = Entry{Name: name, ID: id, Secret: secret, Addrs: addrs}
}

// Remove drops a name, reporting whether it was there.
func (b *Book) Remove(name string) bool {
	_, ok := b.entries[name]
	delete(b.entries, name)
	return ok
}

// Lookup resolves a local name.
func (b *Book) Lookup(name string) (Entry, bool) {
	entry, ok := b.entries[name]
	return entry, ok
}

// ByID finds the entry for a peer id.
func (b *Book) ByID(id node.ID) (Entry, bool) {
	for _, entry := range b.entries {
		if entry.ID == id {
			return entry, true
		}
	}
	return Entry{}, false
}

// All lists the book, name order.
func (b *Book) All() []Entry {
	out := make([]Entry, 0, len(b.entries))
	for _, entry := range b.entries {
		out = append(out, entry)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Paired lists only the peers a secret was derived with.
func (b *Book) Paired() []Entry {
	var out []Entry
	for _, entry := range b.All() {
		if entry.Paired() {
			out = append(out, entry)
		}
	}
	return out
}

// Resolve turns a target written on the command line into an entry: a local name if the book has
// one, otherwise the target parsed as a bare peer id.
func Resolve(target string) (Entry, error) {
	b, err := Load()
	if err != nil {
		return Entry{}, err
	}
	if entry, ok := b.Lookup(target); ok {
		return entry, nil
	}

	id, err := node.ParseID(target)
	if err != nil {
		return Entry{}, fmt.Errorf("%q is neither a known name nor a peer id", target)
	}
	if entry, ok := b.ByID(id); ok {
		return entry, nil
	}
	return Entry{Name: target, ID: id}, nil
}
