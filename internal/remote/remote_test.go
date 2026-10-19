package remote

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/padmk/portside/internal/share"
	"github.com/padmk/portside/internal/tailnet"
)

var devbox = tailnet.Node{Name: "devbox", Label: "devbox", DNSName: "devbox.t.ts.net",
	IPs: []string{"100.0.0.1"}, Online: true, UserID: 1}

func testCred() share.Credential {
	return share.Credential{ID: "abc", Key: strings.Repeat("ab", 32)}
}

// connected returns a manager holding a connection to devbox that has not
// started its event stream.
func connected(t *testing.T) (*Manager, *conn) {
	t.Helper()
	m := New(7878, t.TempDir(), tailnet.Preloaded(&tailnet.Status{Self: tailnet.Node{Name: "mbp", DNSName: "mbp.t.ts.net"}}))
	c := newConn(m, devbox, 7878, testCred(), peerTransport(devbox))
	m.c = c
	t.Cleanup(m.Disconnect)
	return m, c
}

// freePort returns a port free on every address. A port picked on
// 127.0.0.1 alone can be held on the wildcard address by another process
// (macOS allows both binds), which mirrors report as busy.
func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func TestTokens(t *testing.T) {
	dir := t.TempDir()
	tk := newTokens(dir)
	if _, ok := tk.Get("devbox.t.ts.net"); ok {
		t.Fatal("credential before pairing")
	}
	if err := tk.Set("Devbox.t.ts.net", testCred()); err != nil {
		t.Fatal(err)
	}
	got, ok := tk.Get("devbox.t.ts.net")
	if !ok || got != testCred() {
		t.Errorf("Get = %+v, %v", got, ok)
	}
	st, err := os.Stat(filepath.Join(dir, "remote-tokens.json"))
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("credentials file: %v, mode %v", err, st.Mode())
	}
	if err := tk.Set("devbox.t.ts.net", share.Credential{}); err != nil {
		t.Fatal(err)
	}
	if _, ok := tk.Get("devbox.t.ts.net"); ok {
		t.Error("credential still there after removing it")
	}
}

func TestBindLeavesTakenPortsAlone(t *testing.T) {
	_, c := connected(t)

	// Another process on all addresses: 127.0.0.1 could be bound next to it
	// with SO_REUSEADDR and would steal its traffic.
	wild, err := net.Listen("tcp4", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	defer wild.Close()
	if lns := c.bind(wild.Addr().(*net.TCPAddr).Port, 1); lns != nil {
		closeAll(lns)
		t.Error("bound a port that a listener on 0.0.0.0 holds")
	}

	// Another process on IPv6 loopback only.
	if v6, err := net.Listen("tcp6", "[::1]:0"); err == nil {
		defer v6.Close()
		if lns := c.bind(v6.Addr().(*net.TCPAddr).Port, 1); lns != nil {
			closeAll(lns)
			t.Error("bound a port that a listener on ::1 holds")
		}
	}

	p := freePort(t)
	lns := c.bind(p, p)
	if lns == nil {
		t.Fatal("did not bind a free port")
	}
	closeAll(lns)
}

func closeAll(lns []net.Listener) {
	for _, l := range lns {
		l.Close()
	}
}

func TestApplyMirrorsRunningPorts(t *testing.T) {
	m, c := connected(t)
	running, stopped, db, stoppedDB := freePort(t), freePort(t), freePort(t), freePort(t)
	state := map[string]any{
		"daemon": map[string]any{"app": "portside", "version": "test"},
		"servers": []map[string]any{
			{"name": "web", "port": running, "url": "http://127.0.0.1:" + strconv.Itoa(running) + "/app", "pid": 42},
			{"name": "docs", "port": stopped},
		},
		"docker": map[string]any{"containers": []map[string]any{
			{"name": "db", "state": "running", "ports": []map[string]any{{"public": db, "proto": "tcp"}, {"public": 9, "proto": "udp"}}},
			{"name": "old", "state": "exited", "ports": []map[string]any{{"public": stoppedDB, "proto": "tcp"}}},
		}},
	}
	data, _ := json.Marshal(state)
	var rs remoteState
	if err := json.Unmarshal(data, &rs); err != nil {
		t.Fatal(err)
	}
	c.apply(data, &rs)

	info := m.Info()
	got := map[int]Mirror{}
	for _, mr := range info.Mirrors {
		got[mr.Port] = mr
	}
	if len(got) != 2 || got[running].Server != "web" || got[db].Container != "db" {
		t.Fatalf("mirrors = %+v, want web's and db's ports only", info.Mirrors)
	}
	for _, p := range []int{running, db} {
		if got[p].State != "mirrored" {
			t.Errorf("port %d: %s", p, got[p].State)
		}
	}
	if rt, err := m.Route("docs", "devbox"); err != nil || rt.Port != stopped {
		t.Errorf("Route(docs) = %+v, %v; stopped servers keep their route", rt, err)
	}
	if _, err := m.Route("web", "mbp"); err != ErrNotConnected {
		t.Errorf("Route for another computer's label: %v", err)
	}
	if _, err := m.Route("nope", ""); err != ErrUnknownServer {
		t.Errorf("Route for an unknown server: %v", err)
	}

	// A local server about to start takes the port back.
	m.Release(running)
	if l, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(running)); err != nil {
		t.Errorf("port still held after Release: %v", err)
	} else {
		l.Close()
	}
	c.apply(data, &rs) // a new remote state must not grab it again right away
	for _, mr := range m.Info().Mirrors {
		if mr.Port == running && mr.State != "busy" {
			t.Errorf("released port re-bound: %+v", mr)
		}
	}
	// A local server still starting keeps it once the release has expired.
	m.SetLocalWant(func(p int) bool { return p == running })
	expire := func() {
		c.update(func() {
			delete(c.held, running)
			c.mirrors[running].tried = time.Time{}
		})
	}
	expire()
	c.update(c.reconcileLocked)
	if mr := mirrorOf(t, m, running); mr.State != "busy" {
		t.Errorf("port of a starting local server re-bound: %+v", mr)
	}
	m.SetLocalWant(nil)
	expire()
	c.update(c.reconcileLocked)
	if mr := mirrorOf(t, m, running); mr.State != "mirrored" {
		t.Errorf("port not forwarded again once the local server stopped: %+v", mr)
	}

	m.Disconnect()
	if l, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(db)); err != nil {
		t.Errorf("mirror still bound after disconnect: %v", err)
	} else {
		l.Close()
	}
}

