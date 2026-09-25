package made

// Kind is one kind of topic as a person picks it, and the archetype that serves it. The same list
// on the command line, in the terminal interface and on a phone, so a folder is a folder
// everywhere.
type Kind struct {
	Name      string `json:"name"`
	Archetype string `json:"archetype"`
	About     string `json:"about"`
	// Command says the kind needs a command, which is the one thing a person has to type.
	Command bool `json:"command,omitempty"`
}

// Kinds is every kind a topic can be, in the order they are offered.
var Kinds = []Kind{
	{Name: "chat", Archetype: "chat", About: "a conversation"},
	{Name: "inbox", Archetype: "share", About: "files handed over, landing in a folder"},
	{Name: "folder", Archetype: "files", About: "a folder to walk through and put things in"},
	{Name: "note", Archetype: "note", About: "a file several people write at once"},
	{Name: "terminal", Archetype: "tty", About: "a shell on the machine, each watcher their own"},
	{Name: "links", Archetype: "link", About: "links that open over there"},
	{Name: "stream", Archetype: "stream", About: "what a command prints, as it comes", Command: true},
}

// KindNamed is a kind by its own name or its archetype's.
func KindNamed(name string) (Kind, bool) {
	for _, k := range Kinds {
		if k.Name == name || k.Archetype == name {
			return k, true
		}
	}
	return Kind{}, false
}
