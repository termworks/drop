package passwd

import (
	"context"
	"runtime"
	"sync"
	"sync/atomic"
)

// What one guess is allowed to spend, and how many may spend it at once.
//
// A hash is expensive on purpose: 64 MiB and three passes is what makes trying the dictionary
// slower than an attacker will sit through. That cost belongs to whoever guesses, and on a serving
// node it does not — a path guarded by a password is reachable by anybody who knows this device's
// id, so the guessing happens on somebody else's machine and the 64 MiB is allocated on this one.
//
// So the work is queued. A live caller waits its turn rather than being refused, and a caller whose
// stream or server has ended leaves the queue. What is bounded is how much memory is committed at
// any moment, not how many people may ask.
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

// spent counts the hashes that have actually been run. What a guess costs is the whole reason the
// memory is queued and the answer is remembered, and counting is the only way to say so in a test
// that a loaded machine cannot make flaky.
var spent atomic.Uint64

// Spent is how many guesses this process has paid for.
func Spent() uint64 { return spent.Load() }

func spendContext(ctx context.Context, slots chan struct{}, hash func()) bool {
	select {
	case slots <- struct{}{}:
	case <-ctx.Done():
		return false
	}
	defer func() { <-slots }()

	if ctx.Err() != nil {
		return false
	}

	spent.Add(1)
	hash()
	return true
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
	mu  sync.Mutex
	on  map[string]bool
	ctx context.Context
}

func NewTried() *Tried { return NewTriedContext(context.Background()) }

// NewTriedContext returns a guess memory whose queued hashes stop when ctx ends.
func NewTriedContext(ctx context.Context) *Tried {
	if ctx == nil {
		ctx = context.Background()
	}
	return &Tried{on: map[string]bool{}, ctx: ctx}
}

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

	got := verifyContext(t.ctx, hash, plain)

	t.mu.Lock()
	t.on[key] = got
	t.mu.Unlock()

	return got
}