func TestServeAPIChecksTheComputer(t *testing.T) {
	m, c := connected(t)
	cases := []struct {
		name, path string
		status     int
		setup      func()
	}{
		{"another computer", PathPrefix + "/mbp.t.ts.net/state", http.StatusConflict, nil},
		{"reconnecting", PathPrefix + "/devbox.t.ts.net/state", http.StatusServiceUnavailable, func() {
			m.mu.Lock()
			c.status = Reconnecting
			m.mu.Unlock()
		}},
		{"not connected", PathPrefix + "/devbox.t.ts.net/state", http.StatusConflict, m.Disconnect},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.setup != nil {
				tc.setup()
			}
			w := httptest.NewRecorder()
			m.ServeAPI(w, httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader("{}")))
			if w.Code != tc.status {
				t.Errorf("status %d (%s), want %d", w.Code, strings.TrimSpace(w.Body.String()), tc.status)
			}
		})
	}
}

func TestBindEndsWithLife(t *testing.T) {
	life, end := context.WithCancel(context.Background())
	r, release := Bind(httptest.NewRequest(http.MethodGet, "/", nil), life)
	defer release()
	end()
	select {
	case <-r.Context().Done():
	case <-time.After(time.Second):
		t.Error("request context did not end with the connection")
	}
}

// mirrorState applies a remote state in which server web runs on port and
// returns its mirror.
func mirrorState(t *testing.T, m *Manager, c *conn, port int) Mirror {
	t.Helper()
	data, _ := json.Marshal(map[string]any{"servers": []map[string]any{{"name": "web", "port": port, "pid": 42}}})
	var rs remoteState
	if err := json.Unmarshal(data, &rs); err != nil {
		t.Fatal(err)
	}
	c.apply(data, &rs)
	return mirrorOf(t, m, port)
}

func mirrorOf(t *testing.T, m *Manager, port int) Mirror {
	t.Helper()
	for _, mr := range m.Info().Mirrors {
		if mr.Port == port {
			return mr
		}
	}
	t.Fatalf("no mirror for port %d in %+v", port, m.Info().Mirrors)
	return Mirror{}
}

func TestMirrorGivesWayToALocalListener(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("only macOS lets a wildcard bind succeed next to the mirror")
	}
	m, c := connected(t)
	p := freePort(t)
	if mr := mirrorState(t, m, c, p); mr.State != "mirrored" {
		t.Fatalf("mirror = %+v", mr)
	}
	if name, ok := m.Forwarded(p); !ok || name != "devbox" {
		t.Errorf("Forwarded(%d) = %q, %v", p, name, ok)
	}
	c.update(c.reconcileLocked)
	if mr := mirrorOf(t, m, p); mr.State != "mirrored" {
		t.Fatalf("the probe took the mirror's own sockets for another listener: %+v", mr)
	}

	// A local process, such as docker publishing a port, binds all
	// addresses after the mirror; the system allows it.
	local, err := net.Listen("tcp4", "0.0.0.0:"+strconv.Itoa(p))
	if err != nil {
		t.Fatalf("local bind next to the mirror: %v", err)
	}
	defer local.Close()
	c.update(c.reconcileLocked)
	if mr := mirrorOf(t, m, p); mr.State != "busy" {
		t.Fatalf("mirror kept the port next to a local listener: %+v", mr)
	}
	if _, ok := m.Forwarded(p); ok {
		t.Error("Forwarded after giving the port up")
	}
	accepted := make(chan struct{})
	go func() {
		if cn, err := local.Accept(); err == nil {
			cn.Close()
			close(accepted)
		}
	}()
	cn, err := net.Dial("tcp", "127.0.0.1:"+strconv.Itoa(p))
	if err != nil {
		t.Fatal(err)
	}
	defer cn.Close()
	select {
	case <-accepted:
	case <-time.After(2 * time.Second):
		t.Error("127.0.0.1 did not reach the local listener")
	}
}

