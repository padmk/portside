package remote

import (
	"errors"
	"runtime"
	"syscall"
)

// wildcardTaken reports whether another socket now holds port on all
// addresses (0.0.0.0 or ::), next to a mirror bound to loopback.
//
// On macOS a later bind of the wildcard address with SO_REUSEADDR succeeds
// beside the mirror's loopback sockets (docker publishing a port does this),
// yet 127.0.0.1 keeps reaching the mirror, the more specific address.
//
// Linux refuses that later bind while the mirror listens, so there is
// nothing to detect, and wildcardHeld would conflict with the mirror itself.
func wildcardTaken(port int) bool {
	return runtime.GOOS == "darwin" && wildcardHeld(port)
}

// wildcardHeld reports whether a socket of another process stands in the way
// of a mirror on port, by binding the wildcard addresses with SO_REUSEADDR.
// The sockets are bound, never listened on, and closed at once: listening on
// a non-loopback address would make the macOS firewall ask about incoming
// connections.
//
// With SO_REUSEADDR a bind ignores connections closing on the port
// (TIME_WAIT), which a mirror leaves behind for about 30 seconds after the
// other computer ends a connection. On macOS such a bind conflicts only with
// a socket bound to exactly the same wildcard address; listeners on 127.0.0.1
// or ::1 make the mirror's own binds fail. On Linux it conflicts with a
// listener on any address of the port.
func wildcardHeld(port int) bool {
	try := func(family int, sa syscall.Sockaddr) bool {
		fd, err := syscall.Socket(family, syscall.SOCK_STREAM, 0)
		if err != nil {
			// No such family (no IPv6): nothing can hold the port there.
			return false
		}
		defer syscall.Close(fd)
		if syscall.SetsockoptInt(fd, syscall.SOL_SOCKET, syscall.SO_REUSEADDR, 1) != nil {
			return true
		}
		if family == syscall.AF_INET6 && syscall.SetsockoptInt(fd, syscall.IPPROTO_IPV6, syscall.IPV6_V6ONLY, 1) != nil {
			return true
		}
		return errors.Is(syscall.Bind(fd, sa), syscall.EADDRINUSE)
	}
	return try(syscall.AF_INET, &syscall.SockaddrInet4{Port: port}) ||
		try(syscall.AF_INET6, &syscall.SockaddrInet6{Port: port})
}
