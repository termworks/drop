package proto

import (
	"sync"
	"time"

	"github.com/bresilla/drop/src/pkg/node"
)

// What one peer may spend on guesses, and how many peers are counted at once.
//
// Checking a password costs this machine 64 MiB and three passes of argon2, and a path guarded by
// one is reachable by anybody who knows this device's id: the guessing happens somewhere else and
// the memory is spent here. So a peer gets a handful of tries and then waits, which is no burden on
// somebody typing a word they know and the end of trying a dictionary.
const (
	mostGuesses  = 6
	guessWindow  = time.Minute
	mostGuessers = 4096
)

// guessing is what each peer has spent lately. Per process, because so is the memory it stands in
// front of.
var guessing = &guesses{spent: map[node.ID]guess{}}

// guess is what one peer has offered inside one window.
type guess struct {
	count int
	first time.Time
}

type guesses struct {
	mu    sync.Mutex
	spent map[node.ID]guess
}

// spare takes one try from a peer's allowance, reporting whether it had one to take.
func (g *guesses) spare(from node.ID) bool {
	now := time.Now()

	g.mu.Lock()
	defer g.mu.Unlock()

	had, known := g.spent[from]
	if !known || now.Sub(had.first) > guessWindow {
		had = guess{first: now}
	}
	if had.count >= mostGuesses {
		g.spent[from] = had
		return false
	}
	// A stranger holds as many ids as it cares to make, so the table is bounded too. Full of peers
	// that are all still guessing is a flood, and the safe answer to a flood is not to hash.
	if !known && !g.room(now) {
		return false
	}

	had.count++
	g.spent[from] = had
	return true
}

// forget clears what a peer spent, once it has opened something. Ordinary use never reaches the
// limit, however many times somebody offers a password that works.
func (g *guesses) forget(from node.ID) {
	g.mu.Lock()
	defer g.mu.Unlock()

	delete(g.spent, from)
}

// room drops what has gone cold and says whether another peer fits.
func (g *guesses) room(now time.Time) bool {
	if len(g.spent) < mostGuessers {
		return true
	}
	for at, had := range g.spent {
		if now.Sub(had.first) > guessWindow {
			delete(g.spent, at)
		}
	}
	return len(g.spent) < mostGuessers
}
