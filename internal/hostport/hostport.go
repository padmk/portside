// Package hostport reads the host out of a Host header or a host:port pair,
// the one form every host check compares.
package hostport

import (
	"net"
	"strings"
)

// Host returns the host of hostport in lower case and without the brackets
// of an IPv6 literal; a value without a port is taken as the host.
func Host(hostport string) string {
	h := hostport
	if host, _, err := net.SplitHostPort(hostport); err == nil {
		h = host
	}
	return strings.ToLower(strings.Trim(h, "[]"))
}
