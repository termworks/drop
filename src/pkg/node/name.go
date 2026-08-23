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
		return "unknown"
	}
	return name
}
