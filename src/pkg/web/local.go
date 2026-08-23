package web

import (
	"net"
	"net/http"
)

// localRequest reports whether a request came from this machine.
//
// Checked on the connection rather than on a header: X-Forwarded-For is written by whoever is in
// front, and there is not supposed to be anything in front of this.
func localRequest(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}

	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
