package discovery

import (
	"net/netip"
	"testing"
)

func FuzzDecodeAnnounce(f *testing.F) {
	f.Add(encodeAnnounce("peer", []netip.AddrPort{netip.MustParseAddrPort("127.0.0.1:443")}))
	f.Add([]byte{})
	f.Add([]byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x7f})

	f.Fuzz(func(t *testing.T, packet []byte) {
		id, addrs, ok := decodeAnnounce(packet)
		if !ok {
			return
		}
		if len(id) > 256 || len(addrs) > maxAddrs {
			t.Fatalf("decoded a %d-byte id and %d addresses", len(id), len(addrs))
		}
	})
}
