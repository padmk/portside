package supervisor

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/padmk/portside/internal/config"
)

// A stopped server's port that forwards to another computer is shown as
// forwarded, not as held by an external process, and is not dialled.
func TestForwardedPortIsNotExternal(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	accepted := make(chan struct{}, 8)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			accepted <- struct{}{}
			c.Close()
		}
	}()
	port := ln.Addr().(*net.TCPAddr).Port
	cfg := &config.Config{Servers: []config.Server{{Name: "web", Dir: t.TempDir(), Cmd: "true", Port: port, Enabled: true}}}
	s := New(cfg, t.TempDir())
	view := func() View {
		for _, v := range s.Snapshot(7878) {
			if v.Name == "web" {
				return v
			}
		}
		t.Fatal("no view for web")
		return View{}
	}

	// Without the hook the listener counts as another process.
	s.check(context.Background(), map[string]int{}, true)
	if v := view(); v.External == nil || v.ForwardedFrom != "" {
		t.Fatalf("unforwarded: External = %v, ForwardedFrom = %q", v.External, v.ForwardedFrom)
	}
	select {
	case <-accepted: // the probe's connection
	case <-time.After(2 * time.Second):
		t.Fatal("sample did not probe the port")
	}

	s.SetForwarded(func(p int) (string, bool) { return "devbox", p == port })
	s.check(context.Background(), map[string]int{}, true)
	v := view()
	if v.External != nil || v.ForwardedFrom != "devbox" || v.Status != Stopped {
		t.Errorf("forwarded: External = %v, ForwardedFrom = %q, Status = %s", v.External, v.ForwardedFrom, v.Status)
	}
	time.Sleep(100 * time.Millisecond) // time for a probe's connection to arrive
	if len(accepted) > 0 {
		t.Error("sample dialled a forwarded port")
	}
}
