package share

import (
	"context"
	"net"
	"net/netip"
	"os"
	"runtime"
	"time"

	"github.com/padmk/portside/internal/ports"
)

// On Linux every local account can read the Serve configuration, path
// secret included (`tailscale serve status --json`), and could send the
// sharing listener requests with forged Tailscale headers. tailscaled runs
// as root, or as this account when started by it, so on Linux the listener
// accepts only connections whose client end belongs to one of those. On
// macOS lsof cannot see the sockets of Tailscale's root-owned network
// extension, so there is no such check.

// fromServe wraps ln so it drops connections from other local accounts
// where that can be told (Linux).
func fromServe(ln net.Listener) net.Listener {
	if runtime.GOOS != "linux" {
		return ln
	}
	return serveListener{ln, os.Getuid(), ports.ConnectionOwner}
}

type serveListener struct {
	net.Listener
	uid   int
	owner func(ctx context.Context, client, server netip.AddrPort) (ports.ConnOwner, error)
}

func (l serveListener) Accept() (net.Conn, error) {
	for {
		c, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		if l.allowed(c) {
			return c, nil
		}
		_ = c.Close()
	}
}

// allowed reports whether c comes from root or this account; when the
// owner cannot be found, it does not.
func (l serveListener) allowed(c net.Conn) bool {
	client, ok1 := c.RemoteAddr().(*net.TCPAddr)
	server, ok2 := c.LocalAddr().(*net.TCPAddr)
	if !ok1 || !ok2 {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	o, err := l.owner(ctx, client.AddrPort(), server.AddrPort())
	return err == nil && (o.UID == 0 || o.UID == l.uid)
}
