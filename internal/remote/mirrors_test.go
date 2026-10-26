package remote

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestForwardingOff(t *testing.T) {
	m, c := connected(t)
	p := freePort(t)
	if mr := mirrorState(t, m, c, p); mr.State != "mirrored" {
		t.Fatalf("mirror = %+v", mr)
	}
	before, err := m.Route("web", "devbox")
	if err != nil {
		t.Fatal(err)
	}

	info, err := m.SetForwarding(p, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(info.Mirrors) != 1 || info.Mirrors[0].State != "off" {
		t.Fatalf("mirrors = %+v, want the port listed as off", info.Mirrors)
	}
	if answers(p, "127.0.0.1", "::1") {
		t.Error("the port still listens here")
	}
	if _, ok := m.Forwarded(p); ok {
		t.Error("Forwarded reports a port turned off")
	}
	if _, err := m.Route("web", "devbox"); !errors.Is(err, ErrForwardingOff) {
		t.Errorf("Route = %v, want ErrForwardingOff", err)
	}
	// A route handed out before still cannot reach the port.
	if _, err := before.Transport.(*http.Transport).DialContext(context.Background(), "tcp", "127.0.0.1:"+strconv.Itoa(p)); !errors.Is(err, ErrForwardingOff) {
		t.Errorf("dial through an earlier route = %v, want ErrForwardingOff", err)
	}
	// Reconciling, or a new remote state, does not bind it again.
	c.update(func() { c.mirrors[p].tried = time.Time{} })
	c.update(c.reconcileLocked)
	if mr := mirrorState(t, m, c, p); mr.State != "off" || answers(p, "127.0.0.1") {
		t.Errorf("after a new state: %+v", mr)
	}

	// Kept for that computer: a new connection to it starts with it off.
	if off := m.store.off(devbox.DNSName); !off[p] {
		t.Errorf("stored = %v", off)
	}
	if off := m.store.off("other.t.ts.net"); len(off) != 0 {
		t.Errorf("another computer has ports off: %v", off)
	}
	m.Disconnect()
	c = newConn(m, devbox, 7878, testCred(), peerTransport(devbox))
	m.mu.Lock()
	m.c = c
	m.mu.Unlock()
	if mr := mirrorState(t, m, c, p); mr.State != "off" || answers(p, "127.0.0.1") {
		t.Errorf("after reconnecting: %+v", mr)
	}

	info, err = m.SetForwarding(p, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(info.Mirrors) != 1 || info.Mirrors[0].State != "mirrored" {
		t.Fatalf("mirrors after turning on = %+v", info.Mirrors)
	}
	if !answers(p, "127.0.0.1") {
		t.Error("the port does not listen again")
	}
	if _, err := m.Route("web", "devbox"); err != nil {
		t.Errorf("Route after turning on: %v", err)
	}
	if off := m.store.off(devbox.DNSName); len(off) != 0 {
		t.Errorf("stored after turning on = %v", off)
	}
}

func TestForwardingOffClosesOpenConnections(t *testing.T) {
	m, c := connected(t)
	p := freePort(t)
	mirrorState(t, m, c, p)

	// A tunnel as dialPort tracks it, over a local WebSocket.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer ws.CloseNow()
		_, _, _ = ws.Read(r.Context())
	}))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ws, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	tn := &tunnel{Conn: websocket.NetConn(ctx, ws, websocket.MessageBinary), ws: ws, c: c, port: p}
	c.update(func() { c.tunnels[p] = map[*tunnel]struct{}{tn: {}} })

	if _, err := m.SetForwarding(p, false); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := tn.Read(make([]byte, 1))
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Error("read succeeded on a closed tunnel")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the open connection kept running after the port was turned off")
	}
}

// TestBindAfterClosedConnections binds a mirror again right after it ended
// connections itself, which leaves them closing (TIME_WAIT) on the port for
// about 30 seconds, while a listener of another process on the port still
// keeps the mirror away.
func TestBindAfterClosedConnections(t *testing.T) {
	_, c := connected(t)
	// The remote port is turned off, so the mirror ends every connection at
	// once, before the client does.
	c.update(func() { c.off[1] = true })
	p := freePort(t)
	addr := "127.0.0.1:" + strconv.Itoa(p)
	for i := range 3 {
		lns := c.bind(p, 1)
		if lns == nil {
			t.Fatalf("round %d: the port is busy right after the mirror's connections closed", i)
		}
		cn, err := net.Dial("tcp", addr)
		if err != nil {
			closeAll(lns)
			t.Fatal(err)
		}
		_ = cn.SetReadDeadline(time.Now().Add(2 * time.Second))
		if _, err := cn.Read(make([]byte, 1)); !errors.Is(err, io.EOF) {
			t.Fatalf("round %d: read = %v, want the mirror to close first", i, err)
		}
		cn.Close()
		closeAll(lns)
	}

	for _, l := range []struct{ network, host string }{
		{"tcp4", "127.0.0.1"},
		{"tcp6", "::1"},
		{"tcp4", "0.0.0.0"},
		{"tcp", ""}, // all addresses, IPv4 and IPv6
	} {
		local, err := net.Listen(l.network, net.JoinHostPort(l.host, strconv.Itoa(p)))
		if err != nil {
			if l.network == "tcp6" {
				continue // no IPv6 loopback
			}
			t.Fatalf("listener on %q next to closing connections: %v", l.host, err)
		}
		if lns := c.bind(p, 1); lns != nil {
			closeAll(lns)
			t.Errorf("bound port %d while another process listens on %s", p, local.Addr())
		}
		local.Close()
	}
}

func TestForwardingOffOnlyForForwardedPorts(t *testing.T) {
	m, c := connected(t)
	p, busy, other := freePort(t), freePort(t), freePort(t)
	local, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(busy))
	if err != nil {
		t.Fatal(err)
	}
	defer local.Close()
	data, _ := json.Marshal(map[string]any{"servers": []map[string]any{
		{"name": "web", "port": p, "pid": 42}, {"name": "api", "port": busy, "pid": 43}}})
	var rs remoteState
	if err := json.Unmarshal(data, &rs); err != nil {
		t.Fatal(err)
	}
	c.apply(data, &rs)
	if mr := mirrorOf(t, m, busy); mr.State != "busy" {
		t.Fatalf("mirror of a port in use here = %+v", mr)
	}

	_, err = m.SetForwarding(other, false)
	var rerr *Error
	if !errors.As(err, &rerr) || rerr.Status != http.StatusNotFound ||
		!strings.Contains(rerr.Msg, strconv.Itoa(p)) || !strings.Contains(rerr.Msg, strconv.Itoa(busy)) {
		t.Fatalf("turning off a port not forwarded: %v, want 404 listing the forwarded ports", err)
	}
	if off := m.store.off(devbox.DNSName); off[other] {
		t.Error("the refused setting was stored")
	}
	if _, err := m.SetForwarding(busy, false); err != nil {
		t.Errorf("turning off a forwarded port busy here: %v", err)
	}
}
