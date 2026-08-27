package ns

import (
	"encoding/hex"

	"lukechampine.com/blake3"

	"github.com/bresilla/drop/src/pkg/wire"
)

// Shared is a namespace several machines hold, and what its name is worked out from.
//
// Five machines holding the same thing have to agree on what to call it, and the name cannot be
// minted at random on each of them or they would be five unrelated namespaces that happen to share
// a spelling. So it is derived rather than issued: whoever holds these three facts arrives at the
// same name, and a machine that is told them can work the name out for itself instead of taking a
// stranger's word for what something is called.
//
// Derived rather than minted once and carried, because a namespace declared in the config is read
// again at every start. A minted name would have to be remembered somewhere beside the file that
// declares it, and a config and a mint that disagree is a namespace that quietly becomes a
// different one after a restart.
type Shared struct {
	// Creator is the person who made it: their user key, written the way authorized_keys writes
	// one. Empty means this namespace is one machine's own.
	Creator string `json:"creator"`
	// At is the path they made it at.
	At string `json:"at"`
	// Nonce tells one thing made at that path from another made there later. A command mints one;
	// a config writes a word, and writing a different one is how somebody says this is a new thing
	// at an old path.
	Nonce string `json:"nonce,omitempty"`
}

// Declared reports whether this names a namespace several machines hold.
func (s Shared) Declared() bool { return s.Creator != "" }

// ID is the name every machine holding it uses for it, and the name its history is filed under.
func (s Shared) ID() string {
	if !s.Declared() {
		return ""
	}

	w := wire.NewWriter()
	w.String(s.Creator)
	w.String(s.At)
	w.String(s.Nonce)

	sum := blake3.Sum256(w.Body())
	return hex.EncodeToString(sum[:])
}
