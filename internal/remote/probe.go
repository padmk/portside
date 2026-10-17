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
// yet 127.0.0.1 keeps reaching the mirror, the more specific address. Binding
// the wildcard here with SO_REUSEADDR conflicts only with an exact match, so
// it fails exactly when such a socket exists. The socket is bound, never
// listened on, and closed at once.
//
// Linux refuses that later bind while the mirror listens, so there is
// nothing to detect, and this probe would conflict with the mirror itself.
func wildcardTaken(port int) bool {
	if runtime.GOOS != "darwin" {
		return false
	}
	try := func(family int, sa syscall.Sockaddr) bool {
		fd, err := syscall.Socket(family, syscall.SOCK_STREAM, 0)
		if err != nil {
			return false
		}
		defer syscall.Close(fd)
		if syscall.SetsockoptInt(fd, syscall.SOL_SOCKET, syscall.SO_REUSEADDR, 1) != nil {
			return false
		}
		if family == syscall.AF_INET6 && syscall.SetsockoptInt(fd, syscall.IPPROTO_IPV6, syscall.IPV6_V6ONLY, 1) != nil {
			return false
		}
		return errors.Is(syscall.Bind(fd, sa), syscall.EADDRINUSE)
	}
	return try(syscall.AF_INET, &syscall.SockaddrInet4{Port: port}) ||
		try(syscall.AF_INET6, &syscall.SockaddrInet6{Port: port})
}
