package ports

import (
	"slices"
	"testing"
)

func TestClassify(t *testing.T) {
	cases := []struct {
		addrs []string
		want  Exposure
	}{
		{nil, ""},
		{[]string{"127.0.0.1"}, Loopback},
		{[]string{"127.0.0.53"}, Loopback},
		{[]string{"[::1]"}, Loopback},
		{[]string{"::1"}, Loopback},
		{[]string{"127.0.0.1", "[::1]"}, Loopback},
		{[]string{"[::ffff:127.0.0.1]"}, Loopback},
		{[]string{"[fe80::1%lo0]"}, Loopback},
		{[]string{"100.101.102.103"}, Tailnet},
		{[]string{"[fd7a:115c:a1e0::1234]"}, Tailnet},
		{[]string{"127.0.0.1", "100.64.0.1"}, Tailnet},
		{[]string{"*"}, Network},
		{[]string{"0.0.0.0"}, Network},
		{[]string{"[::]"}, Network},
		{[]string{""}, Network},
		{[]string{"192.168.1.20"}, Network},
		{[]string{"[fe80::1%en0]"}, Network},
		{[]string{"[2001:db8::1]"}, Network},
		{[]string{"100.128.0.1"}, Network}, // just outside 100.64.0.0/10
		{[]string{"[fd7a:115c:a1e1::1]"}, Network},
		{[]string{"127.0.0.1", "*"}, Network},
		{[]string{"[::1]", "100.64.0.1", "10.0.0.2"}, Network},
		{[]string{"not-an-address"}, Network},
	}
	for _, c := range cases {
		if got := Classify(c.addrs); got != c.want {
			t.Errorf("Classify(%q) = %q, want %q", c.addrs, got, c.want)
		}
	}
}

// One process listening on loopback and on all interfaces at one port must
// keep both addresses, so the exposed one is not lost.
func TestParseLsofMergesAddresses(t *testing.T) {
	out := "p100\ncnode\nn127.0.0.1:3000\nn*:3000\nn[::1]:3000\nn127.0.0.1:3000\np200\ncpython3\nn127.0.0.1:8000\n"
	got := parseLsof([]byte(out))
	if len(got) != 2 {
		t.Fatalf("got %d listeners, want 2: %+v", len(got), got)
	}
	if got[0].Port != 3000 || got[0].PID != 100 || !slices.Equal(got[0].Addresses, []string{"127.0.0.1", "*", "[::1]"}) {
		t.Errorf("first listener = %+v", got[0])
	}
	if Classify(got[0].Addresses) != Network || Classify(got[1].Addresses) != Loopback {
		t.Errorf("exposures = %q, %q", Classify(got[0].Addresses), Classify(got[1].Addresses))
	}
}

func TestParseSSMergesAddresses(t *testing.T) {
	out := `LISTEN 0 511 127.0.0.1:5173 0.0.0.0:* users:(("node",pid=42,fd=20))
LISTEN 0 511 [::]:5173 [::]:* users:(("node",pid=42,fd=21))
LISTEN 0 4096 127.0.0.53%lo:53 0.0.0.0:* users:(("resolved",pid=7,fd=14))
`
	got := parseSS(out)
	if len(got) != 2 {
		t.Fatalf("got %d listeners, want 2: %+v", len(got), got)
	}
	if got[0].Port != 53 || Classify(got[0].Addresses) != Loopback {
		t.Errorf("port 53 = %+v", got[0])
	}
	if got[1].Port != 5173 || !slices.Equal(got[1].Addresses, []string{"127.0.0.1", "[::]"}) || Classify(got[1].Addresses) != Network {
		t.Errorf("port 5173 = %+v", got[1])
	}
}
