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

	"github.com/padmk/portside/internal/app"
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

// TestForwardedPortsAreBounded checks the limit on forwarded ports (servers
// first, ports turned off not counted, the rest listed as over it) and that
// this computer never listens on the daemon's ports for one.
func TestForwardedPortsAreBounded(t *testing.T) {
	if n := forwardLimit(); n < 128 || n > 65535 {
		t.Errorf("forwardLimit = %d", n)
	}
	m, c := connected(t)
	m.maxForwarded = 5
	m.localPort = 7999
	kept := []int{m.localPort, app.DefaultPort, app.FallbackPorts()[0]}
	servers := []map[string]any{}
	for i, p := range kept {
		servers = append(servers, map[string]any{"name": "kept" + strconv.Itoa(i), "port": p, "pid": 42})
	}
	var free []int
	for i := range 10 {
		free = append(free, freePort(t))
		servers = append(servers, map[string]any{"name": "s" + strconv.Itoa(i), "port": free[i], "pid": 42})
	}
	data, _ := json.Marshal(map[string]any{"servers": servers})
	var rs remoteState
	if err := json.Unmarshal(data, &rs); err != nil {
		t.Fatal(err)
	}
	c.apply(data, &rs)
	if mirrors := m.Info().Mirrors; len(mirrors) != len(servers) {
		t.Errorf("%d ports listed, want %d", len(mirrors), len(servers))
	}
	for _, p := range kept {
		if mr := mirrorOf(t, m, p); mr.State != "busy" {
			t.Errorf("port %d is %s, want busy", p, mr.State)
		}
	}
	for i, p := range free {
		want := "mirrored"
		if i >= 2 {
			want = "limit"
		}
		if mr := mirrorOf(t, m, p); mr.State != want || answers(p, "127.0.0.1") != (want == "mirrored") {
			t.Errorf("port %d is %s, want %s", p, mr.State, want)
		}
	}

	// A port turned off leaves room for the next one.
	if _, err := m.SetForwarding(free[0], false); err != nil {
		t.Fatal(err)
	}
	if mr := mirrorOf(t, m, free[2]); mr.State != "mirrored" || !answers(free[2], "127.0.0.1") {
		t.Errorf("after turning a port off, the next one is %+v", mr)
	}

	if _, err := m.SetLocalPort(free[1], app.FallbackPorts()[0]); err == nil {
		t.Error("SetLocalPort accepted a fallback port of the daemon")
	}
	// A kept remote port forwards to another local port.
	local := freePort(t)
	if _, err := m.SetLocalPort(app.DefaultPort, local); err != nil {
		t.Fatal(err)
	}
	if mr := mirrorOf(t, m, app.DefaultPort); mr.State != "mirrored" || mr.Local != local || !answers(local, "127.0.0.1") {
		t.Errorf("kept port forwarded to %d: %+v", local, mr)
	}
}

// TestMirrorWorkIsSpreadOverPasses checks that one reconcile binds and
// checks at most mirrorWork ports, and that the next passes go on with the
// rest, so the manager's lock is never held for every port at once.
func TestMirrorWorkIsSpreadOverPasses(t *testing.T) {
	m, c := connected(t)
	m.mirrorWork = 2
	var free []int
	servers := []map[string]any{}
	for i := range 5 {
		free = append(free, freePort(t))
		servers = append(servers, map[string]any{"name": "s" + strconv.Itoa(i), "port": free[i], "pid": 42})
	}
	data, _ := json.Marshal(map[string]any{"servers": servers})
	var rs remoteState
	if err := json.Unmarshal(data, &rs); err != nil {
		t.Fatal(err)
	}
	bound := func() int {
		n := 0
		for _, mr := range m.Info().Mirrors {
			if mr.State == "mirrored" {
				n++
			}
		}
		return n
	}
	c.apply(data, &rs)
	if n := bound(); n != 2 {
		t.Fatalf("%d ports bound by the first pass, want 2", n)
	}
	c.update(c.reconcileLocked)
	if n := bound(); n != 4 {
		t.Fatalf("%d ports bound after the second pass, want 4", n)
	}
	c.update(c.reconcileLocked)
	if n := bound(); n != 5 {
		t.Fatalf("%d ports bound after the third pass, want 5", n)
	}
	for _, p := range free {
		if !answers(p, "127.0.0.1") {
			t.Errorf("port %d does not listen", p)
		}
	}
}

// TestReleaseDuringABind checks that a port released for a local server
// while a bind of it runs ends up free: the bind is dropped once it
// finishes, Release waits for that, and the dropped bind never accepts.
func TestReleaseDuringABind(t *testing.T) {
	m, c := connected(t)
	p := freePort(t)
	data, _ := json.Marshal(map[string]any{"servers": []map[string]any{{"name": "web", "port": p, "pid": 42}}})
	var rs remoteState
	if err := json.Unmarshal(data, &rs); err != nil {
		t.Fatal(err)
	}
	// What apply does, with the job's run held back.
	m.mu.Lock()
	c.state, c.want, c.order = data, map[int]Mirror{p: {Port: p, Server: "web"}}, []int{p}
	c.reconcileLocked()
	jobs := c.jobs
	c.jobs = nil
	m.mu.Unlock()
	if len(jobs) != 1 {
		t.Fatalf("planned %d jobs, want 1", len(jobs))
	}

	released := make(chan struct{})
	go func() { m.Release(p); close(released) }()
	time.Sleep(100 * time.Millisecond)
	select {
	case <-released:
		t.Fatal("Release returned while a bind of the port was running")
	default:
	}
	jobs[0].run(c)
	m.mu.Lock()
	c.finishLocked(jobs)
	m.mu.Unlock()
	select {
	case <-released:
	case <-time.After(2 * time.Second):
		t.Fatal("Release did not return after the bind finished")
	}
	if answers(p, "127.0.0.1") {
		t.Error("the released port still accepts connections")
	}
	if mr := mirrorOf(t, m, p); mr.State == "mirrored" {
		t.Errorf("mirror = %+v, want it not bound", mr)
	}
}
