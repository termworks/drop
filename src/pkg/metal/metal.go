// Package metal is what machine this is, taken from the machine itself.
//
// A machine identity that lives only in a file is a machine identity that dies when the disk is
// wiped, and one that can be copied onto a second machine by copying the file. Taking it from the
// hardware answers both: reinstall the operating system and it is the same machine, because the
// machine is the same; carry the backup to another box and it is not that machine, because the
// key was never in the backup.
//
// What that costs, said once and plainly: on a machine where several people have accounts, the
// serial number this derives from is one all of them can read, so all of them can work out the
// machine key. So this identifies hardware; it is not a wall between the people sitting at it.
// Who somebody is stays with their user key, which is where the namespaces are owned anyway. A
// TPM does not have this problem, which is why it is asked first.
package metal

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/user"
	"sort"
	"strings"
	"sync"

	"lukechampine.com/blake3"
)

// Source is where a machine's identity was taken from, strongest first.
type Source int

const (
	// Nowhere is a machine that would say nothing about itself. Its identity is kept in a file
	// and is as portable as that file.
	Nowhere Source = iota
	// Disk is a serial number written on a drive. It survives the drive being wiped and goes when
	// the drive does, which makes the drive the machine.
	Disk
	// Board is a serial the firmware holds: the machine itself, whatever is plugged into it.
	Board
	// Chip is a TPM. The material never leaves it, so nobody reads it off a running machine and
	// nobody derives it on another one.
	Chip
)

func (s Source) String() string {
	switch s {
	case Chip:
		return "the TPM"
	case Board:
		return "the board"
	case Disk:
		return "a drive"
	}
	return "nothing"
}

// Mark is what a machine is known by, and where that came from.
type Mark struct {
	// From is which kind of thing said it.
	From Source
	// Says is the one line to show a person: what was read, and off what.
	Says string

	// raw is the material itself. Unexported and never written down or sent: it is what the key
	// is derived from, and a machine that published it would be handing over its own name.
	raw []byte
}

// Held reports whether anything was found to hold on to.
func (m Mark) Held() bool { return m.From != Nowhere && len(m.raw) > 0 }

// purpose keeps this derivation to itself, so the same serial used for anything else — a disk
// label, a licence, another program doing the same trick — cannot produce the same key.
const purpose = "drop machine identity v1"

// Seed is the key material for one drop on this machine.
//
// Domain-separated and versioned: the version is in the purpose, so the day this has to derive
// differently, old machines keep their names by keeping the old purpose.
//
// Which drop goes in as well as which machine. A machine is one machine, and the drops running on
// it are several — every account with one, every profile under an account, every node a test brings
// up. Each has to be reachable as itself, and two of them deriving one key would not be one machine
// with several people on it but two programs answering to one address, which is nobody.
//
// What tells them apart is where each keeps its things, because that is exactly what makes one drop
// a different drop from another. It survives a reinstall — the path is the same on the machine that
// comes back — and it does not survive being carried somewhere else, which is the point.
func (m Mark) Seed(whose string) ([32]byte, error) {
	if !m.Held() {
		return [32]byte{}, fmt.Errorf("this machine says nothing about itself that a name could be made from")
	}
	if whose == "" {
		return [32]byte{}, fmt.Errorf("a name needs to know which drop on this machine it is for")
	}

	return m.derive("one drop on it", whose), nil
}

// Whose is which account on this machine is asking.
//
// The account name rather than its number: a person who reinstalls and makes their account again
// is the same person and picks the same name, where the number depends on what order the accounts
// were made in. It is not a secret and does not need to be — what it separates is the people on one
// machine, and they can all read the machine's serial anyway.
func Whose() string {
	if who, err := user.Current(); err == nil && who.Username != "" {
		return who.Username
	}
	if named := os.Getenv("USER"); named != "" {
		return named
	}
	return "somebody"
}

// Brief is a few characters standing for the material, for a person checking two machines are not
// somehow the same. It is a digest and not the material: printing the material would put the thing
// the key is derived from on somebody's screen.
func (m Mark) Brief() string {
	if !m.Held() {
		return ""
	}
	sum := blake3.Sum256(append([]byte("drop machine mark"), m.raw...))
	return hex.EncodeToString(sum[:6])
}

