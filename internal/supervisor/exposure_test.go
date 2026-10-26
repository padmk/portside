package supervisor

import (
	"context"
	"net"
	"os"
	"testing"
	"time"

	"github.com/padmk/portside/internal/config"
	"github.com/padmk/portside/internal/ports"
)

func exposureView(t *testing.T, s *Supervisor, name string) View {
	t.Helper()
	for _, v := range s.Snapshot(7878) {
		if v.Name == name {
			return v
		}
	}
	t.Fatalf("no view for %s", name)
	return View{}
}

// A running server's exposure comes from the sockets actually listening on
// its port, read with lsof or ss.
func TestCheckExposureReadsListeners(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port
	cfg := &config.Config{Servers: []config.Server{{Name: "web", Dir: t.TempDir(), Cmd: "true", Port: port, Enabled: true}}}
	s := New(cfg, t.TempDir())
	s.mu.Lock()
	p := s.procs["web"]
	p.pid, p.status, p.running = os.Getpid(), Running, p.cfg
	s.mu.Unlock()

	s.checkExposure(context.Background(), nil, time.Now())
	if v := exposureView(t, s, "web"); v.Exposure != ports.Loopback || v.NetworkWarning {
		t.Fatalf("Exposure = %q, NetworkWarning = %v; want loopback without a warning", v.Exposure, v.NetworkWarning)
	}
}

// Every listener of the server counts: one on its port and, through its
// process tree, one on another port that listens on all interfaces.
func TestServerExposureCountsAllListeners(t *testing.T) {
	socks := []ports.Listener{
		{Port: 3000, PID: 10, Addresses: []string{"127.0.0.1", "[::1]"}},
		{Port: 24678, PID: 11, Addresses: []string{"*"}},         // the server's child
		{Port: 5432, PID: 99, Addresses: []string{"*"}},          // another program
		{Port: 3000, PID: 98, Addresses: []string{"100.64.1.2"}}, // on the server's port
	}
	own := func(pid int) bool { return pid == 10 || pid == 11 }
	if got := serverExposure(socks, 3000, own); got != ports.Network {
		t.Errorf("with a wildcard child listener: %q, want network", got)
	}
	if got := serverExposure(socks[:1], 3000, own); got != ports.Loopback {
		t.Errorf("loopback only: %q", got)
	}
	if got := serverExposure([]ports.Listener{socks[0], socks[3]}, 3000, own); got != ports.Tailnet {
		t.Errorf("loopback and tailnet: %q", got)
	}
	if got := serverExposure(socks[2:3], 3000, own); got != "" {
		t.Errorf("someone else's listener: %q, want none", got)
	}
}

// lan = true keeps the exposure but drops the warning; a stopped server
// shows neither.
func TestLANSilencesNetworkWarning(t *testing.T) {
	cfg := &config.Config{Servers: []config.Server{
		{Name: "open", Dir: t.TempDir(), Cmd: "true", Port: 3000, Enabled: true},
		{Name: "lan", Dir: t.TempDir(), Cmd: "true", Port: 3001, Enabled: true, LAN: true},
		{Name: "stopped", Dir: t.TempDir(), Cmd: "true", Port: 3002, Enabled: true},
	}}
	s := New(cfg, t.TempDir())
	s.mu.Lock()
	for _, name := range []string{"open", "lan"} {
		p := s.procs[name]
		p.pid, p.status, p.running, p.exposure = os.Getpid(), Running, p.cfg, ports.Network
	}
	s.procs["stopped"].exposure = ports.Network
	s.mu.Unlock()

	if v := exposureView(t, s, "open"); v.Exposure != ports.Network || !v.NetworkWarning {
		t.Errorf("open: Exposure = %q, NetworkWarning = %v", v.Exposure, v.NetworkWarning)
	}
	if v := exposureView(t, s, "lan"); v.Exposure != ports.Network || v.NetworkWarning || !v.LAN {
		t.Errorf("lan: Exposure = %q, NetworkWarning = %v, LAN = %v", v.Exposure, v.NetworkWarning, v.LAN)
	}
	if v := exposureView(t, s, "stopped"); v.Exposure != "" || v.NetworkWarning {
		t.Errorf("stopped: Exposure = %q, NetworkWarning = %v", v.Exposure, v.NetworkWarning)
	}
	if NetworkWarning(ports.Tailnet, false) || NetworkWarning(ports.Loopback, false) || NetworkWarning("", false) {
		t.Error("only network exposure warns")
	}
}
