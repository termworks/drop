//go:build !unix

package discovery

import "syscall"

// reuseAddr does nothing where the option does not exist. A browser has no multicast to share.
func reuseAddr(_, _ string, _ syscall.RawConn) error { return nil }
