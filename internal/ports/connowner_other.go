//go:build !linux && !darwin

package ports

import (
	"context"
	"net/netip"
)

// ConnectionOwner cannot tell who holds a connection on this platform.
func ConnectionOwner(context.Context, netip.AddrPort, netip.AddrPort) (ConnOwner, error) {
	return ConnOwner{}, ErrUnsupported
}
