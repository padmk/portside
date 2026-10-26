package ports

import (
	"net/netip"
	"strings"
)

// Exposure says from where a listener can be reached, judged by the
// addresses it listens on.
type Exposure string

const (
	// Loopback listeners answer only on this computer.
	Loopback Exposure = "loopback"
	// Tailnet listeners answer only on Tailscale addresses.
	Tailnet Exposure = "tailnet"
	// Network listeners answer on all interfaces or on another address,
	// such as one of the local network.
	Network Exposure = "network"
)

var (
	tailnetV4 = netip.MustParsePrefix("100.64.0.0/10")
	tailnetV6 = netip.MustParsePrefix("fd7a:115c:a1e0::/48")
)

// rank orders exposures from the narrowest; Classify keeps the widest.
func (e Exposure) rank() int {
	switch e {
	case Loopback:
		return 1
	case Tailnet:
		return 2
	case Network:
		return 3
	}
	return 0
}

// Wider reports whether e reaches further than o; "" (unknown) is the
// narrowest.
func (e Exposure) Wider(o Exposure) bool { return e.rank() > o.rank() }

// Widest returns the widest of the exposures.
func Widest(es ...Exposure) Exposure {
	var out Exposure
	for _, e := range es {
		if e.Wider(out) {
			out = e
		}
	}
	return out
}

// Classify returns the widest exposure of listening addresses as lsof or
// ss write them ("127.0.0.1", "[::1]", "*", "0.0.0.0", "[fe80::1%lo0]"),
// or "" for none. An address it cannot parse counts as reachable from the
// network.
func Classify(addrs []string) Exposure {
	var out Exposure
	for _, a := range addrs {
		out = Widest(out, classifyOne(a))
	}
	return out
}

func classifyOne(addr string) Exposure {
	host := strings.TrimSuffix(strings.TrimPrefix(addr, "["), "]")
	host, zone, _ := strings.Cut(host, "%")
	if host == "" || host == "*" {
		return Network
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return Network
	}
	ip = ip.Unmap()
	switch {
	case ip.IsLoopback():
		return Loopback
	case ip.IsLinkLocalUnicast() && (zone == "lo0" || zone == "lo"):
		// fe80::1%lo0 is the loopback interface's own link-local address.
		return Loopback
	case tailnetV4.Contains(ip) || tailnetV6.Contains(ip):
		return Tailnet
	}
	return Network
}
