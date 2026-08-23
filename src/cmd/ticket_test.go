package cmd

import (
	"net/netip"
	"strings"
	"testing"
)

func at(text string) netip.AddrPort {
	parsed, err := netip.ParseAddrPort(text)
	if err != nil {
		panic(err)
	}
	return parsed
}

// The length of a ticket decides how big its code comes out, and a code too tall for the window is
// one nobody can scan. Four addresses was enough to overflow an ordinary terminal.
func TestATicketCarriesOnlyAFewAddresses(t *testing.T) {
	invite := ticketFor(idFor(1), "abcd-efgh-ijkl", []netip.AddrPort{
		at("192.168.122.1:47777"),
		at("10.179.34.100:47777"),
		at("192.168.1.157:47777"),
		at("100.68.8.159:47777"),
	})

	_, addrs, found := strings.Cut(invite, "#")
	if !found {
		t.Fatalf("no code in %q", invite)
	}
	_, list, found := strings.Cut(addrs, "#")
	if !found {
		t.Fatalf("no addresses in %q", invite)
	}

	if n := len(strings.Split(list, ",")); n > MaxTicketAddrs {
		t.Fatalf("%d addresses, want at most %d: %s", n, MaxTicketAddrs, list)
	}
}

// A virtual bridge is real here and meaningless there: every machine running libvirt has the same
// 192.168.122.1, so leading with it sends the far end to itself.
func TestAVirtualBridgeIsNotOfferedFirst(t *testing.T) {
	invite := ticketFor(idFor(1), "abcd-efgh-ijkl", []netip.AddrPort{
		at("192.168.122.1:47777"),
		at("172.17.0.1:47777"),
		at("192.168.1.157:47777"),
	})

	if !strings.Contains(invite, "192.168.1.157") {
		t.Fatalf("the real address was left out: %s", invite)
	}
	if strings.Contains(invite, "192.168.122.1") || strings.Contains(invite, "172.17.0.1") {
		t.Fatalf("a virtual bridge crowded out something usable: %s", invite)
	}
}

func TestATicketWithNoAddressesIsStillValid(t *testing.T) {
	invite := ticketFor(idFor(1), "abcd-efgh-ijkl", nil)

	if strings.Count(invite, "#") != 1 {
		t.Fatalf("unexpected shape: %s", invite)
	}
	if _, _, _, err := readTicket(invite); err != nil {
		t.Fatalf("it does not read back: %v", err)
	}
}

// Whatever goes in has to come back out, or pairing fails on a ticket that looked fine.
func TestATicketRoundTrips(t *testing.T) {
	want := idFor(7)
	invite := ticketFor(want, "abcd-efgh-ijkl", []netip.AddrPort{at("192.168.1.157:47777")})

	id, code, addrs, err := readTicket(invite)
	if err != nil {
		t.Fatalf("reading: %v", err)
	}
	if id != want {
		t.Fatalf("id = %s", id)
	}
	if code != "abcd-efgh-ijkl" {
		t.Fatalf("code = %q", code)
	}
	if len(addrs) != 1 || addrs[0].String() != "192.168.1.157:47777" {
		t.Fatalf("addrs = %v", addrs)
	}
}
