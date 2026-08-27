package note

import (
	"github.com/bresilla/drop/src/pkg/history"
	"github.com/bresilla/drop/src/pkg/weave"
)

// What the ordered changes come to when several people have been editing at once.
//
// Every change carries a whole file, so the content of the note as one person saw it is that
// person's change and nothing has to be replayed to find it. What is left is the join, which is
// somebody else's: where two changes are concurrent, the two files are merged three-way against the
// file their common ancestors make.

// saves is how a note is woven: a change is the whole file its author saved, and two of them go
// together line by line.
var saves = weave.Melding[[]byte]{
	Take:  func(_ func() []byte, c history.Change) []byte { return c.Body },
	Merge: weave.Bytes,
}

// Whole is the file a set of changes makes, and whatever could not be merged into it.
//
// named says what to call the person who signed with a key; an empty answer, or no function at all,
// leaves the key to speak for itself.
func Whole(changes []history.Change, named func(author string) string) ([]byte, []weave.Aside) {
	return weave.Join(changes, saves, named)
}
