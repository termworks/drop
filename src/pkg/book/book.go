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
	"sync"
	"time"

	"github.com/bresilla/drop/src/pkg/keep"
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
	// User is the person this machine belongs to, written the way authorized_keys writes a key.
	// Empty for a machine paired on its own, with --machine.
	User string
	// Person is what that person is called here, set whenever User is. A machine has a name and
	// its owner has one, and they are not the same name once somebody has more than one machine.
	Person string
	// Trusted marks somebody you would show things to without thinking about it.
	//
	// Pairing is recognition, not trust: it means a device arrives with a name instead of as a
	// stranger. Trust is the second, deliberate step, and it is what the narrower rules are
	// written against -- a path visible to "trusted" is not visible to somebody you paired with
	// once at a conference.
	Trusted bool
}

// Owned reports whether this entry is somebody's machine, rather than a machine on its own.
func (e Entry) Owned() bool { return e.User != "" }

// Paired reports whether this entry carries a shared secret.
func (e Entry) Paired() bool {
	return len(e.Secret) == SecretBytes
}

// Book maps local names to peers. Names are this machine's own labels; they are never published and
// never trusted from the network.
type Book struct {
	// One lock, because a serving node reads this from every connection it answers while pairing
	// writes to it, and because Refresh replaces the whole map under them.
	mu      sync.RWMutex
	entries map[string]Entry
	// read is when the file this was loaded from was last written, so Refresh can tell whether
	// anything has happened since.
	read time.Time
}

// Refresh re-reads the address book if the file has changed since it was loaded.
//
// A long-running node has to notice a pairing it did not make itself: `drop peer pair` is a separate
// process, and without this a device paired while the daemon was up stays a stranger to it until
// the daemon is restarted — which looks exactly like pairing not working.
func (b *Book) Refresh() error {
	file, err := path()
	if err != nil {
		return err
	}

	at, err := os.Stat(file)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}

	b.mu.RLock()
	known := b.read
	b.mu.RUnlock()

	if !at.ModTime().After(known) {
		return nil
	}

	fresh, err := Load()
	if err != nil {
		return err
	}

	fresh.mu.RLock()
	entries := fresh.entries
	fresh.mu.RUnlock()

	b.mu.Lock()
	b.entries, b.read = entries, at.ModTime()
	b.mu.Unlock()

	return nil
}

// stored is the on-disk shape.
type stored struct {
	ID      string   `json:"id"`
	Secret  string   `json:"secret,omitempty"`
	Addrs   []string `json:"addrs,omitempty"`
	User    string   `json:"user,omitempty"`
	Person  string   `json:"person,omitempty"`
	Trusted bool     `json:"trusted,omitempty"`
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

	// Stamped before parsing, so a write that lands while this is being read is noticed next time
	// rather than being taken for already-read.
	if at, err := os.Stat(file); err == nil {
		b.read = at.ModTime()
	}

	var onDisk map[string]stored
	if err := json.Unmarshal(raw, &onDisk); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", file, err)
	}

	// A line that will not read is dropped and said out loud, not made everybody's problem. One
	// unreadable id used to turn every paired peer in the file into a stranger and stop the node
	// from starting at all, which is a far larger failure than the one that caused it.
	for name, entry := range onDisk {
		id, err := node.ParseID(entry.ID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "drop: %s: %s is not a peer id, skipping %s\n", file, entry.ID, name)
			continue
		}
		var secret []byte
		if entry.Secret != "" {
			secret, err = base64.StdEncoding.DecodeString(entry.Secret)
			if err != nil {
				fmt.Fprintf(os.Stderr, "drop: %s: %s has an unreadable secret, skipping it\n", file, name)
				continue
			}
		}
		b.entries[name] = Entry{Name: name, ID: id, Secret: secret, Addrs: entry.Addrs, User: entry.User, Person: entry.Person, Trusted: entry.Trusted}
	}
	return b, nil
}

