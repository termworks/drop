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

// A ticket says who, and nothing about where.
//
// An address in an invitation is a guess about somebody else's network that goes stale the moment
// a laptop moves, and every one of them is twenty more characters to type and a bigger code to
// draw. Finding a device is drop's job.
func TestATicketCarriesNoAddresses(t *testing.T) {
	invite := ticketFor(idFor(1), "abcd-efgh-ijkl")

	if strings.Count(invite, "#") != 1 {
		t.Fatalf("a ticket carries more than who and a code: %s", invite)
	}
	for _, looksLikeAnAddress := range []string{".", ":"} {
		if strings.Contains(strings.SplitN(invite, "#", 2)[1], looksLikeAnAddress) {
			t.Fatalf("an address got into the ticket: %s", invite)
		}
	}
}

// Whatever goes in has to come back out, or pairing fails on a ticket that looked fine.
func TestATicketRoundTrips(t *testing.T) {
	want := idFor(7)
	invite := ticketFor(want, "abcd-efgh-ijkl")

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
	if len(addrs) != 0 {
		t.Fatalf("addrs = %v, want none", addrs)
	}
}

// Tickets written by an older version still carry addresses, and still have to pair.
func TestAnOlderTicketWithAddressesStillReads(t *testing.T) {
	invite := idFor(7).String() + "#abcd-efgh-ijkl#192.168.1.157:47777,10.0.0.2:47777"

	id, code, addrs, err := readTicket(invite)
	if err != nil {
		t.Fatalf("reading: %v", err)
	}
	if id != idFor(7) || code != "abcd-efgh-ijkl" {
		t.Fatalf("id = %s, code = %q", id, code)
	}
	if len(addrs) != 2 || addrs[0] != at("192.168.1.157:47777") {
		t.Fatalf("addrs = %v", addrs)
	}
}
