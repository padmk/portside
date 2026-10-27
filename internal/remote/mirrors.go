package remote

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"slices"
	"strconv"
	"strings"
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
	over              bool           // beyond the ports forwardLimit allows
	pending           bool           // a mirrorJob for it is running
}

// mirrorJob is a bind attempt or a check for a local listener that
// reconcileLocked plans and update runs without the manager's lock: with
// thousands of sockets open, single calls can take the kernel hundreds of
// milliseconds.
type mirrorJob struct {
	mr    *mirror
	local int
	check bool           // check a bound mirror instead of binding
	lns   []net.Listener // what a bind got
	taken bool           // what a check found
}

func (j *mirrorJob) run(c *conn) {
	if j.check {
		j.taken = wildcardTaken(j.local)
	} else {
		j.lns = listen(j.local)
	}
}

// bindableLocked reports whether mr may listen on its local port now.
func (c *conn) bindableLocked(mr *mirror, now time.Time) bool {
	_, wanted := c.want[mr.port]
	return wanted && !c.off[mr.port] && !mr.over && c.m.forwardable(mr.local) &&
		!now.Before(c.held[mr.local]) && (c.m.wanted == nil || !c.m.wanted(mr.local))
}

// finishLocked applies what jobs found, unless the mirror changed meanwhile:
// a bind starts accepting connections only once it is kept here.
func (c *conn) finishLocked(jobs []*mirrorJob) {
	defer c.settled.Broadcast()
	now := time.Now()
	for _, j := range jobs {
		mr := j.mr
		mr.pending = false
		if !j.check {
			c.binding[j.local]--
			if c.binding[j.local] <= 0 {
				delete(c.binding, j.local)
			}
		}
		current := c.m.c == c && c.mirrors[mr.port] == mr && mr.local == j.local
		if j.check {
			if current && j.taken && len(mr.lns) > 0 {
				// A local process started listening on the port: it gets
				// the traffic, and the mirror waits until the port is free.
				log.Printf("remote: port %d is now in use on this computer; no longer forwarding it", j.local)
				mr.close()
				mr.tried = now
			}
			continue
		}
		if !current || len(mr.lns) > 0 || !c.bindableLocked(mr, now) {
			closeAll(j.lns)
			continue
		}
		mr.lns = j.lns
		c.serve(mr.lns, mr.port)
	}
}

func (mr *mirror) close() {
	closeAll(mr.lns)
	mr.lns = nil
}

func closeAll(lns []net.Listener) {
	for _, ln := range lns {
		_ = ln.Close()
	}
}

// dropAfter is how long mirrors outlive a lost connection: long enough for
// a brief network change, short enough that local tools stop reaching a
// computer that went away and its ports are free for local servers again.
const dropAfter = time.Minute

// retryBusy spaces out bind attempts on a port busy here, so a local server
// that restarts gets its port back before the mirror tries it again.
const retryBusy = 4 * time.Second

// mirrorWork is the most bind attempts and checks for a local listener one
// reconcile makes. Each takes several system calls under the manager's lock,
// so with thousands of forwarded ports they are spread over several passes,
// in turn, instead of holding the lock for seconds.
const mirrorWork = 128

// forwardLimit returns how many ports may be forwarded here: those that
// fit in an eighth of this process's open-file limit (which Go raises to
// the hard limit at start). Each port listens on two sockets, so forwarded
// ports hold at most a quarter of the descriptors; the rest is left for the
// connections through them (two descriptors each), the servers' terminals
// and logs, and the daemon's clients, so a computer that was taken over
// cannot make this daemon run out of them.
func forwardLimit() int {
	var lim syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_NOFILE, &lim); err != nil || lim.Cur == 0 {
		return 1024 / 8
	}
	return int(min(lim.Cur/8, 65535))
}

