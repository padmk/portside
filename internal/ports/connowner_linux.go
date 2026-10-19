package ports

import (
	"context"
	"net/netip"
	"os"
	"strconv"
)

// ConnectionOwner returns who holds the client end of a loopback TCP
// connection from client to server, from /proc/net/tcp and tcp6, which list
// the sockets of every account with their uid.
func ConnectionOwner(_ context.Context, client, server netip.AddrPort) (ConnOwner, error) {
	client = netip.AddrPortFrom(client.Addr().Unmap(), client.Port())
	server = netip.AddrPortFrom(server.Addr().Unmap(), server.Port())
	for _, table := range []string{"/proc/net/tcp", "/proc/net/tcp6"} {
		f, err := os.Open(table)
		if err != nil {
			continue
		}
		uid, inode, found := procNetEntry(f, client, server)
		f.Close()
		if found {
			return ConnOwner{UID: uid, Self: ownSocket(inode)}, nil
		}
	}
	return ConnOwner{}, ErrNoOwner
}

// ownSocket reports whether this process holds the socket with inode. When
// that cannot be told, it says yes, so the connection is not trusted.
func ownSocket(inode string) bool {
	if n, err := strconv.ParseUint(inode, 10, 64); err != nil || n == 0 {
		return true
	}
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		return true
	}
	want := "socket:[" + inode + "]"
	for _, e := range entries {
		if l, err := os.Readlink("/proc/self/fd/" + e.Name()); err == nil && l == want {
			return true
		}
	}
	return false
}
