package daemon

import (
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/padmk/portside/internal/app"
	"github.com/padmk/portside/internal/ports"
)

func TestListenRefusesBusyIPv6Loopback(t *testing.T) {
	if !ports.IPv6Loopback() {
		t.Skip("no IPv6 loopback")
	}
	// Another program holds [::1]:<port>, where browsers send *.localhost first.
	squatter, err := net.Listen("tcp", "[::1]:0")
	if err != nil {
		t.Fatal(err)
	}
	defer squatter.Close()
	port := squatter.Addr().(*net.TCPAddr).Port
	lns, err := listen(port)
	if err == nil {
		for _, ln := range lns {
			ln.Close()
		}
		t.Fatal("the daemon listened while another program held [::1]")
	}
	if !strings.Contains(err.Error(), "[::1]") || !strings.Contains(err.Error(), "another program") {
		t.Errorf("error %q does not say what holds which address", err)
	}
	// IPv4 loopback was given up again, so nothing half-listens.
	if ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port))); err != nil {
		t.Errorf("127.0.0.1 still held after the refusal: %v", err)
	} else {
		ln.Close()
	}

	// A handed-over IPv4 listener alone is completed with ::1, or refused.
	v4, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := completeListeners([]net.Listener{v4}, port); err == nil {
		t.Error("an inherited IPv4 listener accepted without ::1")
	}
	squatter.Close()
	v4, err = net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		t.Fatal(err)
	}
	lns, err = completeListeners([]net.Listener{v4}, port)
	if err != nil || len(lns) != 2 {
		t.Errorf("completing a free ::1: %v, %d listeners", err, len(lns))
	}
	for _, ln := range lns {
		ln.Close()
	}
}

func TestHandoffCarriesDashboardHost(t *testing.T) {
	state := t.TempDir()
	const host = "d-0123456789abcdef01234567.localhost"
	nonce, _ := newNonce()
	if err := writeHandoff(handoffPath(state), handoff{Nonce: nonce, PID: os.Getpid(), DashboardHost: host}); err != nil {
		t.Fatal(err)
	}
	t.Setenv(handoffEnv, nonce)
	ho := takeHandoff(state)
	if ho == nil || ho.DashboardHost != host {
		t.Fatalf("handoff %+v, want dashboard host %s", ho, host)
	}
}

func TestServiceSkillRefreshIsolation(t *testing.T) {
	base := t.TempDir()
	t.Setenv("HOME", base)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(base, ".config"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(base, ".local", "state"))
	t.Setenv(app.Env("STATE_DIR"), "")
	path := filepath.Join(base, ".config", app.ID, app.ID+".toml")
	t.Setenv(app.Env("CONFIG"), path)
	if !refreshInstalledSkill(path) {
		t.Fatal("a service using the default config must refresh the installed skill")
	}
	// The service pins the default state directory explicitly.
	t.Setenv(app.Env("STATE_DIR"), filepath.Join(base, ".local", "state", app.ID))
	if !refreshInstalledSkill(path) {
		t.Fatal("a service pinning the default state directory must refresh the installed skill")
	}
	if refreshInstalledSkill(filepath.Join(base, "isolated.toml")) {
		t.Fatal("a custom config must not refresh the installed skill")
	}
	t.Setenv(app.Env("STATE_DIR"), filepath.Join(base, "state"))
	if refreshInstalledSkill(path) {
		t.Fatal("an isolated state directory must not refresh the installed skill")
	}
}
