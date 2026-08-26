package made

import "github.com/bresilla/drop/src/pkg/ns"

// Access is who may reach a created namespace, in the words the config uses for the same rule.
//
// The words rather than the namespace layer's own fields, so that a file somebody opens reads like
// the config beside it: paired, trusted, anyone, a list of names, a key, a password.
//
// Refusal is not here. It is what revoking from the interface leaves behind, it is never written in
// the config, and a second place to write one would be a second place to forget to look.
type Access struct {
	// Paired admits any device in the address book, Trusted only the ones you decided to trust,
	// and Anyone admits whoever turns up.
	Paired  bool `json:"paired,omitempty"`
	Trusted bool `json:"trusted,omitempty"`
	Anyone  bool `json:"anyone,omitempty"`
	// Named admits people and machines, spelt the way an access rule spells them: "bob" is a
	// person and every machine of theirs, "bob@laptop" is one of them.
	Named []string `json:"named,omitempty"`
	// Keys admits bare endpoint ids that never paired, and Password is a hash from
	// `drop me passwd` — never the word itself, which a file this readable would hand over.
	Keys     []string `json:"keys,omitempty"`
	Password string   `json:"password,omitempty"`
	// All requires every rule here rather than any one of them.
	All bool `json:"all,omitempty"`
	// Visible is who may see the path without being able to open it, and the two words that widen
	// it to everybody paired or to everybody trusted.
	Visible        []string `json:"visible,omitempty"`
	VisiblePaired  bool     `json:"visible_paired,omitempty"`
	VisibleTrusted bool     `json:"visible_trusted,omitempty"`
}

// Says reports whether this admits anybody at all. One that says nothing admits nobody.
func (a Access) Says() bool {
	return a.Paired || a.Trusted || a.Anyone || len(a.Named) > 0 || len(a.Keys) > 0 || a.Password != ""
}

// Rule is this in the words the namespace layer decides on.
func (a Access) Rule() ns.Access {
	return ns.Access{
		AnyPaired:      a.Paired,
		AnyTrusted:     a.Trusted,
		Anyone:         a.Anyone,
		Named:          append([]string(nil), a.Named...),
		Keys:           append([]string(nil), a.Keys...),
		Password:       a.Password,
		All:            a.All,
		Visible:        append([]string(nil), a.Visible...),
		AnyVisible:     a.VisiblePaired,
		TrustedVisible: a.VisibleTrusted,
	}
}

// Ruled is a rule put back into the words that can be written down. Whoever has been refused is
// left behind, because that belongs to the grants and is not a config's to say.
func Ruled(a ns.Access) Access {
	return Access{
		Paired:         a.AnyPaired,
		Trusted:        a.AnyTrusted,
		Anyone:         a.Anyone,
		Named:          append([]string(nil), a.Named...),
		Keys:           append([]string(nil), a.Keys...),
		Password:       a.Password,
		All:            a.All,
		Visible:        append([]string(nil), a.Visible...),
		VisiblePaired:  a.AnyVisible,
		VisibleTrusted: a.TrustedVisible,
	}
}

func (a Access) clone() Access {
	out := a
	out.Named = append([]string(nil), a.Named...)
	out.Keys = append([]string(nil), a.Keys...)
	out.Visible = append([]string(nil), a.Visible...)
	return out
}
