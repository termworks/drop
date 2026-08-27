package dial

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strings"

	"github.com/tmc/go-iroh/netaddr"

	"github.com/bresilla/drop/src/pkg/book"
)

// What a failed dial actually means.
//
// The transport has one answer for everything: nothing came back. `timeout: no recent network
// activity` after twenty-three seconds reads as "that machine is off", and it usually is not. It is
// this process being unreachable because another drop on this machine holds its port; or the far
// end having restarted onto a NAT mapping it has not published yet; or there having been no address
// to try in the first place. Those are three different things to do next, and the transport's
// wording suggests none of them.

// unreachable says what was tried and what to do about it. wrong is whatever is known to be wrong
// with this end, from node.Trouble, and empty when nothing is.
func unreachable(wrong string, entry book.Entry, at netaddr.EndpointAddr, err error) error {
	if wrong != "" {
		return fmt.Errorf("reaching %s: %s: %w", entry.Name, wrong, err)
	}

	// What was actually dialled, not everything the far end said about itself: an address this
	// machine could never have reached is not one it tried, and naming it sends somebody looking
	// in the wrong place.
	tried := Nearest(at.IPAddrs())
	switch {
	case len(tried) == 0 && len(at.RelayURLs()) == 0:
		return fmt.Errorf("reaching %s: nowhere to try: it has never said where it is, and neither this network nor the rendezvous knows: %w", entry.Name, err)

	case timedOut(err):
		return fmt.Errorf("reaching %s: nothing answered at %s: it is off, or it restarted and moved — a device that restarts says where it is again within a minute, so try again: %w",
			entry.Name, listing(tried), err)
	}
	return fmt.Errorf("reaching %s: %w", entry.Name, err)
}

// timedOut reports whether a dial ended by waiting rather than by being refused.
func timedOut(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}

	var waited net.Error
	return errors.As(err, &waited) && waited.Timeout()
}

// mostNamed is how many addresses a failure reads out before it starts counting them instead.
//
// A machine on a laptop with a VPN, a container bridge and two overlays advertises a dozen, and a
// dozen of them on one line is a wall nobody reads. The first few are the ones worth trying by
// hand; the rest are a number.
const mostNamed = 4

// listing is the addresses that were tried, as somebody would read them out.
func listing(tried []netip.AddrPort) string {
	if len(tried) == 0 {
		return "its relay"
	}

	rest := 0
	if len(tried) > mostNamed {
		rest, tried = len(tried)-mostNamed, tried[:mostNamed]
	}

	out := make([]string, 0, len(tried))
	for _, at := range tried {
		out = append(out, at.String())
	}
	said := strings.Join(out, ", ")

	switch rest {
	case 0:
		return said
	case 1:
		return said + " and one more"
	}
	return fmt.Sprintf("%s and %d more", said, rest)
}
