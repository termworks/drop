package node

import "sync"

// Settings a config may override.
//
// A setting the config never mentions is left alone, so the environment or a flag still decides
// it. That is why these are set rather than defaulted: there is no value here meaning "unset".
var (
	settingsMu   sync.RWMutex
	nameSet      string
	bootstrapSet []string
	relaysSet    []string
	// On unless a config turns it off. A device that has moved to another network cannot be found
	// any other way, and a program that only works while both machines are on one wire is not the
	// program this is meant to be. What it publishes is derived from a pairing secret and rotates
	// hourly, so a relay learns a key it cannot link to anybody and an address.
	rendezvousSet = true
)

// SetName makes this node call itself something other than its hostname.
func SetName(name string) {
	settingsMu.Lock()
	defer settingsMu.Unlock()

	nameSet = name
}

// SetBootstrap replaces the nodes used to join the DHT.
func SetBootstrap(addrs []string) {
	settingsMu.Lock()
	defer settingsMu.Unlock()

	bootstrapSet = addrs
}

// SetRelays replaces the relays this node reserves on.
func SetRelays(addrs []string) {
	settingsMu.Lock()
	defer settingsMu.Unlock()

	relaysSet = addrs
}

func configuredName() string {
	settingsMu.RLock()
	defer settingsMu.RUnlock()

	return nameSet
}

func configuredBootstrap() []string {
	settingsMu.RLock()
	defer settingsMu.RUnlock()

	return bootstrapSet
}

func configuredRelays() []string {
	settingsMu.RLock()
	defer settingsMu.RUnlock()

	return relaysSet
}

// SetRendezvous turns publishing this device's address on or off.
//
// On by default: a laptop that changed networks is the ordinary case, not the exotic one, and
// finding it again is the only thing that makes pairing worth having. A config can turn it off for
// somebody who would rather a device be unreachable than tell a relay it exists.
func SetRendezvous(on bool) {
	settingsMu.Lock()
	defer settingsMu.Unlock()

	rendezvousSet = on
}

// Rendezvous reports whether address publishing is on.
func Rendezvous() bool {
	settingsMu.RLock()
	defer settingsMu.RUnlock()

	return rendezvousSet
}
