// Package remote connects this daemon to the daemon on another of the
// owner's devices (see package share for the other side). While connected it
// relays the dashboard's API calls and WebSockets, serves the remote servers
// at http://<name>.<computer>.localhost through this daemon's proxy, and
// forwards each running remote server's port to the same port here, so links
// such as http://127.0.0.1:5173 open as if the servers ran on this machine.
//
// All traffic travels over the tailnet as HTTP to Tailscale Serve on the
// other device; raw TCP for ports is carried in WebSockets to /api/tunnel.
package remote

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/coder/websocket"

	"github.com/padmk/portside/internal/app"
	"github.com/padmk/portside/internal/share"
	"github.com/padmk/portside/internal/tailnet"
)

// Connection states.
const (
	Connecting   = "connecting"
	Open         = "open"
	Reconnecting = "reconnecting"
)

// Mirror is a remote port forwarded to the same port on this machine.
type Mirror struct {
	Port      int    `json:"port"`
	Server    string `json:"server,omitempty"`
	Container string `json:"container,omitempty"`
	// State is "mirrored", or "busy" when the port is taken here.
	State string `json:"state"`
}

// Info describes the connection for the dashboard.
type Info struct {
	Peer    tailnet.Node `json:"peer"`
	Status  string       `json:"status"`
	Error   string       `json:"error,omitempty"`
	Version string       `json:"version,omitempty"`
	// LocalPort is this daemon's port, used in *.localhost URLs.
	LocalPort int       `json:"localPort"`
	Mirrors   []Mirror  `json:"mirrors"`
	Since     time.Time `json:"since"`
}

// ErrNotConnected is returned when no remote is connected, or another one is.
var ErrNotConnected = errors.New("not connected")

// ErrUnknownServer is returned for a server the remote does not have.
var ErrUnknownServer = errors.New("unknown server")

// Manager holds at most one connection.
type Manager struct {
	localPort int
	ts        *tailnet.Cache
	tokens    *tokens

	mu       sync.Mutex
	c        *conn
	known    map[string]int // daemon port per peer, learned by discovery
	subs     map[chan struct{}]struct{}
	infoSubs map[chan struct{}]struct{}
}

// New creates a manager for the daemon listening on localPort that keeps
// its pairings in stateDir.
func New(localPort int, stateDir string, ts *tailnet.Cache) *Manager {
	return &Manager{localPort: localPort, ts: ts, tokens: newTokens(stateDir), known: map[string]int{},
		subs: map[chan struct{}]struct{}{}, infoSubs: map[chan struct{}]struct{}{}}
}

// Subscribe returns a channel signalled on every change, including each new
// remote state. SubscribeInfo is signalled only when Info changes.
func (m *Manager) Subscribe() chan struct{}     { return m.sub(m.subs) }
func (m *Manager) SubscribeInfo() chan struct{} { return m.sub(m.infoSubs) }

// Unsubscribe removes a channel from either set.
func (m *Manager) Unsubscribe(ch chan struct{}) {
	m.mu.Lock()
	delete(m.subs, ch)
	delete(m.infoSubs, ch)
	m.mu.Unlock()
}

func (m *Manager) sub(set map[chan struct{}]struct{}) chan struct{} {
	ch := make(chan struct{}, 1)
	m.mu.Lock()
	set[ch] = struct{}{}
	m.mu.Unlock()
	return ch
}

func (m *Manager) notify(info bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	signal := func(set map[chan struct{}]struct{}) {
		for ch := range set {
			select {
			case ch <- struct{}{}:
			default:
			}
		}
	}
	signal(m.subs)
	if info {
		signal(m.infoSubs)
	}
}

// Info describes the current connection, or nil.
func (m *Manager) Info() *Info {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.c == nil {
		return nil
	}
	info := m.c.infoLocked()
	return &info
}

// Disconnect ends the current connection.
func (m *Manager) Disconnect() {
	if c := m.current(); c != nil {
		m.disconnect(c)
	}
}

// disconnect ends c if it is still the current connection.
func (m *Manager) disconnect(c *conn) {
	m.mu.Lock()
	if m.c != c {
		m.mu.Unlock()
		return
	}
	m.c = nil
	m.mu.Unlock()
	c.close()
	log.Printf("remote: disconnected from %s", c.node.DNSName)
	m.notify(true)
}

func (m *Manager) current() *conn {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.c
}

