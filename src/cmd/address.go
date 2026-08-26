package cmd

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"strings"
	"time"

	"github.com/bresilla/drop/src/pkg/book"
	"github.com/bresilla/drop/src/pkg/node"
	"github.com/bresilla/drop/src/pkg/ns"
)

// Which machine an address means.
//
// Every command takes the same address, so there is one answer to "which machine is that" and it
// lives here. An address names a machine outright, or names only whose it is and leaves this to
// work out which of theirs — and when it cannot, it says so instead of picking.

// resolve turns an address into the machine it names.
func resolve(at ns.Address) (book.Entry, error) {
	if at.Here {
		return book.Entry{}, fmt.Errorf("%s is this machine, and this command is for somebody else's", at)
	}
	if at.Named() {
		return book.Resolve(at.Machine)
	}

	pinned, err := book.Load()
	if err != nil {
		return book.Entry{}, err
	}

	theirs := machinesOf(pinned, at.User)
	switch len(theirs) {
	case 0:
		return book.Entry{}, fmt.Errorf("nobody here is called %q: pair with a machine of theirs first", at.User)
	case 1:
		return theirs[0], nil
	}

	if one, ok := onlyAnswering(theirs); ok {
		return one, nil
	}
	return book.Entry{}, fmt.Errorf("%s has %d machines here and none of them answered: say which of %s",
		at.User, len(theirs), namesOf(theirs))
}

// machinesOf is every machine in the book that belongs to a person, found by the name they are
// called here or by the user key itself.
func machinesOf(pinned *book.Book, who string) []book.Entry {
	mine := myKey()

	var out []book.Entry
	for _, entry := range pinned.All() {
		if !entry.Owned() {
			continue
		}
		if entry.Person == who || entry.User == who || (who == "me" && mine != "" && entry.User == mine) {
			out = append(out, entry)
		}
	}
	return out
}

// onlyAnswering narrows a person's machines to the one this node is holding a connection to, and
// gives up when that is none of them or more than one.
func onlyAnswering(theirs []book.Entry) (book.Entry, bool) {
	held := heldHere()
	if len(held) == 0 {
		return book.Entry{}, false
	}

	out, found := book.Entry{}, false
	for _, entry := range theirs {
		if !held[entry.ID] {
			continue
		}
		if found {
			return book.Entry{}, false
		}
		out, found = entry, true
	}
	return out, found
}

// heldHere is which devices the node running on this machine has a connection to.
//
// Not a probe: the daemon is asked what it is already holding, so narrowing a person's machines to
// one costs nothing. With no daemon there is nothing to narrow with and the answer is empty.
func heldHere() map[node.ID]bool {
	path, err := castSocket()
	if err != nil {
		return nil
	}

	conn, err := net.Dial("unix", path)
	if err != nil {
		return nil
	}
	defer conn.Close()

	_ = conn.SetDeadline(time.Now().Add(answerWait))
	if _, err := io.WriteString(conn, "held\n"); err != nil {
		return nil
	}

	out := map[node.ID]bool{}
	scan := bufio.NewScanner(conn)
	for scan.Scan() {
		if id, err := node.ParseID(strings.TrimSpace(scan.Text())); err == nil {
			out[id] = true
		}
	}
	return out
}

// answerWait bounds asking the node on this machine a question it answers out of memory.
const answerWait = 2 * time.Second

// namesOf lists machines the way a sentence does.
func namesOf(entries []book.Entry) string {
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name)
	}
	switch len(names) {
	case 0:
		return "nothing"
	case 1:
		return names[0]
	}
	return strings.Join(names[:len(names)-1], ", ") + " or " + names[len(names)-1]
}

// splitAddress reads an address whose namespace is followed by a real filename.
//
// Below a directory namespace the rest of the path is a name on the far machine, with whatever
// capitals and spaces that filesystem takes, so it travels exactly as it was typed rather than
// through the spelling ns.Clean enforces.
func splitAddress(text string) (ns.Address, string, error) {
	text = strings.TrimSpace(text)

	names, under, cut := strings.Cut(text, "/")
	if !cut {
		at, err := ns.ParseAddress(text)
		return at, ns.Root, err
	}

	at, err := ns.ParseAddress(names + "/")
	if err != nil {
		return ns.Address{}, "", err
	}
	return at, "/" + strings.Trim(under, "/"), nil
}
