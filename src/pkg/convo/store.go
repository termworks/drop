package convo

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"github.com/bresilla/drop/src/pkg/wire"

	"github.com/bresilla/drop/src/pkg/node"
)

// Store is one peer's conversation on disk.
//
// The history is append-only: a record is written once and never rewritten, so a crash midway can
// truncate the tail but cannot corrupt what came before. The outbox is the mutable half, holding
// only what has not been delivered yet, and it is small enough to rewrite whole.
type Store struct {
	mu      sync.Mutex
	peer    node.ID
	dir     string
	history string
	outbox  string
}

// DataDir is $XDG_DATA_HOME/drop, or ~/.local/share/drop. Conversations are data, not settings, so
// they do not live beside the config.
func DataDir() (string, error) {
	if dir := os.Getenv("XDG_DATA_HOME"); dir != "" {
		return node.Under(filepath.Join(dir, "drop"))
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locating home directory: %w", err)
	}
	return node.Under(filepath.Join(home, ".local", "share", "drop"))
}

// Open prepares the conversation with one peer.
func Open(id node.ID) (*Store, error) {
	base, err := DataDir()
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(base, "convo", id.String())
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("creating %s: %w", dir, err)
	}
	return &Store{
		peer:    id,
		dir:     dir,
		history: filepath.Join(dir, "history"),
		outbox:  filepath.Join(dir, "outbox"),
	}, nil
}

// Peer is who this conversation is with.
func (s *Store) Peer() node.ID {
	return s.peer
}

// record is one entry as stored: the direction, then the message, sealed if there is a key.
func record(m Message, peer string) ([]byte, error) {
	key, err := keyed()
	if err != nil {
		// A key that exists and cannot be reached must not turn into plaintext appended to a
		// sealed history. Refusing to write is the only safe answer.
		return nil, err
	}
	return recordWith(m, peer, key)
}

// recordWith is the same, under a key given rather than held -- which is what rewriting a log to
// turn a vault on or off needs, because that reads under one key and writes under another.
func recordWith(m Message, peer string, key []byte) ([]byte, error) {
	w := wire.NewWriter()
	w.Byte(m.Dir)
	w.Bytes(m.Encode())
	body := w.Body()

	if len(key) == 0 {
		return body, nil
	}
	return seal(key, body, peer, m.ID)
}

func unrecord(body []byte, peer string) (Message, error) {
	// Both forms are readable: a log written before a vault existed, and one written since. A
	// vault that could not read what came before it would be a vault nobody could turn on.
	if isSealed(body) {
		key, err := keyed()
		if err != nil {
			return Message{}, err
		}
		if len(key) == 0 {
			return Message{}, ErrLocked
		}
		opened, err := unseal(key, body, peer)
		if err != nil {
			return Message{}, err
		}
		body = opened
	}
	return plain(body)
}

// plain reads a record that is not sealed.
func plain(body []byte) (Message, error) {
	r := wire.NewReader(body)
	dir, err := r.Byte()
	if err != nil {
		return Message{}, err
	}
	packed, err := r.Bytes(MaxBody + 4096)
	if err != nil {
		return Message{}, err
	}
	m, err := Decode(packed)
	if err != nil {
		return Message{}, err
	}
	m.Dir = dir
	return m, nil
}

// append writes one length-prefixed record and flushes it, so a message that was reported stored
// is on the disk rather than in a buffer.
func appendTo(path string, body []byte) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("opening %s: %w", path, err)
	}
	defer file.Close()

	var head [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(head[:], uint64(len(body)))
	if _, err := file.Write(append(head[:n], body...)); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return file.Sync()
}

// readAll walks a log. A truncated tail — a crash mid-write — ends the walk rather than failing
// it, because the records before it are still good and losing them would be the worse outcome.
func readAll(path, peer string) ([]Message, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}

	var out []Message
	for at := 0; at < len(raw); {
		size, used := binary.Uvarint(raw[at:])
		if used <= 0 || at+used+int(size) > len(raw) {
			break
		}
		at += used
		m, err := unrecord(raw[at:at+int(size)], peer)
		if errors.Is(err, ErrLocked) {
			// Not a truncated tail: the records are whole and the key is not here. Reporting an
			// empty conversation would be a lie about the disk rather than a report about the key.
			return nil, err
		}
		if err != nil {
			break
		}
		at += int(size)
		out = append(out, m)
	}
	return out, nil
}