type conn struct {
	m      *Manager
	node   tailnet.Node
	host   string // DNS name and port of the remote daemon
	ctx    context.Context
	cancel context.CancelFunc
	peerTr *http.Transport // API calls and tunnels
	devTr  *http.Transport // remote dev servers, dialled through tunnels
	since  time.Time
	port   int
	cred   share.Credential // what the remote issued to this device at pairing

	// Guarded by m.mu.
	epoch   string // the remote listener's epoch, from its last proof
	status  string
	err     string
	version string
	state   json.RawMessage
	servers map[string]int // remote server name to port
	want    map[int]Mirror
	mirrors map[int]*mirror
	held    map[int]time.Time // ports left free for a local server
}

func newConn(m *Manager, node tailnet.Node, port int, cred share.Credential, tr *http.Transport) *conn {
	ctx, cancel := context.WithCancel(context.Background())
	c := &conn{m: m, node: node, host: net.JoinHostPort(node.DNSName, strconv.Itoa(port)),
		ctx: ctx, cancel: cancel, peerTr: tr, since: time.Now(), status: Open, port: port, cred: cred,
		servers: map[string]int{}, mirrors: map[int]*mirror{}, held: map[int]time.Time{}}
	c.devTr = &http.Transport{
		DialContext: func(ctx context.Context, _, addr string) (net.Conn, error) {
			_, p, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			port, err := strconv.Atoi(p)
			if err != nil {
				return nil, err
			}
			return c.dialPort(ctx, port)
		},
		MaxIdleConnsPerHost: 32,
		IdleConnTimeout:     60 * time.Second,
	}
	return c
}

func (c *conn) close() {
	c.cancel()
	c.m.mu.Lock()
	for _, mr := range c.mirrors {
		mr.close()
	}
	c.mirrors = map[int]*mirror{}
	c.m.mu.Unlock()
	c.peerTr.CloseIdleConnections()
	c.devTr.CloseIdleConnections()
}

func (c *conn) infoLocked() Info {
	info := Info{Peer: c.node, Status: c.status, Error: c.err, Version: c.version,
		LocalPort: c.m.localPort, Mirrors: []Mirror{}, Since: c.since}
	for _, mr := range c.mirrors {
		state := "busy"
		if len(mr.lns) > 0 {
			state = "mirrored"
		}
		info.Mirrors = append(info.Mirrors, Mirror{Port: mr.port, Server: mr.server, Container: mr.container, State: state})
	}
	sort.Slice(info.Mirrors, func(i, j int) bool { return info.Mirrors[i].Port < info.Mirrors[j].Port })
	return info
}

// update changes the connection under the lock and notifies subscribers,
// telling info subscribers only when Info changed.
func (c *conn) update(fn func()) {
	c.m.mu.Lock()
	if c.m.c != c {
		c.m.mu.Unlock()
		return
	}
	before, _ := json.Marshal(c.infoLocked())
	fn()
	after, _ := json.Marshal(c.infoLocked())
	c.m.mu.Unlock()
	c.m.notify(!bytes.Equal(before, after))
}

func (c *conn) setStatus(status string, err error) {
	c.update(func() {
		c.status = status
		c.err = ""
		if err != nil {
			c.err = friendly(c.node.Name, err)
		}
	})
}

func friendly(name string, err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Msg
	}
	if errors.Is(err, errUnverified) {
		return name + " could not prove it is the computer this one paired with"
	}
	msg := err.Error()
	// The WebSocket library reports a refused handshake as "... but got <status>".
	switch {
	case strings.Contains(msg, "got 403"):
		return name + " refused the connection"
	case strings.Contains(msg, "got 502"), strings.Contains(msg, "got 404"):
		return app.Name + " on " + name + " is not running or not shared"
	}
	return name + " is not reachable"
}

// run follows the remote's event stream until the connection is closed.
func (c *conn) run() {
	go c.retryMirrors()
	delay := time.Second
	for {
		started := time.Now()
		err := c.stream()
		if c.ctx.Err() != nil {
			return
		}
		if time.Since(started) > time.Minute {
			delay = time.Second
		}
		log.Printf("remote: %s: %v", c.node.DNSName, err)
		if errors.Is(err, errRevoked) {
			// Revoked on the other side: end the connection. The credential
			// stays until pairing again replaces it, since the reply that said
			// so cannot prove where it came from.
			c.m.disconnect(c)
			return
		}
		c.setStatus(Reconnecting, err)
		select {
		case <-c.ctx.Done():
			return
		case <-time.After(delay):
		}
		delay = min(delay*2, 15*time.Second)
	}
}

