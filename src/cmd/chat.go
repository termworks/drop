package cmd

import (
	"github.com/bresilla/drop/src/pkg/arch"
	"github.com/bresilla/drop/src/pkg/ns"
)

// chatMounts is the one namespace a chat window serves while it is open.
//
// Open to any paired device, and said so rather than left out: access is denied unless a rule
// grants it, and a mount with no rule is one nobody can ever say a word into.
func chatMounts(known *arch.Registry) *ns.Table {
	m := ns.Mount{Path: "/chat", Archetype: "chat", Access: ns.Access{AnyPaired: true}}
	if answers, ok := known.Lookup(m.Archetype, 0); ok {
		m.Config, _ = answers.Read(nothing{})
	}

	table := ns.NewTable()
	_ = table.Add(m)
	return table
}