// forwardable reports whether a port may be bound here for a forwarded
// port: never this daemon's own port or the ports a daemon of this product
// may move to.
func (m *Manager) forwardable(port int) bool {
	switch {
	case port <= 0 || port > 65535:
		return false
	case port == m.localPort || port == app.DefaultPort:
		return false
	}
	return !slices.Contains(app.FallbackPorts(), port)
}

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
// connection has been down for dropAfter. It only plans the binds and the
// checks for local listeners, as jobs that update runs.
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
	// Ports beyond the limit, in the order the other computer lists them
	// (servers first); ports turned off do not count.
	over := map[int]bool{}
	n := 0
	for _, port := range c.order {
		if !c.off[port] {
			over[port] = n >= c.m.maxForwarded
			n++
		}
	}
	// Bind attempts and checks start where the last pass stopped.
	work, from, resume := 0, 0, -1
	if len(c.order) > 0 {
		from = c.next % len(c.order)
	}
	for i := range c.order {
		at := (from + i) % len(c.order)
		port := c.order[at]
		w := c.want[port]
		local := c.localFor(port)
		mr := c.mirrors[port]
		if mr == nil {
			mr = &mirror{port: port, local: local}
			c.mirrors[port] = mr
		}
		mr.server, mr.container, mr.over = w.Server, w.Container, over[port]
		if c.off[port] || mr.over {
			mr.close()
			continue
		}
		bound := len(mr.lns) > 0
		switch {
		case mr.pending:
			continue
		case bound && !probeWildcard:
			continue
		case !bound && (now.Sub(mr.tried) < retryBusy || !c.bindableLocked(mr, now)):
			continue
		case work >= c.m.mirrorWork:
			if resume < 0 {
				resume = at // the first port left for the next pass
			}
			continue
		}
		work++
		mr.pending = true
		if !bound {
			mr.tried = now
			c.binding[local]++
		}
		c.jobs = append(c.jobs, &mirrorJob{mr: mr, local: local, check: bound})
	}
	c.next = max(resume, 0)
}

// listen binds loopback port local for a mirror, IPv4 and (where
// available) IPv6, when it is free here, without accepting connections
// yet (serve does). A port counts as taken when a socket of another
// process holds it on loopback or on all addresses: 127.0.0.1 can be bound
// next to a process listening on all addresses and would steal its
// traffic, so wildcardHeld checks for one first. The binds use
// SO_REUSEADDR (Go's default), so connections still closing after the
// mirror's last bind (TIME_WAIT) do not keep the port busy. Binds are tried
// instead of connecting, so local servers never see probe connections.
func listen(local int) []net.Listener {
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
	return lns
}

// serve forwards the connections lns accept to port on the remote machine.
func (c *conn) serve(lns []net.Listener, port int) {
	for _, ln := range lns {
		go c.accept(ln, port)
	}
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
		// A bind of the port still running would hold it for a moment;
		// finishLocked drops it now that the port is held.
		for c.binding[port] > 0 {
			c.settled.Wait()
		}
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
	if !m.forwardable(local) {
		return Info{}, &Error{Status: http.StatusConflict, Msg: fmt.Sprintf("port %d is kept for %s", local, app.Name)}
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
// computer. Turning off a port that computer does not forward is refused.
func (m *Manager) SetForwarding(port int, on bool) (Info, error) {
	c := m.current()
	if c == nil {
		return Info{}, &Error{Status: http.StatusConflict, Msg: "not connected to another computer"}
	}
	if port <= 0 || port > 65535 {
		return Info{}, &Error{Status: http.StatusBadRequest, Msg: "invalid port"}
	}
	if !on {
		if err := m.checkForwarded(c, port); err != nil {
			return Info{}, err
		}
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

// checkForwarded fails unless the connected computer c forwards port, busy
// here or turned off included, naming the ports it forwards.
func (m *Manager) checkForwarded(c *conn, port int) error {
	m.mu.Lock()
	_, ok := c.want[port]
	listed := make([]int, 0, len(c.want))
	for p := range c.want {
		listed = append(listed, p)
	}
	m.mu.Unlock()
	if ok {
		return nil
	}
	if len(listed) == 0 {
		return &Error{Status: http.StatusNotFound, Msg: fmt.Sprintf("port %d is not forwarded from %s, which forwards no ports now", port, c.node.Name)}
	}
	slices.Sort(listed)
	names := make([]string, len(listed))
	for i, p := range listed {
		names[i] = strconv.Itoa(p)
	}
	return &Error{Status: http.StatusNotFound, Msg: fmt.Sprintf("port %d is not forwarded from %s; forwarded ports: %s", port, c.node.Name, strings.Join(names, ", "))}
}