// errRevoked means the remote no longer accepts this device's pairing.
var errRevoked = errors.New("the pairing was removed on the other computer")

// errUnverified means the other side could not prove it holds the key.
var errUnverified = errors.New("the other computer could not prove it is the one this computer paired with")

// dialWS opens a signed WebSocket to the remote daemon and checks that the
// daemon proved it holds the pairing key in its reply.
func (c *conn) dialWS(ctx context.Context, uri string) (*websocket.Conn, error) {
	h := peerHeader(&c.cred, c.currentEpoch(), http.MethodGet, uri, nil)
	challenge := share.Challenge(h)
	ws, res, err := websocket.Dial(ctx, "wss://"+c.host+uri, &websocket.DialOptions{
		HTTPClient: &http.Client{Transport: c.peerTr}, HTTPHeader: h})
	if err != nil {
		if res != nil && res.StatusCode == http.StatusUnauthorized {
			return nil, errRevoked
		}
		return nil, err
	}
	if _, ok := c.cred.Proven(challenge, res.Header); !ok {
		ws.CloseNow()
		return nil, errUnverified
	}
	return ws, nil
}

func (c *conn) currentEpoch() string {
	c.m.mu.Lock()
	defer c.m.mu.Unlock()
	return c.epoch
}

func (c *conn) stream() error {
	// Each (re)connection first checks that the daemon answering is the one
	// this device paired with, before trusting the state it sends.
	_, epoch, err := call(c.ctx, c.peerTr, c.node, c.port, &c.cred, "", http.MethodGet, "/api/hello", nil, nil)
	if err != nil {
		var e *Error
		if errors.As(err, &e) && e.Status == http.StatusUnauthorized {
			return errRevoked
		}
		return err
	}
	c.m.mu.Lock()
	c.epoch = epoch
	c.m.mu.Unlock()
	ws, err := c.dialWS(c.ctx, "/api/events")
	if err != nil {
		return err
	}
	defer ws.CloseNow()
	ws.SetReadLimit(64 << 20)
	c.setStatus(Open, nil)
	// A silent network partition would otherwise go unnoticed until TCP
	// keepalive gives up, minutes later.
	ctx, cancel := context.WithCancel(c.ctx)
	defer cancel()
	go func() {
		t := time.NewTicker(15 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				pctx, pcancel := context.WithTimeout(ctx, 10*time.Second)
				err := ws.Ping(pctx)
				pcancel()
				if err != nil && ctx.Err() == nil {
					ws.CloseNow()
					return
				}
			}
		}
	}()
	for {
		_, data, err := ws.Read(ctx)
		if err != nil {
			return err
		}
		var rs remoteState
		if json.Unmarshal(data, &rs) == nil {
			c.apply(data, &rs)
		}
	}
}

// apply stores a remote state and updates the port mirrors to match it.
func (c *conn) apply(data []byte, rs *remoteState) {
	servers := map[string]int{}
	want := map[int]Mirror{}
	for _, s := range rs.Servers {
		port := s.Port
		if u, err := url.Parse(s.URL); err == nil && u.Port() != "" {
			port, _ = strconv.Atoi(u.Port())
		}
		if port <= 0 {
			continue
		}
		servers[s.Name] = port
		if s.PID != 0 {
			want[port] = Mirror{Port: port, Server: s.Name}
		}
	}
	for _, ct := range rs.Docker.Containers {
		if ct.State != "running" {
			continue
		}
		for _, p := range ct.Ports {
			if _, taken := want[p.Public]; p.Proto == "tcp" && p.Public > 0 && !taken {
				want[p.Public] = Mirror{Port: p.Public, Container: ct.Name}
			}
		}
	}
	c.update(func() {
		c.state = data
		c.version = rs.Daemon.Version
		c.servers = servers
		c.want = want
		c.reconcileLocked()
	})
}

// ── port mirrors ────────────────────────────────────────────────────────

