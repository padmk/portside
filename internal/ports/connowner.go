package ports

import (
	"bufio"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"io"
	"net/netip"
	"strconv"
	"strings"
)

// ConnOwner is who holds the client end of a TCP connection to this process.
type ConnOwner struct {
	UID int
	// Self is true when the client end belongs to this process too, as for
	// a connection the daemon opened to itself.
	Self bool
}

// ErrNoOwner means the client end of a connection was not found, as for a
// connection from a process of another account that lsof cannot see.
var ErrNoOwner = errors.New("the other end of the connection was not found")

// ErrUnsupported means this platform cannot tell who holds a connection.
var ErrUnsupported = errors.New("connection owners cannot be looked up on this platform")

// procNetEntry finds the established connection whose local address is
// client and remote address server in a /proc/net/tcp or tcp6 table, and
// returns its uid and socket inode.
func procNetEntry(table io.Reader, client, server netip.AddrPort) (uid int, inode string, found bool) {
	sc := bufio.NewScanner(table)
	sc.Scan() // header
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		// Only established connections: a closed one in TIME_WAIT can list
		// the same addresses, with no owner.
		if len(f) < 10 || f[3] != "01" {
			continue
		}
		local, ok1 := procNetAddr(f[1])
		remote, ok2 := procNetAddr(f[2])
		if !ok1 || !ok2 || local != client || remote != server {
			continue
		}
		u, err := strconv.Atoi(f[7])
		if err != nil {
			continue
		}
		return u, f[9], true
	}
	return 0, "", false
}

// procNetAddr parses "0100007F:1F90": the address as 32-bit words in host
// byte order, the port in hex. IPv4-mapped IPv6 addresses come back as IPv4.
func procNetAddr(s string) (netip.AddrPort, bool) {
	a, p, ok := strings.Cut(s, ":")
	if !ok || (len(a) != 8 && len(a) != 32) {
		return netip.AddrPort{}, false
	}
	port, err := strconv.ParseUint(p, 16, 16)
	if err != nil {
		return netip.AddrPort{}, false
	}
	raw, err := hex.DecodeString(a)
	if err != nil {
		return netip.AddrPort{}, false
	}
	out := make([]byte, len(raw))
	for i := 0; i < len(raw); i += 4 {
		binary.NativeEndian.PutUint32(out[i:], binary.BigEndian.Uint32(raw[i:]))
	}
	ip, _ := netip.AddrFromSlice(out)
	return netip.AddrPortFrom(ip.Unmap(), uint16(port)), true
}

// lsofEntry finds, in `lsof -F pun` output, the process holding a socket
// named "<client>-><server>" and returns its pid and uid.
func lsofEntry(out string, client, server netip.AddrPort) (pid, uid int, found bool) {
	want := client.String() + "->" + server.String()
	for _, line := range strings.Split(out, "\n") {
		if line == "" {
			continue
		}
		switch line[0] {
		case 'p':
			pid, _ = strconv.Atoi(line[1:])
			uid = -1
		case 'u':
			uid, _ = strconv.Atoi(line[1:])
		case 'n':
			if line[1:] == want && pid > 0 && uid >= 0 {
				return pid, uid, true
			}
		}
	}
	return 0, 0, false
}
