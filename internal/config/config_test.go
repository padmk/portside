package config

import (
	"slices"
	"strings"
	"testing"
)

func TestValidName(t *testing.T) {
	good := []string{"a", "web", "web-2", "0day", "a-b-c", strings.Repeat("a", 63)}
	bad := []string{"", "Web", "web_ui", "-web", "web-", "web.ui", "web ui", "wéb", "a/b", strings.Repeat("a", 64)}
	for _, n := range good {
		if err := ValidName(n); err != nil {
			t.Errorf("ValidName(%q) = %v, want nil", n, err)
		}
	}
	for _, n := range bad {
		if err := ValidName(n); err == nil {
			t.Errorf("ValidName(%q) = nil, want an error", n)
		}
	}
}

func TestReservedPorts(t *testing.T) {
	// macOS AirPlay Receiver.
	for _, p := range []int{5000, 7000} {
		if !Reserved[p] {
			t.Errorf("port %d (AirPlay) is not reserved", p)
		}
	}
	// Browser-blocked ports that dev servers could otherwise be handed.
	for _, p := range []int{4045, 5060, 5061, 6000, 6566, 6665, 6667, 6697, 10080} {
		if !Reserved[p] {
			t.Errorf("port %d (blocked by browsers) is not reserved", p)
		}
	}
	if Reserved[AutoPortBase] || Reserved[3000] {
		t.Error("the automatic base and 3000 must not be reserved")
	}
}

func TestFreePortLowestFree(t *testing.T) {
	c := &Config{Port: 7878}
	if got := c.FreePort(nil); got != AutoPortBase {
		t.Errorf("FreePort(nil) = %d, want %d", got, AutoPortBase)
	}
}

func TestFreePortSkipsUsed(t *testing.T) {
	c := &Config{
		Port:    AutoPortBase + 2,
		Servers: []Server{{Name: "a", Port: AutoPortBase}, {Name: "b", Port: AutoPortBase + 1}, {Name: "c", Port: 0}},
	}
	// 3001 and 3002 are servers, 3003 is the daemon's own port, 3004 is busy.
	got := c.FreePort(func(p int) bool { return p == AutoPortBase+3 })
	if want := AutoPortBase + 4; got != want {
		t.Errorf("FreePort = %d, want %d", got, want)
	}
}

func TestFreePortSkipsReserved(t *testing.T) {
	c := &Config{Port: 7878}
	for r := range Reserved {
		if r < AutoPortBase {
			continue
		}
		// Everything below r is busy, so r is the first candidate.
		got := c.FreePort(func(p int) bool { return p < r })
		want := r + 1
		for Reserved[want] || want == c.Port {
			want++
		}
		if got != want {
			t.Errorf("busy below %d: FreePort = %d, want %d", r, got, want)
		}
		if Reserved[got] {
			t.Errorf("FreePort returned reserved port %d", got)
		}
	}
}

func TestFreePortAirPlay(t *testing.T) {
	c := &Config{Port: 7878}
	if got := c.FreePort(func(p int) bool { return p < 5000 }); got != 5001 {
		t.Errorf("FreePort with 5000 next = %d, want 5001", got)
	}
	// Only the AirPlay ports are idle, and they are never handed out.
	if got := c.FreePort(func(p int) bool { return p != 5000 && p != 7000 }); got != 0 {
		t.Errorf("FreePort with only AirPlay ports idle = %d, want 0", got)
	}
}

func TestFreePortExhausted(t *testing.T) {
	c := &Config{Port: 7878}
	if got := c.FreePort(func(int) bool { return true }); got != 0 {
		t.Errorf("FreePort with every port busy = %d, want 0", got)
	}
}

func TestParseAutomaticPorts(t *testing.T) {
	text := `
port = 3002

[servers.a]
cmd = "a"

[servers.b]
cmd = "b"
port = 3003

[servers.c]
cmd = "c"

[servers.d]
cmd = "d"
port = 0
`
	cfg, err := Parse("/tmp/portside.toml", []byte(text))
	if err != nil {
		t.Fatal(err)
	}
	var got []int
	for _, s := range cfg.Servers {
		got = append(got, s.Port)
	}
	// a takes 3001, 3002 is the daemon, 3003 is b's, c takes 3004, d has no port.
	if want := []int{3001, 3003, 3004, 0}; !slices.Equal(got, want) {
		t.Errorf("ports = %v, want %v", got, want)
	}
	if !cfg.Servers[0].PortAuto || cfg.Servers[1].PortAuto || cfg.Servers[3].PortAuto {
		t.Error("PortAuto should be set only for servers without a port key")
	}
}

func TestParseRejectsInvalid(t *testing.T) {
	cases := map[string]string{
		"missing cmd":      "[servers.a]\ndir = \"x\"\n",
		"bad name":         "[servers.Web]\ncmd = \"x\"\n",
		"bad restart":      "[servers.a]\ncmd = \"x\"\nrestart = \"sometimes\"\n",
		"port range":       "[servers.a]\ncmd = \"x\"\nport = 70000\n",
		"daemon port":      "port = 4000\n[servers.a]\ncmd = \"x\"\nport = 4000\n",
		"unknown dep":      "[servers.a]\ncmd = \"x\"\ndepends_on = [\"b\"]\n",
		"dependency cycle": "[servers.a]\ncmd = \"x\"\ndepends_on = [\"b\"]\n[servers.b]\ncmd = \"x\"\ndepends_on = [\"a\"]\n",
		"toml syntax":      "[servers.a\n",
	}
	for name, text := range cases {
		if _, err := Parse("/tmp/portside.toml", []byte(text)); err == nil {
			t.Errorf("%s: Parse accepted %q", name, text)
		}
	}
}