type mirror struct {
	port              int
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

// reconcileLocked binds wanted ports that are free here and releases the rest.
func (c *conn) reconcileLocked() {
	now := time.Now()
	for port, until := range c.held {
		if now.After(until) {
			delete(c.held, port)
		}
	}
	for port, mr := range c.mirrors {
		if _, ok := c.want[port]; !ok {
			mr.close()
			delete(c.mirrors, port)
		}
	}
	for port, w := range c.want {
		mr := c.mirrors[port]
		if mr == nil {
			mr = &mirror{port: port}
			c.mirrors[port] = mr
		}
		mr.server, mr.container = w.Server, w.Container
		if len(mr.lns) > 0 || port == c.m.localPort || now.Before(c.held[port]) || now.Sub(mr.tried) < 9*time.Second {
			continue
		}
		mr.tried = now
		mr.lns = c.bind(port)
	}
}

// bind listens on loopback, IPv4 and (where available) IPv6, when the port
// is free here. A port counts as taken when any address of it is bound:
// 127.0.0.1 can be bound next to a process listening on all addresses and
// would steal its traffic. Binds are tried instead of connecting, so local
// servers never see probe connections.
func (c *conn) bind(port int) []net.Listener {
	p := strconv.Itoa(port)
	ctx := context.Background()
	all, err := exclusive.Listen(ctx, "tcp", ":"+p)
	if err != nil {
		return nil
	}
	_ = all.Close()
	v4, err := exclusive.Listen(ctx, "tcp", net.JoinHostPort("127.0.0.1", p))
	if err != nil {
		return nil
	}
	lns := []net.Listener{v4}
	v6, err := exclusive.Listen(ctx, "tcp", net.JoinHostPort("::1", p))
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

// exclusive listens without SO_REUSEADDR, which Go sets by default: with it,
// BSD systems let 127.0.0.1:port bind next to a listener on 0.0.0.0:port.
var exclusive = net.ListenConfig{Control: func(_, _ string, rc syscall.RawConn) error {
	var serr error
	if err := rc.Control(func(fd uintptr) {
		serr = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_REUSEADDR, 0)
	}); err != nil {
		return err
	}
	return serr
}}

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

// dialPort opens a TCP connection to port on the remote machine's loopback.
func (c *conn) dialPort(ctx context.Context, port int) (net.Conn, error) {
	ws, err := c.dialWS(ctx, "/api/tunnel?port="+strconv.Itoa(port))
	if err != nil {
		return nil, err
	}
	return websocket.NetConn(c.ctx, ws, websocket.MessageBinary), nil
}

// retryMirrors binds ports that were busy once they become free.
func (c *conn) retryMirrors() {
	t := time.NewTicker(10 * time.Second)
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

// Release frees port for a local server about to start, and keeps it free
// long enough for that server to bind it.
func (m *Manager) Release(port int) {
	c := m.current()
	if c == nil {
		return
	}
	c.update(func() {
		c.held[port] = time.Now().Add(30 * time.Second)
		if mr := c.mirrors[port]; mr != nil {
			mr.close()
		}
	})
}

// ── relaying ────────────────────────────────────────────────────────────

// Route is how to reach a remote server: dial 127.0.0.1:Port through Transport.
type Route struct {
	Peer      string
	Port      int
	Transport http.RoundTripper
	// Done ends with the connection; requests through the route, upgraded
	// WebSockets included, must end with it.
	Done context.Context
}

// Bind returns r with a context that also ends when life does, and a
// function that releases the link. A reverse proxy closes an upgraded
// connection when its request context ends.
func Bind(r *http.Request, life context.Context) (*http.Request, func()) {
	ctx, cancel := context.WithCancel(r.Context())
	stop := context.AfterFunc(life, cancel)
	return r.WithContext(ctx), func() { stop(); cancel() }
}

// Route finds the remote server called name. label, when set, must be the
// connected device's DNS label.
func (m *Manager) Route(name, label string) (Route, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c := m.c
	if c == nil || (label != "" && !strings.EqualFold(label, c.node.Label)) {
		return Route{}, ErrNotConnected
	}
	port, ok := c.servers[name]
	if !ok {
		return Route{}, ErrUnknownServer
	}
	return Route{Peer: c.node.Name, Port: port, Transport: c.devTr, Done: c.ctx}, nil
}

// PathPrefix is where the dashboard reaches the remote daemon's API, as
// PathPrefix/<DNS name of the device it expects>/...
const PathPrefix = "/api/peer"

// maxRelayBody bounds a relayed request body; the remote accepts no more.
const maxRelayBody = 8 << 20

func writeError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// ServeAPI relays a request for PathPrefix/<DNS name>/... to the remote
// daemon's /api/..., refusing it when the named device is not the connected
// one, so a request made while a window showed another computer never runs
// on this one. The caller has already authenticated the request.
func (m *Manager) ServeAPI(w http.ResponseWriter, r *http.Request) {
	c := m.current()
	if c == nil {
		writeError(w, http.StatusConflict, "not connected to another computer")
		return
	}
	target, rest, _ := strings.Cut(strings.TrimPrefix(r.URL.EscapedPath(), PathPrefix+"/"), "/")
	if name, err := url.PathUnescape(target); err != nil || !strings.EqualFold(name, c.node.DNSName) {
		writeError(w, http.StatusConflict, "connected to "+c.node.Name+", not "+target)
		return
	}
	rest = "/" + rest
	if rest == "/events" {
		m.serveEvents(w, r, c)
		return
	}
	// The body is signed, so it is read whole (API bodies are small).
	var body []byte
	if r.Body != nil {
		b, err := io.ReadAll(io.LimitReader(r.Body, maxRelayBody+1))
		if err != nil || len(b) > maxRelayBody {
			writeError(w, http.StatusRequestEntityTooLarge, "request too large")
			return
		}
		body = b
		r.Body = io.NopCloser(bytes.NewReader(b))
	}
	m.mu.Lock()
	status, epoch := c.status, c.epoch
	m.mu.Unlock()
	if status != Open {
		// Until the event stream has reconnected to a daemon that proved
		// itself, nothing is relayed.
		writeError(w, http.StatusServiceUnavailable, c.node.Name+" is reconnecting")
		return
	}
	r, release := Bind(r, c.ctx)
	defer release()
	var challenge string
	rp := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.Out.URL.Scheme = "https"
			pr.Out.URL.Host = c.host
			pr.Out.URL.RawPath = "/api" + rest
			pr.Out.URL.Path, _ = url.PathUnescape(pr.Out.URL.RawPath)
			pr.Out.Host = c.host
			for _, h := range []string{"Cookie", "Authorization", "Origin", "Referer"} {
				pr.Out.Header.Del(h)
			}
			h := peerHeader(&c.cred, epoch, pr.Out.Method, pr.Out.URL.RequestURI(), body)
			challenge = share.Challenge(h)
			for k, v := range h {
				pr.Out.Header[k] = v
			}
		},
		Transport:     c.peerTr,
		FlushInterval: -1,
		ModifyResponse: func(res *http.Response) error {
			// Every reply, WebSocket upgrades included, must come from the
			// daemon holding the key; a refusal from the guard carries none
			// and is passed on as it is.
			if _, ok := c.cred.Proven(challenge, res.Header); !ok && res.StatusCode != http.StatusUnauthorized && res.StatusCode != http.StatusForbidden {
				return errUnverified
			}
			res.Header.Del(share.HeaderProof)
			res.Header.Del(share.HeaderEpoch)
			res.Header.Del("Set-Cookie")
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			if errors.Is(err, errUnverified) {
				writeError(w, http.StatusBadGateway, friendly(c.node.Name, err))
				return
			}
			writeError(w, http.StatusBadGateway, c.node.Name+" is not reachable")
		},
	}
	rp.ServeHTTP(w, r)
}

