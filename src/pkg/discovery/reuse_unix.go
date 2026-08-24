//go:build unix

package discovery

import (
	"syscall"

	"golang.org/x/sys/unix"
)

// reuseAddr allows several drop instances on one machine to share the port.
//
// SO_REUSEADDR only, deliberately: adding SO_REUSEPORT would put them in a load-balancing group
// where the kernel gives each datagram to exactly one of them, which is the opposite of what a
// multicast listener needs.
func reuseAddr(_, _ string, c syscall.RawConn) error {
	var failed error

	err := c.Control(func(fd uintptr) {
		if err := unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_REUSEADDR, 1); err != nil {
			failed = err
		}
	})
	if err != nil {
		return err
	}
	return failed
}