func TestMirrorsDropWhileDisconnected(t *testing.T) {
	m, c := connected(t)
	p := freePort(t)
	mirrorState(t, m, c, p)

	c.update(func() { c.down = time.Now().Add(-2 * dropAfter) })
	c.update(c.reconcileLocked)
	if n := len(m.Info().Mirrors); n != 0 {
		t.Fatalf("%d mirrors after the connection stayed down", n)
	}
	if l, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(p)); err != nil {
		t.Errorf("port still held: %v", err)
	} else {
		l.Close()
	}

	c.setStatus(Open, nil)
	c.update(c.reconcileLocked)
	if mr := mirrorOf(t, m, p); mr.State != "mirrored" {
		t.Errorf("mirror after reconnecting: %+v", mr)
	}
}

func TestSetLocalPort(t *testing.T) {
	m, c := connected(t)
	p, alt, other := freePort(t), freePort(t), freePort(t)
	data, _ := json.Marshal(map[string]any{"servers": []map[string]any{
		{"name": "web", "port": p, "pid": 42}, {"name": "api", "port": other, "pid": 43}}})
	var rs remoteState
	_ = json.Unmarshal(data, &rs)
	c.apply(data, &rs)

	if _, err := m.SetLocalPort(p, other); err == nil {
		t.Error("forwarded a port to another mirror's port")
	}
	info, err := m.SetLocalPort(p, alt)
	if err != nil {
		t.Fatal(err)
	}
	for _, mr := range info.Mirrors {
		if mr.Port == p && (mr.Local != alt || mr.State != "mirrored") {
			t.Errorf("mirror = %+v, want forwarded to %d", mr, alt)
		}
	}
	if _, ok := m.Forwarded(alt); !ok {
		t.Errorf("port %d not forwarded", alt)
	}
	if l, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(p)); err != nil {
		t.Errorf("the remote port's own number is still bound here: %v", err)
	} else {
		l.Close()
	}
	// Kept for that computer.
	if got := m.store.locals(devbox.DNSName); got[p] != alt {
		t.Errorf("stored = %v", got)
	}
	m.Release(alt)
	if _, ok := m.Forwarded(alt); ok {
		t.Error("Release left the chosen local port bound")
	}

	if _, err := m.SetLocalPort(p, 0); err != nil {
		t.Fatal(err)
	}
	if got := m.store.locals(devbox.DNSName); len(got) != 0 {
		t.Errorf("stored after reset = %v", got)
	}
}

func TestResume(t *testing.T) {
	m := New(7878, t.TempDir(), tailnet.Preloaded(&tailnet.Status{Self: tailnet.Node{Name: "mbp", DNSName: "mbp.t.ts.net"}}))
	t.Cleanup(m.Disconnect)
	m.Resume(context.Background())
	if m.Info() != nil {
		t.Fatal("resumed without a saved connection")
	}

	// A closed local port stands in for a computer that does not answer.
	node := devbox
	node.IPs = []string{"127.0.0.1"}
	if err := m.tokens.Set(node.DNSName, testCred()); err != nil {
		t.Fatal(err)
	}
	if err := m.store.setConnection(savedFrom(node, freePort(t))); err != nil {
		t.Fatal(err)
	}
	m.Resume(context.Background())
	if info := m.Info(); info == nil || info.Peer.DNSName != node.DNSName || info.Status == Open {
		t.Fatalf("Info after Resume = %+v", info)
	}

	// A daemon that stops keeps the connection for the next one.
	m.Close()
	if _, ok := m.store.connection(); !ok {
		t.Fatal("Close forgot the saved connection")
	}
	m.Resume(context.Background())
	if m.Info() == nil {
		t.Fatal("did not resume after Close")
	}

	m.Disconnect()
	if _, ok := m.store.connection(); ok {
		t.Error("Disconnect kept the saved connection")
	}
	m.Resume(context.Background())
	if m.Info() != nil {
		t.Error("resumed after Disconnect")
	}

	// Without a pairing there is nothing to resume.
	_ = m.store.setConnection(savedFrom(node, 7878))
	_ = m.tokens.Set(node.DNSName, share.Credential{})
	m.Resume(context.Background())
	if m.Info() != nil {
		t.Error("resumed without a pairing")
	}
	if _, ok := m.store.connection(); ok {
		t.Error("kept a saved connection without a pairing")
	}
}