// serveEvents streams the remote state with the connection's Info added as
// "remote", and closes once this connection ends.
func (m *Manager) serveEvents(w http.ResponseWriter, r *http.Request, c *conn) {
	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return
	}
	defer ws.CloseNow()
	ctx := ws.CloseRead(r.Context())
	ch := m.Subscribe()
	defer m.Unsubscribe(ch)
	send := func() bool {
		data := m.wrapped(c)
		if data == nil {
			_ = ws.Close(websocket.StatusNormalClosure, "disconnected")
			return false
		}
		wctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		return ws.Write(wctx, websocket.MessageText, data) == nil
	}
	if !send() {
		return
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-ch:
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(250 * time.Millisecond):
		}
		if !send() {
			return
		}
	}
}

func (m *Manager) wrapped(c *conn) []byte {
	m.mu.Lock()
	if m.c != c {
		m.mu.Unlock()
		return nil
	}
	info := c.infoLocked()
	state := c.state
	m.mu.Unlock()
	obj := map[string]json.RawMessage{}
	if state != nil {
		_ = json.Unmarshal(state, &obj)
	}
	raw, err := json.Marshal(info)
	if err != nil {
		return nil
	}
	obj["remote"] = raw
	data, err := json.Marshal(obj)
	if err != nil {
		return nil
	}
	return data
}
