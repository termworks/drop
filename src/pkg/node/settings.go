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
	// On unless a config turns it off. See SetDirect.
	directSet = true
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

// SetDirect turns publishing the addresses this machine has on its own networks on or off.
//
// On by default. Without it an endpoint says only where a relay saw it come from, so two machines
// on one wire — or on one overlay — hand each other nothing either can dial and meet through a
// relay in another country instead of over a link that answers in milliseconds.
//
// What it costs: those addresses go into every record this device publishes, and while it is
// offering to pair it publishes under its own id, which anybody holding a ticket can read. Such a
// record says 192.168.1.24, or whatever a VPN or an overlay gave this machine, so a reader learns
// which networks this device is on and can watch them change as it moves. Off leaves the relay and
// the reflexive address, which say a great deal less.
func SetDirect(on bool) {
	settingsMu.Lock()
	defer settingsMu.Unlock()

	directSet = on
}

// Direct reports whether this machine's own addresses are published.
func Direct() bool {
	settingsMu.RLock()
	defer settingsMu.RUnlock()

	return directSet
}