// Save writes the address book back. The file holds pairing secrets, so it is written 0600.
//
// Through a scratch file that is synced and then renamed over the old one, with the directory
// synced after, so that a crash or a power cut leaves either book whole. The secrets in here were
// derived once during pairing and are kept nowhere else: a file truncated to nothing costs every
// pairing on the machine, and there is no way to get them back.
func (b *Book) Save() error {
	b.mu.RLock()
	defer b.mu.RUnlock()

	file, err := path()
	if err != nil {
		return err
	}

	onDisk := make(map[string]stored, len(b.entries))
	for name, entry := range b.entries {
		out := stored{ID: entry.ID.String(), Addrs: entry.Addrs, User: entry.User, Person: entry.Person, Trusted: entry.Trusted}
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
	return keep.Replace(file, append(raw, '\n'))
}

// Pin records a name for a peer id, without a shared secret.
func (b *Book) Pin(name string, id node.ID) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.entries[name] = Entry{Name: name, ID: id}
}

// Pair records a name, a peer id and the secret the two derived together.
func (b *Book) Pair(name string, id node.ID, secret []byte, addrs ...string) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.entries[name] = Entry{Name: name, ID: id, Secret: secret, Addrs: addrs}
}

// Belongs records whose machine an entry is. Pairing learns the person from the ticket; this is
// how that is kept, and it leaves everything else about the entry alone.
func (b *Book) Belongs(name, key string) {
	b.mu.Lock()
	defer b.mu.Unlock()

	entry, ok := b.entries[name]
	if !ok {
		return
	}
	entry.User = key
	entry.Person = b.personFor(key, entry.Name)
	entry.Trusted = entry.Trusted || b.trustedFor(key)
	b.entries[name] = entry
}

// trustedFor reports whether this person is already trusted here, on any machine of theirs. Trust
// belongs to the person, so a machine of theirs paired afterwards arrives with it rather than
// leaving the book saying two different things about one person.
func (b *Book) trustedFor(key string) bool {
	for _, entry := range b.entries {
		if entry.User == key && entry.Trusted {
			return true
		}
	}
	return false
}

// personFor is what a user key is already called here, and the fallback when it is called nothing.
//
// Every machine of one person carries the same label, so which of their machines was paired first
// does not decide what the rest of them are called.
func (b *Book) personFor(key, fallback string) string {
	for _, entry := range b.entries {
		if entry.User == key && entry.Person != "" {
			return entry.Person
		}
	}
	return fallback
}

// Trust marks somebody trusted or not, and every machine of theirs with them.
//
// Trust is a property of a person, not of one of their laptops: deciding you trust bob and then
// having to say it again for each machine he owns is a decision nobody would keep up with.
func (b *Book) Trust(name string, trusted bool) {
	b.mu.Lock()
	defer b.mu.Unlock()

	entry, ok := b.entries[name]
	if !ok {
		return
	}

	entry.Trusted = trusted
	b.entries[name] = entry

	if entry.User == "" {
		return
	}
	for at, other := range b.entries {
		if other.User == entry.User {
			other.Trusted = trusted
			b.entries[at] = other
		}
	}
}

// ByUser finds who a user key belongs to: the name this machine files that person under.
//
// This is the whole of person-level recognition. A machine nobody has ever paired with presents a
// badge signed by a key that is already in here, and it is that person's machine from then on --
// which is the point of pairing once per person instead of once per pair of machines.
//
// A person's machines are folded into one answer: the first by name, carrying trust if any machine
// of theirs has it. Returning whichever machine a map walk reached first would let the same caller
// be trusted on one connection and a stranger on the next.
func (b *Book) ByUser(key string) (Entry, bool) {
	if key == "" {
		return Entry{}, false
	}

	b.mu.RLock()
	defer b.mu.RUnlock()

	out, found, trusted := Entry{}, false, false
	for _, entry := range b.entries {
		if entry.User != key {
			continue
		}
		trusted = trusted || entry.Trusted
		if !found || entry.Name < out.Name {
			out, found = entry, true
		}
	}
	if found {
		out.Trusted = trusted
	}
	return out, found
}

// Remove drops a name, reporting whether it was there.
func (b *Book) Remove(name string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	_, ok := b.entries[name]
	delete(b.entries, name)
	return ok
}

