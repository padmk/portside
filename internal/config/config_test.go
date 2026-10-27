package config

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/padmk/portside/internal/app"
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

func TestParseShare(t *testing.T) {
	for text, want := range map[string]bool{"": false, "share = true\n": true, "share = false\n": false} {
		cfg, err := Parse("/tmp/portside.toml", []byte(text))
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Share != want {
			t.Errorf("%q: Share = %v, want %v", text, cfg.Share, want)
		}
	}
}

func TestParseOnCrash(t *testing.T) {
	text := `on_crash = "report-crash"
[servers.web]
cmd = "x"
[servers.api]
cmd = "x"
on_crash = "page-me"
[servers.job]
cmd = "x"
on_crash = ""
`
	cfg, err := Parse("/tmp/portside.toml", []byte(text))
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Warnings) != 0 {
		t.Fatalf("warnings: %v", cfg.Warnings)
	}
	for name, want := range map[string]string{"web": "report-crash", "api": "page-me", "job": ""} {
		if s, _ := cfg.Server(name); s.OnCrash != want {
			t.Errorf("%s: OnCrash = %q, want %q", name, s.OnCrash, want)
		}
	}
}

// Server settings for unattended machines: the top level sets defaults
// that a server's own keys replace, and login_env is top-level only.
func TestParseServerOperations(t *testing.T) {
	text := `login_env = false
umask = "0007"
on_unready = "page-me"
on_recover = "all-clear"
unready_after = "2m"
retry_after = "5m"
[servers.web]
cmd = "x"
restart = "on-failure"
[servers.api]
cmd = "x"
restart = "always"
umask = "077"
on_unready = ""
unready_after = "30s"
retry_after = "0s"
`
	cfg, err := Parse("/tmp/portside.toml", []byte(text))
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.OwnEnv || len(cfg.Warnings) != 0 {
		t.Fatalf("OwnEnv %v, warnings %v", cfg.OwnEnv, cfg.Warnings)
	}
	web, _ := cfg.Server("web")
	api, _ := cfg.Server("api")
	if web.Umask != "0007" || web.OnUnready != "page-me" || web.OnRecover != "all-clear" || web.UnreadyAfter != 2*time.Minute || web.RetryAfter != 5*time.Minute {
		t.Errorf("web inherits: %+v", web)
	}
	if api.Umask != "077" || api.OnUnready != "" || api.OnRecover != "all-clear" || api.UnreadyAfter != 30*time.Second || api.RetryAfter != 0 {
		t.Errorf("api overrides: %+v", api)
	}
	if cfg, err := Parse("/tmp/portside.toml", []byte("[servers.web]\ncmd = \"x\"\n")); err != nil || cfg.OwnEnv || cfg.Servers[0].UnreadyAfter != DefaultUnreadyAfter || cfg.Servers[0].Umask != "" {
		t.Errorf("defaults: %+v, %v", cfg, err)
	}
	cfg, err = Parse("/tmp/portside.toml", []byte("[servers.web]\ncmd = \"x\"\nretry_after = \"1m\"\n"))
	if err != nil || len(cfg.Warnings) != 1 || !strings.Contains(cfg.Warnings[0], "retry_after has no effect") {
		t.Errorf("retry_after without a restart policy: %v, %v", cfg.Warnings, err)
	}
	for _, bad := range []string{`umask = "7"`, `umask = "0008"`, `umask = "u=rwx"`, `unready_after = "0s"`, `retry_after = "-1m"`} {
		if _, err := Parse("/tmp/portside.toml", []byte(bad+"\n[servers.web]\ncmd = \"x\"\n")); err == nil {
			t.Errorf("%s loaded", bad)
		}
		if _, err := Parse("/tmp/portside.toml", []byte("[servers.web]\ncmd = \"x\"\n"+bad+"\n")); err == nil {
			t.Errorf("server %s loaded", bad)
		}
	}
}

func TestParseReadiness(t *testing.T) {
	cfg, err := Parse("/tmp/portside.toml", []byte("[servers.worker]\ncmd = \"x\"\nport = 0\nready_log = \"Worker ready\"\nready_after = \"10s\"\n"))
	if err != nil {
		t.Fatal(err)
	}
	if s, _ := cfg.Server("worker"); s.ReadyLog != "Worker ready" || s.ReadyAfter != 10*time.Second {
		t.Fatalf("server = %+v", s)
	}
	if _, err := Parse("/tmp/portside.toml", []byte("[servers.worker]\ncmd = \"x\"\nready_log = \"(unclosed\"\n")); err == nil {
		t.Fatal("an invalid ready_log was accepted")
	}
}

