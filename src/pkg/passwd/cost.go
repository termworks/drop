package passwd

import (
	"runtime"
	"sync"
)

// What one guess is allowed to spend, and how many may spend it at once.
//
// A hash is expensive on purpose: 64 MiB and three passes is what makes trying the dictionary
// slower than an attacker will sit through. That cost belongs to whoever guesses, and on a serving
// node it does not — a path guarded by a password is reachable by anybody who knows this device's
// id, so the guessing happens on somebody else's machine and the 64 MiB is allocated on this one.
//
// So the work is queued. A caller waits its turn rather than being refused, because a person typing
// a password should get in behind whoever is ahead of them; what is bounded is how much memory is
// committed at any moment, not how many people may ask.
var hashing = make(chan struct{}, atOnce())

// atOnce is how many hashes may run together: enough that one person never waits on nothing, few
// enough that the memory is a number this machine has.
func atOnce() int {
	n := runtime.NumCPU() / 2
	if n < 1 {
		return 1
	}
	if n > 4 {
		return 4
	}
	return n
}

// spend runs one hash, waiting for room.
func spend(hash func()) {
	hashing <- struct{}{}
	defer func() { <-hashing }()

	hash()
}

// Tried remembers what a caller already offered, so one session hashes a guess once.
//
// The same secret is checked more than once on the way through: a path is asked whether it admits
// somebody, and then, when it does not, whether they may at least know it is there. Both questions
// reach the same hash with the same guess, and each one is the full cost. This is the memory that
// makes the second question free.
//
// A caller with no Tried still works and simply pays twice; nothing here is required for a correct
// answer, only for a cheap one.
type Tried struct {
	mu sync.Mutex
	on map[string]bool
}

func NewTried() *Tried { return &Tried{on: map[string]bool{}} }

// Says whether a guess matches a hash, remembering the answer.
func (t *Tried) Says(hash, plain string) bool {
	if t == nil {
		return Verify(hash, plain)
	}

	// Keyed by both, because one caller may meet several hashes on one path and each is a separate
	// question. The guess is what a peer sent, and it is already in memory.
	key := hash + "\x00" + plain

	t.mu.Lock()
	was, known := t.on[key]
	t.mu.Unlock()

	if known {
		return was
	}

	got := Verify(hash, plain)

	t.mu.Lock()
	t.on[key] = got
	t.mu.Unlock()

	return got
}
