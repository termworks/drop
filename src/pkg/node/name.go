package node

import "os"

// DisplayName is what this node calls itself to others. Convenience only: it is self-declared, so
// anyone can claim any name, and nothing may be authorised on the strength of it.
//
// The config wins when it named one, then $DROP_NAME, then the hostname.
func DisplayName() string {
	if name := configuredName(); name != "" {
		return name
	}
	if name := os.Getenv("DROP_NAME"); name != "" {
		return name
	}
	name, err := os.Hostname()
	if err != nil || name == "" {
		name = "unknown"
	}

	// A profile is a different machine as far as the protocol is concerned, so it says so. Two
	// rows both called "tron" in somebody's peer list would be unreadable, and the name is the
	// thing an access rule is written against.
	if at := Profile(); at != "" {
		return name + "-" + at
	}
	return name
}