// A task (oneshot = true) gets no automatic port, and settings for
// readiness or for a server that keeps running are errors.
func TestParseOneshot(t *testing.T) {
	cfg, err := Parse("/tmp/portside.toml", []byte("[servers.web]\ncmd = \"x\"\n[servers.migrate]\ncmd = \"x\"\noneshot = true\nrestart = \"on-failure\"\n[servers.seed]\ncmd = \"x\"\noneshot = true\nport = 0\n"))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"migrate", "seed"} {
		if s, _ := cfg.Server(name); !s.Oneshot || s.Port != 0 || s.PortAuto {
			t.Errorf("%s = %+v, want a task without a port", name, s)
		}
	}
	if s, _ := cfg.Server("web"); s.Oneshot || s.Port != AutoPortBase {
		t.Errorf("web = %+v", s)
	}
	for _, extra := range []string{"port = 3005", "health = \"/\"", "ready_log = \"ok\"", "ready_after = \"1s\"", "restart = \"always\""} {
		text := "[servers.migrate]\ncmd = \"x\"\noneshot = true\n" + extra + "\n"
		if _, err := Parse("/tmp/portside.toml", []byte(text)); err == nil {
			t.Errorf("a task with %s was accepted", extra)
		}
	}
}

func TestParseTerminal(t *testing.T) {
	cfg, err := Parse("/tmp/config.toml", []byte("terminal = 'custom-terminal --cwd={DIR}'\n"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Terminal != "custom-terminal --cwd={DIR}" || len(cfg.Warnings) != 0 {
		t.Fatalf("config = %+v", cfg)
	}
	cfg, err = Parse("/tmp/config.toml", nil)
	if err != nil || cfg.Terminal != "" {
		t.Fatalf("default = %+v, %v", cfg, err)
	}
}

func TestRecoverableConfiguration(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	state := filepath.Join(dir, "state")
	good := "port = 18432\n[servers.web]\ncmd = 'echo ok'\ndir = 'relative'\nport = 4802\n"
	if err := os.WriteFile(path, []byte(good), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadRecoverable(path, state)
	if err != nil || cfg.Port != 18432 {
		t.Fatalf("valid config: %v, %v", cfg, err)
	}
	if err := os.WriteFile(path, []byte("invalid = ["), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err = LoadRecoverable(path, state)
	if err == nil || cfg.Port != 18432 || len(cfg.Servers) != 1 {
		t.Fatalf("lost last good config: %v, %v", cfg, err)
	}
	if cfg.Servers[0].Dir != filepath.Join(dir, "relative") {
		t.Fatal("cached config changed relative paths")
	}
	cfg, err = LoadRecoverable(path, t.TempDir())
	if err == nil || cfg == nil || len(cfg.Servers) != 0 {
		t.Fatalf("empty fallback: %v, %v", cfg, err)
	}
	if err := os.WriteFile(path, []byte("port = 18433\n[servers.web]\ncmd = ''\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if port, auto, err := ReadPort(path); err != nil || port != 18433 || auto {
		t.Fatalf("port without server validation: %d, %v, %v", port, auto, err)
	}
	cfg, err = LoadRecoverable(path, t.TempDir())
	if err == nil || cfg.Port != 18433 || len(cfg.Servers) != 0 {
		t.Fatalf("raw port fallback: %v, %v", cfg, err)
	}
	if err := os.WriteFile(path, []byte(good), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err = LoadRecoverable(path, state)
	if err != nil || cfg.Port != 18432 {
		t.Fatalf("config repair: %v, %v", cfg, err)
	}
	blocked := filepath.Join(dir, "not-a-directory")
	if err := os.WriteFile(blocked, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if cfg, err := LoadRecoverable(path, blocked); err != nil || cfg.Port != 18432 {
		t.Fatalf("cache failure invalidated live config: %v", err)
	}
}

func TestReservedDashboardName(t *testing.T) {
	if err := ValidName(app.ID); err == nil {
		t.Fatal("dashboard name allowed for new entries")
	}
	text := fmt.Sprintf("[servers.%s]\ncmd = 'echo ok'\nport = 0\n", app.ID)
	cfg, err := Parse("config.toml", []byte(text))
	if err != nil || len(cfg.Servers) != 1 || len(cfg.Warnings) == 0 {
		t.Fatalf("existing reserved name: %+v, %v", cfg, err)
	}
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	spec := SpecOf(cfg.Servers[0])
	spec.Cmd = "echo edited"
	if err := UpdateServer(path, app.ID, spec); err != nil {
		t.Fatal(err)
	}
	if err := AddServer(path, spec); err == nil {
		t.Fatal("reserved entry could be added")
	}
}
