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
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

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

// Seed is the machine key material, derived from what the hardware said.
//
// Domain-separated and versioned: the version is in the purpose, so the day this has to derive
// differently, old machines keep their names by keeping the old purpose.
func (m Mark) Seed() ([32]byte, error) {
	if !m.Held() {
		return [32]byte{}, fmt.Errorf("this machine says nothing about itself that a name could be made from")
	}

	h := blake3.New(32, nil)
	h.Write([]byte(purpose))
	h.Write([]byte{0})
	h.Write([]byte(m.From.String()))
	h.Write([]byte{0})
	h.Write(m.raw)

	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out, nil
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
