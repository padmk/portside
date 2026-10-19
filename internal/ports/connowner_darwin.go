package ports

import (
	"context"
	"net/netip"
	"os"
)

// ConnectionOwner returns who holds the client end of a loopback TCP
// connection from client to server. lsof run by an account sees only that
// account's processes, so a connection from another account is not found.
func ConnectionOwner(ctx context.Context, client, server netip.AddrPort) (ConnOwner, error) {
	client = netip.AddrPortFrom(client.Addr().Unmap(), client.Port())
	server = netip.AddrPortFrom(server.Addr().Unmap(), server.Port())
	// Both ends of the connection match the filter; lsofEntry picks the
	// client's by the socket's name.
	out, _ := run(ctx, "lsof", "-nP", "-w", "-iTCP@"+client.String(), "-F", "pun")
	pid, uid, found := lsofEntry(string(out), client, server)
	if !found {
		return ConnOwner{}, ErrNoOwner
	}
	return ConnOwner{UID: uid, Self: pid == os.Getpid()}, nil
}
