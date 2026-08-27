package note

import (
	"fmt"

	"github.com/bresilla/drop/src/pkg/history"
	"github.com/bresilla/drop/src/pkg/weave"
)

// What the ordered changes come to when several people have been editing at once.
//
// Every change carries a whole file, so the content of the note as one person saw it is that
// person's change and nothing has to be replayed to find it. What is left is the join, which is
// somebody else's: where two changes are concurrent, the two files are merged three-way against the
// file their common ancestors make.

// MaxFold is how many versions of one note are put together at once.
//
// Several people editing one file at the same time is a handful of versions, and a handful is what
// this is for. A thousand of them is not a merge: they are folded one into the next, so both the
// time it takes and the file it would write grow with the square of their number, and one machine
// pushing changes as fast as it can would spend this one on nothing else.
const MaxFold = 1 << 4

// saves is how a note is woven: a change is the whole file its author saved, and two of them go
// together line by line.
var saves = weave.Melding[[]byte]{
	Take:  func(_ func() []byte, c history.Change) []byte { return c.Body },
	Merge: weave.Bytes,
}

// Whole is the file a set of changes makes, and whatever could not be merged into it.
//
// Nobody is named. The file this makes is written back to disk and held up against the file every
// other machine made of the same changes, so it has to be the changes and nothing else: what this
// machine calls somebody is this machine's own business, and a name in a conflict marker is content
// like any other, which the next save would sign and hand to everybody.
func Whole(changes []history.Change) ([]byte, []weave.Aside, error) {
	if n := len(weave.Heads(changes)); n > MaxFold {
		return nil, nil, fmt.Errorf("putting %d versions of this note together, and %d is as many as it merges", n, MaxFold)
	}
	body, aside := weave.Join(changes, saves, nil)
	return body, aside, nil
}
