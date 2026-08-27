package node

import (
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"strings"
)

// A profile is a whole other person on this machine.
//
// Not a second name for the same identity: a profile gets its own device key, its own user key,
// its own address book and its own conversations. Two profiles on one machine are two strangers who
// have to pair before they can say anything to each other, which is exactly what is needed to try
// an access rule that names somebody else without owning a second computer.
//
// $DROP_PROFILE is the whole of it. Nothing else has to be set: the directories move underneath,
// and the port moves with them so two profiles can be up at once.

// Profile is which one is in use, and empty for the ordinary one.
func Profile() string { return strings.TrimSpace(os.Getenv("DROP_PROFILE")) }

// Under puts whatever a profile keeps beneath the directory the ordinary one uses. Exported for
// the data directory, which lives in another package but moves for the same reason.
func Under(base string) (string, error) { return profileDir(base) }

// profileDir puts a profile's things under the directory the ordinary one uses.
//
// A profile named with a slash or a dot would climb out of that directory and write over the
// ordinary profile's keys, so a name is letters, digits, dash and underscore or it is refused.
func profileDir(base string) (string, error) {
	name := Profile()
	if name == "" {
		return base, nil
	}
	if !safeProfile(name) {
		return "", fmt.Errorf("DROP_PROFILE=%q: a profile name is letters, digits, - and _", name)
	}
	return filepath.Join(base, "profiles", name), nil
}

func safeProfile(name string) bool {
	for _, c := range name {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-', c == '_':
		default:
			return false
		}
	}
	return true
}

// profilePort is where a profile listens when nothing said otherwise.
//
// Derived from the name rather than picked at random, so a profile is at the same port every time
// and an address written down for it keeps working. The ordinary profile keeps DefaultPort.
func profilePort() uint16 {
	name := Profile()
	if name == "" {
		return DefaultPort
	}

	// Above the ordinary port and well below the ephemeral range, so a profile cannot land on
	// something the kernel was about to hand out.
	sum := fnv.New32a()
	_, _ = sum.Write([]byte(name))

	return DefaultPort + 1 + uint16(sum.Sum32()%1000)
}
