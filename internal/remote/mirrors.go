package remote

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strconv"
	"syscall"
	"time"

	"github.com/coder/websocket"

	"github.com/padmk/portside/internal/app"
)

// ── port mirrors ────────────────────────────────────────────────────────

type mirror struct {
	port              int // on the remote machine
	local             int // bound here
	server, container string
	lns               []net.Listener // empty while the port is busy here
	tried             time.Time      // last bind attempt, to space out retries
}

func (mr *mirror) close() {
	for _, ln := range mr.lns {
		_ = ln.Close()
	}
	mr.lns = nil
}

// dropAfter is how long mirrors outlive a lost connection: long enough for
// a brief network change, short enough that local tools stop reaching a
// computer that went away and its ports are free for local servers again.
const dropAfter = time.Minute

// retryBusy spaces out bind attempts on a port busy here, so a local server
// that restarts gets its port back before the mirror tries it again.
const retryBusy = 4 * time.Second

// localFor returns the local port a remote port is forwarded to.
func (c *conn) localFor(port int) int {
	if l := c.locals[port]; l > 0 {
		return l
	}
	return port
}

// reconcileLocked binds wanted ports that are free here and releases the
// rest: ports no longer wanted, ports turned off (listed, never bound),
// ports another local process now listens on, and every port once the
// connection has been down for dropAfter.
func (c *conn) reconcileLocked() {
	now := time.Now()
	for port, until := range c.held {
		if now.After(until) {
			delete(c.held, port)
		}
	}
	offline := !c.down.IsZero() && now.Sub(c.down) > dropAfter
	for port, mr := range c.mirrors {
		if _, ok := c.want[port]; !ok || offline || mr.local != c.localFor(port) {
			mr.close()
			delete(c.mirrors, port)
		}
	}
	if offline {
		return
	}
	for port, w := range c.want {
		local := c.localFor(port)
		mr := c.mirrors[port]
		if mr == nil {
			mr = &mirror{port: port, local: local}
			c.mirrors[port] = mr
		}
		mr.server, mr.container = w.Server, w.Container
		if c.off[port] {
			mr.close()
			continue
		}
		if len(mr.lns) > 0 {
			if wildcardTaken(local) {
				// A local process started listening on the port: it gets
				// the traffic, and the mirror waits until the port is free.
				log.Printf("remote: port %d is now in use on this computer; no longer forwarding it", local)
				mr.close()
				mr.tried = now
			}
			continue
		}
		if local == c.m.localPort || now.Before(c.held[local]) || now.Sub(mr.tried) < retryBusy ||
			(c.m.wanted != nil && c.m.wanted(local)) {
			continue
		}
		mr.tried = now
		mr.lns = c.bind(local, port)
	}
}

// bind listens on loopback port local, IPv4 and (where available) IPv6,
// when it is free here, and forwards connections to port on the remote
// machine. A port counts as taken when a socket of another process holds it
// on loopback or on all addresses: 127.0.0.1 can be bound next to a process
// listening on all addresses and would steal its traffic, so wildcardHeld
// checks for one first. The binds use SO_REUSEADDR (Go's default), so
// connections still closing after the mirror's last bind (TIME_WAIT) do not
// keep the port busy. Binds are tried instead of connecting, so local
// servers never see probe connections.
func (c *conn) bind(local, port int) []net.Listener {
	p := strconv.Itoa(local)
	ctx := context.Background()
	if wildcardHeld(local) {
		return nil
	}
	var lc net.ListenConfig
	v4, err := lc.Listen(ctx, "tcp", net.JoinHostPort("127.0.0.1", p))
	if err != nil {
		return nil
	}
	lns := []net.Listener{v4}
	v6, err := lc.Listen(ctx, "tcp", net.JoinHostPort("::1", p))
	switch {
	case err == nil:
		lns = append(lns, v6)
	case errors.Is(err, syscall.EADDRINUSE):
		_ = v4.Close()
		return nil
	}
	for _, ln := range lns {
		go c.accept(ln, port)
	}
	return lns
}

func (c *conn) accept(ln net.Listener, port int) {
	for {
		local, err := ln.Accept()
		if err != nil {
			return
		}
		go c.pipe(local, port)
	}
}

func (c *conn) pipe(local net.Conn, port int) {
	defer local.Close()
	ctx, cancel := context.WithTimeout(c.ctx, 10*time.Second)
	remote, err := c.dialPort(ctx, port)
	cancel()
	if err != nil {
		return
	}
	defer remote.Close()
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(remote, local); done <- struct{}{} }()
	go func() { _, _ = io.Copy(local, remote); done <- struct{}{} }()
	<-done
}

// dialPort opens a TCP connection to port on the remote machine's loopback,
// for a mirror or the *.localhost proxy. A port turned off is refused, and
// turning it off closes its open connections.
func (c *conn) dialPort(ctx context.Context, port int) (net.Conn, error) {
	if c.isOff(port) {
		return nil, ErrForwardingOff
	}
	ws, err := c.dialWS(ctx, "/api/tunnel?port="+strconv.Itoa(port))
	if err != nil {
		return nil, err
	}
	t := &tunnel{Conn: websocket.NetConn(c.ctx, ws, websocket.MessageBinary), ws: ws, c: c, port: port}
	c.m.mu.Lock()
	if c.off[port] {
		c.m.mu.Unlock()
		_ = ws.CloseNow()
		return nil, ErrForwardingOff
	}
	if c.tunnels[port] == nil {
		c.tunnels[port] = map[*tunnel]struct{}{}
	}
	c.tunnels[port][t] = struct{}{}
	c.m.mu.Unlock()
	return t, nil
}

