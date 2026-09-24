package ns

// Who may reach a path, as one step on a ladder rather than a rule written out. Every path on every
// machine of yours can be put on a step from the interface, and the step is what a person reads.
const (
	// LevelMe is your own machines, and nobody else.
	LevelMe = "me"
	// LevelTrusted is you and the people you decided to trust.
	LevelTrusted = "trusted"
	// LevelPaired is everybody you paired with.
	LevelPaired = "paired"
	// LevelAnyone is whoever knows this machine's id.
	LevelAnyone = "anyone"
	// LevelCustom is a rule that is none of those: names only, a key, or a password.
	LevelCustom = "custom"
)

// IsLevel reports whether a step is one that can be chosen.
func IsLevel(level string) bool {
	switch level {
	case LevelMe, LevelTrusted, LevelPaired, LevelAnyone:
		return true
	}
	return false
}

// Leveling is a source of grants that can also put a path on a step in place of its rule, and say
// whether those who may not open it may see it. Optional: a source of names alone stays one.
type Leveling interface {
	Level(path string) (level string, shown *bool)
}

// leveled is a rule put on a step: the step in place of whatever it said about who, you always, and
// what it said about being seen left as it was.
func leveled(rule Access, level string) Access {
	out := Access{
		Named:          []string{LevelMe},
		Visible:        rule.Visible,
		AnyVisible:     rule.AnyVisible,
		TrustedVisible: rule.TrustedVisible,
	}
	switch level {
	case LevelAnyone:
		out.Anyone = true
	case LevelPaired:
		out.AnyPaired = true
	case LevelTrusted:
		out.AnyTrusted = true
	}
	return out
}

// shownAs is a rule with being seen set one way: everybody paired may see it is there, or nobody
// who may not open it.
func shownAs(rule Access, shown bool) Access {
	rule.AnyVisible, rule.TrustedVisible = shown, false
	if !shown {
		rule.Visible = nil
	}
	return rule
}

// LevelOf is the step a rule stands on, as a person would read it.
func LevelOf(rule Access) string {
	switch {
	case rule.Password != "" || len(rule.Keys) > 0 || rule.All:
		return LevelCustom
	case rule.Anyone:
		return LevelAnyone
	case rule.AnyPaired:
		return LevelPaired
	case rule.AnyTrusted:
		return LevelTrusted
	}
	for _, name := range rule.Named {
		if name == LevelMe {
			return LevelMe
		}
	}
	if len(rule.Named) == 0 {
		return LevelMe
	}
	return LevelCustom
}