// Lookup resolves a local name.
func (b *Book) Lookup(name string) (Entry, bool) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	entry, ok := b.entries[name]
	return entry, ok
}

// ByID finds the entry for a peer id.
func (b *Book) ByID(id node.ID) (Entry, bool) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	for _, entry := range b.entries {
		if entry.ID == id {
			return entry, true
		}
	}
	return Entry{}, false
}

// All lists the book, name order.
func (b *Book) All() []Entry {
	b.mu.RLock()
	defer b.mu.RUnlock()

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

// Reached remembers the address a device actually answered on.
//
// Finding a device is the expensive part of talking to it: discovery, a relay, a rendezvous, then
// a handshake. The address that worked is the best guess for the next time, and writing it down
// turns the second conversation into a dial rather than a search.
//
// Only when it changes something. A dial is not a reason to rewrite a file.
func (b *Book) Reached(id node.ID, at string) (bool, error) {
	b.mu.Lock()

	name, entry, found := "", Entry{}, false
	for known, e := range b.entries {
		if e.ID == id {
			name, entry, found = known, e, true
			break
		}
	}
	if !found || (len(entry.Addrs) > 0 && entry.Addrs[0] == at) {
		b.mu.Unlock()
		return false, nil
	}

	// First, and once: the rest keep their order behind it, because they were worth trying before
	// and may be again from somewhere else.
	addrs := make([]string, 0, len(entry.Addrs)+1)
	addrs = append(addrs, at)
	for _, was := range entry.Addrs {
		if was != at {
			addrs = append(addrs, was)
		}
	}
	if len(addrs) > mostAddrs {
		addrs = addrs[:mostAddrs]
	}

	entry.Addrs = addrs
	b.entries[name] = entry
	b.mu.Unlock()

	return true, b.Save()
}

// mostAddrs caps what is remembered for one device. A machine that moves between a few networks is
// worth keeping; one that has been on thirty is not, and the oldest are the least likely to work.
const mostAddrs = 8

// Moved points an entry at the machine its old one says it became, and says which entry it was.
//
// Everything else about the entry is kept: the name it is filed under here, the secret the two
// derived together, whose machine it is, and whether it is trusted. That is the whole point — a
// machine somebody replaced is the same machine to everybody who knew it, and being made to pair
// again with each of them is the thing this avoids.
//
// The addresses go, because they are where the old machine was and the new one is somewhere else.
// They are learned again the first time it is reached.
//
// Nothing here checks anybody's word for it. Whoever calls this has already checked that the old
// machine signed the statement, because the old machine's key is the id being replaced.
func (b *Book) Moved(was, now node.ID) (string, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if was == now || was.IsZero() || now.IsZero() {
		return "", false
	}

	// A machine already in the book under the new id is one this has been done for, or one paired
	// separately. Either way there is nothing to move and moving would make two entries one.
	for _, entry := range b.entries {
		if entry.ID == now {
			return "", false
		}
	}

	for name, entry := range b.entries {
		if entry.ID != was {
			continue
		}
		entry.ID, entry.Addrs = now, nil
		b.entries[name] = entry
		return name, true
	}
	return "", false
}

// Change re-reads the book, alters it, and writes it back, with nothing else able to write in
// between.
//
// Read, change, write is three steps, and another process writing between the first and the third
// has its work thrown away by the third. Every drop on a machine shares one address book and
// `drop peer pair` is its own process, so a pairing made at the wrong instant would vanish with
// nothing to say so. That was already worth avoiding when the only thing that wrote here was
// somebody typing; it matters more now a machine saying it moved can make the daemon write.
//
// alter reports whether anything actually changed, so a connection that had nothing to say does
// not rewrite the file.
func (b *Book) Change(alter func() (bool, error)) error {
	file, err := path()
	if err != nil {
		return err
	}

	return keep.While(file, func() error {
		if err := b.Refresh(); err != nil {
			return err
		}
		changed, err := alter()
		if err != nil || !changed {
			return err
		}
		return b.Save()
	})
}