// Read is what this machine says about itself, taking the strongest thing it will admit to.
//
// Every source is tried, and the best one wins rather than the first: a machine with a TPM should
// not be identified by a drive serial because the drive happened to answer sooner, and one whose
// TPM is present but unreadable should still have a name.
func Read() Mark {
	once.Do(func() { found = looked() })
	return found
}

// once keeps the answer. Nothing this reads changes while the process runs, and one of the things
// it reads is a TPM, which is slow enough to be worth asking only the first time.
var (
	once  sync.Once
	found Mark
)

// looked tries every source and takes the best one that answered.
func looked() Mark {
	best := Mark{}
	for _, look := range sources {
		m, err := look()
		if err != nil || !m.Held() {
			continue
		}
		if m.From > best.From {
			best = m
		}
	}
	return best
}

// sources is everything that might know, in no particular order: Read picks by strength, not by
// position, so adding one here cannot quietly demote another.
var sources = []func() (Mark, error){
	fromChip,
	fromBoard,
	fromDisk,
}

// steady turns whatever a file held into material worth deriving from, and refuses what is not
// worth it.
//
// Firmware is full of strings that are the same on every machine of a model — "To be filled by
// O.E.M.", "Default string", a run of zeroes — and a machine identified by one of those would
// share its name with every other machine of that model. Better to have no name than that one.
func steady(raw []byte) ([]byte, bool) {
	text := strings.TrimSpace(strings.Trim(string(raw), "\x00"))
	if len(text) < 4 {
		return nil, false
	}

	switch strings.ToLower(text) {
	case "to be filled by o.e.m.", "default string", "none", "unknown",
		"system serial number", "not specified", "not applicable", "0", "n/a":
		return nil, false
	}

	// A serial that is one character repeated says nothing either: 0000000, ffffffff.
	only := true
	for i := 1; i < len(text); i++ {
		if text[i] != text[0] {
			only = false
			break
		}
	}
	if only {
		return nil, false
	}
	return []byte(text), true
}

// gathered is several pieces of material as one, in a fixed order so that reading them in a
// different order on the next boot cannot change the machine's name.
func gathered(parts []string) []byte {
	sort.Strings(parts)
	return []byte(strings.Join(parts, "\x00"))
}

// Machine is the key material for the machine itself, rather than for one person's drop on it.
//
// No account goes into it: the point of it is to be the same for everyone with an account here, so
// that two of them can each say "this endpoint of mine is on that machine" and be talking about one
// machine. Which means, said plainly, that everyone with an account here can produce it — so what
// it proves is which hardware something is on, and never which person is behind it. People are
// proved by their own keys, which is where drop already keeps that question.
func (m Mark) Machine() ([32]byte, error) {
	if !m.Held() {
		return [32]byte{}, fmt.Errorf("this machine says nothing about itself that a name could be made from")
	}

	return m.derive("the machine itself", ""), nil
}

// bound writes a piece into a hash so that where it ends cannot be mistaken.
//
// The pieces are joined with a separator, and one of them — the drive case — is itself several
// serials joined the same way. Two machines could then hash the same bytes from different pieces:
// a serial that ends where a path begins is the same run of bytes as a serial that runs on into it.
// A length in front of each removes the question rather than arguing about whether the values that
// would collide can occur.
func bound(h io.Writer, part []byte) {
	var n [8]byte
	binary.BigEndian.PutUint64(n[:], uint64(len(part)))
	h.Write(n[:])
	h.Write(part)
}

// derive is every key this machine has, made the same way and kept apart from each other.
//
// kind is what the key is for and whose is which drop it belongs to, and they are separate pieces
// because they come from different places: kind is written here, whose is a path off the disk. In
// one slot between them, a drop whose directory happened to read like the word for a machine key
// would derive the machine key. Two slots cannot be confused for one another whatever is in them.
func (m Mark) derive(kind, whose string) [32]byte {
	h := blake3.New(32, nil)
	bound(h, []byte(purpose))
	bound(h, []byte(m.From.String()))
	bound(h, m.raw)
	bound(h, []byte(kind))
	bound(h, []byte(whose))

	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}