// Add records a message in the history, ignoring one already there. Returns whether it was new,
// which is how a resend is told from a first delivery.
func (s *Store) Add(m Message) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	known, err := readAll(s.history, s.peer.String())
	if err != nil {
		return false, err
	}
	for _, seen := range known {
		if seen.ID == m.ID {
			return false, nil
		}
	}
	body, err := record(m, s.peer.String())
	if err != nil {
		return false, err
	}
	if err := appendTo(s.history, body); err != nil {
		return false, err
	}
	return true, nil
}

// History is everything that passed with this peer, oldest first.
func (s *Store) History() ([]Message, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	out, err := readAll(s.history, s.peer.String())
	if err != nil {
		return nil, err
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].At != out[j].At {
			return out[i].At < out[j].At
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

// Queue records a message to send and holds it until it is delivered. A message is in the history
// the moment it is composed: what is uncertain is whether it arrived, not whether it was said.
func (s *Store) Queue(m Message) error {
	if _, err := s.Add(m); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	body, err := record(m, s.peer.String())
	if err != nil {
		return err
	}
	return appendTo(s.outbox, body)
}

// Pending is what has not been delivered, oldest first.
func (s *Store) Pending() ([]Message, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	out, err := readAll(s.outbox, s.peer.String())
	if err != nil {
		return nil, err
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// Delivered drops messages from the outbox once the far end has acknowledged them.
//
// The outbox is rewritten through a temporary file and renamed, so an interruption leaves either
// the old outbox or the new one. Truncating in place would leave a half-written queue.
func (s *Store) Delivered(ids ...string) error {
	if len(ids) == 0 {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	gone := make(map[string]bool, len(ids))
	for _, id := range ids {
		gone[id] = true
	}

	waiting, err := readAll(s.outbox, s.peer.String())
	if err != nil {
		return err
	}

	var keep []byte
	for _, m := range waiting {
		if gone[m.ID] {
			continue
		}
		body, err := record(m, s.peer.String())
		if err != nil {
			return err
		}
		var head [binary.MaxVarintLen64]byte
		n := binary.PutUvarint(head[:], uint64(len(body)))
		keep = append(keep, head[:n]...)
		keep = append(keep, body...)
	}

	if len(keep) == 0 {
		if err := os.Remove(s.outbox); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("clearing %s: %w", s.outbox, err)
		}
		return nil
	}

	scratch := s.outbox + ".new"
	if err := os.WriteFile(scratch, keep, 0o600); err != nil {
		return fmt.Errorf("writing %s: %w", scratch, err)
	}
	if err := os.Rename(scratch, s.outbox); err != nil {
		return fmt.Errorf("replacing %s: %w", s.outbox, err)
	}
	return nil
}

// Note records something drop did, so the log reads as one story rather than only the chat half.
func (s *Store) Note(kind byte, dir byte, body, extra string) error {
	m, err := New(kind, body, extra)
	if err != nil {
		return err
	}
	m.Dir = dir
	_, err = s.Add(m)
	return err
}

// Peers lists every peer there is a conversation with.
func Peers() ([]node.ID, error) {
	base, err := DataDir()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(filepath.Join(base, "convo"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading conversations: %w", err)
	}

	var out []node.ID
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		id, err := node.ParseID(entry.Name())
		if err != nil {
			continue
		}
		out = append(out, id)
	}
	return out, nil
}

// Rewrite writes both logs again under a key given: what is there now is read with the key this
// process holds, and written back under `to`. Nothing at all writes them in the clear.
//
// This is what turning a vault on does to what is already there, and what turning one off undoes.
// Both directions are the same walk: every record is read -- sealed or not -- and written back the
// way the current key says it should be.
//
// Each log goes through a temporary file and a rename, so an interruption leaves either the old one
// or the new one. It is not safe to run while a daemon is appending: an append that lands during
// the walk is in neither file afterwards, which is why the command that calls this says so.
func (s *Store) Rewrite(to []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, at := range []string{s.history, s.outbox} {
		all, err := readAll(at, s.peer.String())
		if err != nil {
			return err
		}
		if len(all) == 0 {
			continue
		}

		var out []byte
		for _, m := range all {
			body, err := recordWith(m, s.peer.String(), to)
			if err != nil {
				return err
			}
			var head [binary.MaxVarintLen64]byte
			n := binary.PutUvarint(head[:], uint64(len(body)))
			out = append(out, head[:n]...)
			out = append(out, body...)
		}

		scratch := at + ".new"
		if err := os.WriteFile(scratch, out, 0o600); err != nil {
			return fmt.Errorf("writing %s: %w", scratch, err)
		}
		if err := os.Rename(scratch, at); err != nil {
			return fmt.Errorf("replacing %s: %w", at, err)
		}
	}
	return nil
}