func (c *conn) isOff(port int) bool {
	c.m.mu.Lock()
	defer c.m.mu.Unlock()
	return c.off[port]
}

// tunnel is an open connection to a remote port, tracked until it closes.
type tunnel struct {
	net.Conn
	ws   *websocket.Conn
	c    *conn
	port int
}

func (t *tunnel) Close() error {
	t.c.m.mu.Lock()
	delete(t.c.tunnels[t.port], t)
	t.c.m.mu.Unlock()
	return t.Conn.Close()
}

// closeTunnelsLocked ends the open connections to a remote port at once,
// without the WebSocket closing handshake.
func (c *conn) closeTunnelsLocked(port int) {
	for t := range c.tunnels[port] {
		_ = t.ws.CloseNow()
	}
	delete(c.tunnels, port)
}

// retryMirrors reconciles the mirrors every few seconds: it notices a local
// process taking a forwarded port soon after it starts, binds busy ports
// again soon after they are free (retryBusy apart), and drops the mirrors of
// a connection that stays down.
func (c *conn) retryMirrors() {
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-c.ctx.Done():
			return
		case <-t.C:
			c.update(c.reconcileLocked)
		}
	}
}

// Release frees local port for a local server about to start, and keeps it
// free long enough for that server to bind it.
func (m *Manager) Release(port int) {
	c := m.current()
	if c == nil {
		return
	}
	c.update(func() {
		c.held[port] = time.Now().Add(30 * time.Second)
		for _, mr := range c.mirrors {
			if mr.local == port {
				mr.close()
			}
		}
	})
}

// SetLocalWant registers fn, which reports a port a server on this computer
// is starting or running on. Such a port is not forwarded even once a
// Release has expired, so a server that binds late is not reached through
// the other computer. fn is called with the manager's lock held and must
// not call into the manager.
func (m *Manager) SetLocalWant(fn func(port int) bool) {
	m.mu.Lock()
	m.wanted = fn
	m.mu.Unlock()
}

// Forwarded reports whether local port currently forwards to the connected
// computer, with that computer's name. A connection to the port reaches the
// other computer, not a process here.
func (m *Manager) Forwarded(port int) (string, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.c == nil {
		return "", false
	}
	for _, mr := range m.c.mirrors {
		if mr.local == port && len(mr.lns) > 0 {
			return m.c.node.Name, true
		}
	}
	return "", false
}

// SetLocalPort forwards the connected computer's port to local on this
// computer instead of the same number, or to the same number again when
// local is 0 or port. The choice is kept for that computer.
func (m *Manager) SetLocalPort(port, local int) (Info, error) {
	c := m.current()
	if c == nil {
		return Info{}, &Error{Status: http.StatusConflict, Msg: "not connected to another computer"}
	}
	if local == 0 {
		local = port
	}
	if port <= 0 || port > 65535 || local <= 0 || local > 65535 {
		return Info{}, &Error{Status: http.StatusBadRequest, Msg: "invalid port"}
	}
	if local == m.localPort {
		return Info{}, &Error{Status: http.StatusConflict, Msg: fmt.Sprintf("port %d is %s's own port", local, app.Name)}
	}
	var conflict error
	c.update(func() {
		for p := range c.want {
			if p != port && c.localFor(p) == local {
				conflict = &Error{Status: http.StatusConflict, Msg: fmt.Sprintf("port %d already forwards port %d of %s", local, p, c.node.Name)}
				return
			}
		}
		if local == port {
			delete(c.locals, port)
		} else {
			c.locals[port] = local
		}
		c.reconcileLocked()
	})
	if conflict != nil {
		return Info{}, conflict
	}
	if err := m.store.setLocal(c.node.DNSName, port, local); err != nil {
		return Info{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.c != c {
		return Info{}, &Error{Status: http.StatusConflict, Msg: "not connected to " + c.node.Name}
	}
	return c.infoLocked(), nil
}

// SetForwarding turns forwarding of the connected computer's port on or
// off. Off closes the port's listeners here and its open connections, and
// <name>.<computer>.localhost no longer reaches a server on it; the port is
// still listed so it can be turned on again. The choice is kept for that
// computer.
func (m *Manager) SetForwarding(port int, on bool) (Info, error) {
	c := m.current()
	if c == nil {
		return Info{}, &Error{Status: http.StatusConflict, Msg: "not connected to another computer"}
	}
	if port <= 0 || port > 65535 {
		return Info{}, &Error{Status: http.StatusBadRequest, Msg: "invalid port"}
	}
	if err := m.store.setOff(c.node.DNSName, port, !on); err != nil {
		return Info{}, err
	}
	c.update(func() {
		if on {
			delete(c.off, port)
			if mr := c.mirrors[port]; mr != nil {
				mr.tried = time.Time{} // bind at once, not after the retry pause
			}
		} else {
			c.off[port] = true
			c.closeTunnelsLocked(port)
		}
		c.reconcileLocked()
	})
	if !on {
		// Idle keep-alive connections of the proxy are among those closed;
		// drop them from the pool too.
		c.devTr.CloseIdleConnections()
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.c != c {
		return Info{}, &Error{Status: http.StatusConflict, Msg: "not connected to " + c.node.Name}
	}
	return c.infoLocked(), nil
}
