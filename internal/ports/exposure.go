package ports

import (
	"net/netip"
	"slices"
	"strings"
)

// Exposure says from where a listener can be reached, judged by the
// addresses it listens on.
type Exposure string

const (
	// Loopback listeners answer only on this computer.
	Loopback Exposure = "loopback"
	// Tailnet listeners answer only on this machine's own Tailscale
	// addresses.
	Tailnet Exposure = "tailnet"
	// Network listeners answer on all interfaces or on another address,
	// such as one of the local network.
	Network Exposure = "network"
)

// Tailscale assigns addresses from these ranges, but 100.64.0.0/10 is also
// carrier-grade NAT space that some networks give devices directly, so an
// address in them counts as tailnet only when it is one of this machine's
// Tailscale addresses.
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

// TailnetAddrs returns this machine's own Tailscale addresses, or none when
// Tailscale is unavailable. Classify calls it only for an address in
// Tailscale's ranges; a nil TailnetAddrs knows no addresses.
type TailnetAddrs func() []netip.Addr

// Resolved calls own at most once, when an address of the lists is in
// Tailscale's ranges, and returns a TailnetAddrs that answers that result,
// so a caller can classify under a lock without waiting for Tailscale.
func (own TailnetAddrs) Resolved(lists ...[]string) TailnetAddrs {
	for _, l := range lists {
		for _, a := range l {
			if ip, _, ok := parseListen(a); ok && inTailnetRange(ip) {
				var got []netip.Addr
				if own != nil {
					got = own()
				}
				return func() []netip.Addr { return got }
			}
		}
	}
	return nil
}

// Classify returns the widest exposure of listening addresses as lsof or
// ss write them ("127.0.0.1", "[::1]", "*", "0.0.0.0", "[fe80::1%lo0]"),
// or "" for none. own gives this machine's Tailscale addresses; any other
// address, also one in Tailscale's ranges, and one it cannot parse count as
// reachable from the network.
func Classify(addrs []string, own TailnetAddrs) Exposure {
	var out Exposure
	var self []netip.Addr
	looked := false
	for _, a := range addrs {
		e := Network
		if ip, zone, ok := parseListen(a); ok {
			switch {
			case ip.IsLoopback():
				e = Loopback
			case ip.IsLinkLocalUnicast() && (zone == "lo0" || zone == "lo"):
				// fe80::1%lo0 is the loopback interface's own link-local address.
				e = Loopback
			case inTailnetRange(ip):
				if !looked && own != nil {
					self, looked = own(), true
				}
				if slices.ContainsFunc(self, func(s netip.Addr) bool { return s.Unmap() == ip }) {
					e = Tailnet
				}
			}
		}
		out = Widest(out, e)
	}
	return out
}

// parseListen parses one listening address; ok is false for a wildcard
// ("", "*") or text that is no address.
func parseListen(addr string) (ip netip.Addr, zone string, ok bool) {
	host := strings.TrimSuffix(strings.TrimPrefix(addr, "["), "]")
	host, zone, _ = strings.Cut(host, "%")
	if host == "" || host == "*" {
		return netip.Addr{}, "", false
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return netip.Addr{}, "", false
	}
	return ip.Unmap(), zone, true
}

func inTailnetRange(ip netip.Addr) bool {
	return tailnetV4.Contains(ip) || tailnetV6.Contains(ip)
}
