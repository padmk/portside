package ports

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"net/netip"
	"strings"
	"testing"
)

// procHex writes an address the way /proc/net/tcp does on this machine.
func procHex(ap netip.AddrPort) string {
	raw := ap.Addr().AsSlice()
	out := make([]byte, len(raw))
	for i := 0; i < len(raw); i += 4 {
		binary.BigEndian.PutUint32(out[i:], binary.NativeEndian.Uint32(raw[i:]))
	}
	return strings.ToUpper(hex.EncodeToString(out)) + fmt.Sprintf(":%04X", ap.Port())
}

func TestProcNetEntryFindsClientEnd(t *testing.T) {
	// As a little-endian kernel prints 127.0.0.1:8080 and [::1]:8080.
	if binary.NativeEndian.Uint16([]byte{1, 0}) == 1 {
		for s, want := range map[string]string{
			"0100007F:1F90":                         "127.0.0.1:8080",
			"00000000000000000000000001000000:1F90": "[::1]:8080",
		} {
			if got, ok := procNetAddr(s); !ok || got.String() != want {
				t.Errorf("procNetAddr(%s) = %v, want %s", s, got, want)
			}
		}
	}
	daemon := netip.MustParseAddrPort("127.0.0.1:7878")
	client := netip.MustParseAddrPort("127.0.0.1:52000")
	other := netip.MustParseAddrPort("127.0.0.1:52001")
	row := func(local, remote netip.AddrPort, uid, inode int) string {
		return fmt.Sprintf("   1: %s %s 01 00000000:00000000 00:00000000 00000000 %5d        0 %d 1 0000000000000000 20 4 30 10 -1",
			procHex(local), procHex(remote), uid, inode)
	}
	table := strings.Join([]string{
		"  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode",
		row(daemon, client, 1000, 11), // the daemon's end
		row(other, daemon, 1000, 12),  // someone else's connection
		row(client, daemon, 1001, 13), // the client's end
	}, "\n")
	uid, inode, ok := procNetEntry(strings.NewReader(table), client, daemon)
	if !ok || uid != 1001 || inode != "13" {
		t.Errorf("got uid %d inode %q found %v; want the client's end (1001, 13)", uid, inode, ok)
	}
	if _, _, ok := procNetEntry(strings.NewReader(table), netip.MustParseAddrPort("127.0.0.1:1"), daemon); ok {
		t.Error("found a connection that is not in the table")
	}

	// IPv6, and an IPv4 client on a dual-stack socket, listed in tcp6.
	v6d, v6c := netip.MustParseAddrPort("[::1]:7878"), netip.MustParseAddrPort("[::1]:52002")
	mapped := netip.AddrPortFrom(netip.AddrFrom16(client.Addr().As16()), client.Port())
	table6 := strings.Join([]string{"header", row(v6c, v6d, 1002, 14), row(mapped, daemon, 1003, 15)}, "\n")
	if uid, _, ok := procNetEntry(strings.NewReader(table6), v6c, v6d); !ok || uid != 1002 {
		t.Errorf("IPv6: uid %d found %v", uid, ok)
	}
	if uid, _, ok := procNetEntry(strings.NewReader(table6), client, daemon); !ok || uid != 1003 {
		t.Errorf("IPv4-mapped: uid %d found %v", uid, ok)
	}
}

func TestLsofEntryFindsClientEnd(t *testing.T) {
	daemon := netip.MustParseAddrPort("[::1]:7878")
	client := netip.MustParseAddrPort("[::1]:52000")
	out := strings.Join([]string{
		"p100", "u501", "f12", "n[::1]:7878->[::1]:52000", // the daemon's end
		"p200", "u501", "f30", "n[::1]:52000->[::1]:7878", // the browser's end
		"",
	}, "\n")
	if pid, uid, ok := lsofEntry(out, client, daemon); !ok || pid != 200 || uid != 501 {
		t.Errorf("got pid %d uid %d found %v; want the browser's end", pid, uid, ok)
	}
	if _, _, ok := lsofEntry("p100\nu501\nn[::1]:7878->[::1]:52000\n", client, daemon); ok {
		t.Error("the daemon's own end taken for the client's")
	}
}
