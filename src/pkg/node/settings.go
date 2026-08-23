package node

import "sync"

// Settings a config may override.
//
// A setting the config never mentions is left alone, so the environment or a flag still decides
// it. That is why these are set rather than defaulted: there is no value here meaning "unset".
var (
	settingsMu    sync.RWMutex
	nameSet       string
	bootstrapSet  []string
	relaysSet     []string
	rendezvousSet bool
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

// SetRendezvous turns on publishing this device's address so paired peers can find it after it
// moves networks. Off unless a config asks for it: it writes to a relay this machine does not own.
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
