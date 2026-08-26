package dial

import (
	"errors"
	"net"
	"net/netip"
	"strings"
	"testing"

	"github.com/tmc/go-iroh/netaddr"

	"github.com/bresilla/drop/src/pkg/book"
)

// remembering builds an entry whose first written address is the one that answered last time.
func remembering(seed byte, written ...string) book.Entry {
	entry := entryFor(seed)
	entry.Addrs = written
	return entry
}

func advertising(seed byte, written ...string) netaddr.EndpointAddr {
	at := netaddr.NewEndpointAddr(idFor(seed))
	for _, one := range written {
		at = at.WithIP(netip.MustParseAddrPort(one))
	}
	return at
}

// A remembered address must not outrank a nearer one.
//
// It is a guess about somewhere the device used to be. When its NAT mapping has expired, or the
// address now belongs to somebody else's machine, trying it first costs a whole timeout on every
// dial before the address that was always going to work is reached.
func TestARememberedAddressDoesNotOutrankANearerOne(t *testing.T) {
	nets := subnets()
	if len(nets) == 0 {
		t.Skip("this machine has no ordinary networks to compare against")
	}

	here := netip.AddrPortFrom(nets[0].Addr().Next(), 47777)
	if !onOurWire(here.Addr()) {
		t.Skip("this machine has no subnet to place an address in")
	}
	away := netip.MustParseAddrPort("203.0.113.7:47777")

	got := worthTrying(advertising(9, here.String(), away.String()), remembering(9, away.String()))

	if len(got) == 0 || got[0] != here {
		t.Fatalf("tried %v first, want the one on our own wire %v", got, here)
	}
}

// Among addresses that are equally far away, the one that answered last time is the better guess:
// finding a device is expensive and the answer rarely changes between two conversations.
func TestTheRememberedAddressWinsAmongEquals(t *testing.T) {
	first := netip.MustParseAddrPort("10.8.67.112:47777")
	worked := netip.MustParseAddrPort("10.8.67.200:47777")

	got := worthTrying(advertising(10, first.String(), worked.String()), remembering(10, worked.String()))

	if len(got) == 0 || got[0] != worked {
		t.Fatalf("tried %v first, want the one that answered last time %v", got, worked)
	}
}

// Only two get an attempt of their own. A peer that advertises every address it has would otherwise
// spend three seconds each before the full attempt — which has the relay as well — is ever made.
func TestOnlyTheBestFewAreTriedOnTheirOwn(t *testing.T) {
	at := advertising(11, "10.0.0.1:47777", "10.0.0.2:47777", "10.0.0.3:47777", "10.0.0.4:47777")

	if got := worthTrying(at, entryFor(11)); len(got) > atMost {
		t.Fatalf("tried %d addresses on their own, want at most %d", len(got), atMost)
	}
}

// One address is already the whole attempt, so there is nothing to try first.
func TestASingleAddressIsNotWorthNarrowingDown(t *testing.T) {
	if got := worthTrying(advertising(12, "10.0.0.1:47777"), entryFor(12)); len(got) != 0 {
		t.Fatalf("narrowed one address down to %v", got)
	}
}

// A far-away address is left to the full attempt: it cannot answer straight away, and the full
// attempt has the relay too. Spending three seconds discovering that is three seconds wasted.
func TestDistantAddressesAreLeftToTheFullAttempt(t *testing.T) {
	at := advertising(13, "203.0.113.7:47777", "198.51.100.9:47777")

	if got := worthTrying(at, entryFor(13)); len(got) != 0 {
		t.Fatalf("gave %v an attempt of its own", got)
	}
}

// A dial that ends in a transport timeout says nothing anybody can act on. When this end cannot be
// reached at all, that is the thing to say, and the way out has to be named.
func TestAPortClashIsSaidOutLoud(t *testing.T) {
	wrong := "another process on this machine holds port 47777, so this one bound elsewhere and cannot be reached; stop it, or set DROP_PORT to a free port"

	timeout := errors.New("timeout: no recent network activity")
	err := unreachable(wrong, entryFor(14), advertising(14, "10.0.0.1:47777"), timeout)

	if !strings.Contains(err.Error(), "DROP_PORT") {
		t.Errorf("the way out was not named: %v", err)
	}
	if !errors.Is(err, timeout) {
		t.Error("what the transport said was thrown away")
	}
}

// Otherwise a timeout says what was tried and that a restarted device comes back by itself, which
// is what it usually turns out to be.
func TestATimeoutSaysWhatWasTried(t *testing.T) {
	at := advertising(15, "192.168.1.5:47777")
	err := unreachable("", entryFor(15), at, waited{})

	for _, want := range []string{"192.168.1.5:47777", "restarted", "alpha"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("%q is missing from %v", want, err)
		}
	}
}

// And a device nobody has ever had an address for is a different problem entirely: there was
// nothing to try, so there is nothing to try again.
func TestNowhereToTryIsSaidAsSuch(t *testing.T) {
	err := unreachable("", entryFor(16), netaddr.NewEndpointAddr(idFor(16)), waited{})

	if !strings.Contains(err.Error(), "nowhere to try") {
		t.Errorf("said %v", err)
	}
}

// A refusal is not a timeout, and dressing it up as one would send somebody looking for a network
// problem that is not there.
func TestSomethingOtherThanATimeoutIsLeftAlone(t *testing.T) {
	refused := errors.New("connection refused")
	err := unreachable("", entryFor(17), advertising(17, "10.0.0.1:47777"), refused)

	if strings.Contains(err.Error(), "restarted") {
		t.Errorf("a refusal was explained as a timeout: %v", err)
	}
	if !errors.Is(err, refused) {
		t.Error("what the transport said was thrown away")
	}
}

// waited is what the transport gives back when nothing answered.
type waited struct{}

func (waited) Error() string   { return "timeout: no recent network activity" }
func (waited) Timeout() bool   { return true }
func (waited) Temporary() bool { return true }

var _ net.Error = waited{}

// A failure is read by somebody deciding what to do next, so it names what was actually dialled and
// stops before it becomes a wall. A laptop with a VPN, a bridge and two overlays advertises a dozen
// addresses, and a dozen on one line is a line nobody reads.
func TestAFailureNamesWhatWasTriedAndNoMore(t *testing.T) {
	many := advertising(18,
		"10.0.0.1:47777", "10.0.0.2:47777", "10.0.0.3:47777",
		"10.0.0.4:47777", "10.0.0.5:47777", "10.0.0.6:47777",
		// Neither of these is somewhere a peer can be reached, so neither was tried.
		"169.254.9.9:47777", "127.0.0.1:47777",
	)

	said := unreachable("", entryFor(18), many, waited{}).Error()

	for _, never := range []string{"169.254.9.9", "127.0.0.1"} {
		if strings.Contains(said, never) {
			t.Errorf("the failure names %s, which was never dialled:\n%s", never, said)
		}
	}
	if !strings.Contains(said, "and 2 more") {
		t.Errorf("six addresses were read out in full:\n%s", said)
	}
	if n := strings.Count(said, "10.0.0."); n != mostNamed {
		t.Errorf("%d addresses named, want %d:\n%s", n, mostNamed, said)
	}
}
