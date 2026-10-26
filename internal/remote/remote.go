// Package remote connects this daemon to the daemon on another of the
// owner's devices (see package share for the other side). While connected it
// relays the dashboard's API calls and WebSockets, serves the remote servers
// at http://<name>.<computer>.localhost through this daemon's proxy, and
// forwards each running remote server's port to the same port here, so links
// such as http://127.0.0.1:5173 open as if the servers ran on this machine,
// except ports the user turned off for that computer (mirrors.go).
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
	"time"

	"github.com/coder/websocket"

	"github.com/padmk/portside/internal/app"
	"github.com/padmk/portside/internal/csp"
	"github.com/padmk/portside/internal/notify"
	"github.com/padmk/portside/internal/share"
	"github.com/padmk/portside/internal/tailnet"
)

// Connection states.
const (
	Connecting   = "connecting"
	Open         = "open"
	Reconnecting = "reconnecting"
)

// Mirror is a remote port forwarded to this machine: to the same port, or
// to Local when the user chose another one.
type Mirror struct {
	Port      int    `json:"port"`
	Local     int    `json:"local,omitempty"`
	Server    string `json:"server,omitempty"`
	Container string `json:"container,omitempty"`
	// State is "mirrored", "busy" when the port is taken here, or "off"
	// when the user turned its forwarding off.
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

// ErrForwardingOff is returned for a server whose port the user turned off.
var ErrForwardingOff = errors.New("forwarding is off")

// Manager holds at most one connection.
type Manager struct {
	localPort int
	ts        *tailnet.Cache
	tokens    *tokens
	store     *store

	mu       sync.Mutex
	c        *conn
	known    map[string]int // daemon port per peer, learned by discovery
	wanted   func(port int) bool
	subs     notify.Signals
	infoSubs notify.Signals
}

// New creates a manager for the daemon listening on localPort that keeps
// its pairings in stateDir.
func New(localPort int, stateDir string, ts *tailnet.Cache) *Manager {
	return &Manager{localPort: localPort, ts: ts, tokens: newTokens(stateDir), store: newStore(stateDir), known: map[string]int{}}
}

// Subscribe returns a channel signalled on every change, including each new
// remote state. SubscribeInfo is signalled only when Info changes.
func (m *Manager) Subscribe() chan struct{}     { return m.subs.Subscribe() }
func (m *Manager) SubscribeInfo() chan struct{} { return m.infoSubs.Subscribe() }

// Unsubscribe removes a channel from either set.
func (m *Manager) Unsubscribe(ch chan struct{}) {
	m.subs.Unsubscribe(ch)
	m.infoSubs.Unsubscribe(ch)
}

func (m *Manager) notify(info bool) {
	m.subs.Notify()
	if info {
		m.infoSubs.Notify()
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

// Disconnect ends the current connection, and the daemon no longer
// resumes it when it starts.
func (m *Manager) Disconnect() {
	if err := m.store.setConnection(nil); err != nil {
		log.Printf("remote: %v", err)
	}
	m.Close()
}

// Close ends the current connection as the daemon stops; the next daemon
// resumes it.
func (m *Manager) Close() {
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
	ctx    context.Context
	cancel context.CancelFunc
	peerTr *http.Transport // API calls and tunnels
	devTr  *http.Transport // remote dev servers, dialled through tunnels
	since  time.Time
	cred   share.Credential // what the remote issued to this device at pairing
	found  time.Time        // when stream last looked for the daemon's port

	// Guarded by m.mu.
	host    string // DNS name and port of the remote daemon
	port    int
	epoch   string // the remote listener's epoch, from its last proof
	status  string
	down    time.Time // when the connection was lost; zero while open
	err     string
	version string
	state   json.RawMessage
	servers map[string]int               // remote server name to port
	want    map[int]Mirror               // by remote port
	mirrors map[int]*mirror              // by remote port
	locals  map[int]int                  // remote port to the local port chosen for it
	off     map[int]bool                 // remote ports the user turned off
	held    map[int]time.Time            // local ports left free for a local server
	tunnels map[int]map[*tunnel]struct{} // open connections by remote port
}

func newConn(m *Manager, node tailnet.Node, port int, cred share.Credential, tr *http.Transport) *conn {
	ctx, cancel := context.WithCancel(context.Background())
	c := &conn{m: m, node: node, host: net.JoinHostPort(node.DNSName, strconv.Itoa(port)),
		ctx: ctx, cancel: cancel, peerTr: tr, since: time.Now(), status: Open, port: port, cred: cred,
		servers: map[string]int{}, mirrors: map[int]*mirror{}, held: map[int]time.Time{},
		locals: m.store.locals(node.DNSName), off: m.store.off(node.DNSName), tunnels: map[int]map[*tunnel]struct{}{}}
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
		switch {
		case c.off[mr.port]:
			state = "off"
		case len(mr.lns) > 0:
			state = "mirrored"
		}
		local := 0
		if mr.local != mr.port {
			local = mr.local
		}
		info.Mirrors = append(info.Mirrors, Mirror{Port: mr.port, Local: local, Server: mr.server, Container: mr.container, State: state})
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
		switch {
		case status == Open:
			c.down = time.Time{}
		case c.down.IsZero():
			c.down = time.Now()
		}
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
			// Revoked on the other side: end the connection and stop resuming
			// it. The credential stays until pairing again replaces it, since
			// the reply that said so cannot prove where it came from.
			if err := c.m.store.forget(c.node.DNSName); err != nil {
				log.Printf("remote: %v", err)
			}
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

// errPeerRefused means the other side refused a relayed request without a
// proof, as its guard does: unauthorized (the pairing may be gone) or, for
// errPeerRejected, forbidden (sharing restarted or the clocks differ).
var (
	errPeerRefused  = errors.New("the other computer refused the request")
	errPeerRejected = errors.New("the other computer rejected the request")
)

// dialWS opens a signed WebSocket to the remote daemon and checks that the
// daemon proved it holds the pairing key in its reply.
func (c *conn) dialWS(ctx context.Context, uri string) (*websocket.Conn, error) {
	h := peerHeader(&c.cred, c.currentEpoch(), http.MethodGet, uri, nil)
	challenge := share.Challenge(h)
	host, _ := c.address()
	ws, res, err := websocket.Dial(ctx, "wss://"+host+uri, &websocket.DialOptions{
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

// address returns the remote daemon's host (DNS name and port) and port.
func (c *conn) address() (string, int) {
	c.m.mu.Lock()
	defer c.m.mu.Unlock()
	return c.host, c.port
}

// moveTo follows the remote daemon to port, where it answered after taking
// another automatic port, and keeps that port for resuming unless the
// connection was ended or replaced meanwhile.
func (c *conn) moveTo(port int) {
	if c.ctx.Err() != nil {
		return
	}
	c.m.mu.Lock()
	c.host, c.port = net.JoinHostPort(c.node.DNSName, strconv.Itoa(port)), port
	c.m.mu.Unlock()
	log.Printf("remote: %s now answers on port %d", c.node.DNSName, port)
	if err := c.m.store.movePort(c.node.DNSName, port); err != nil {
		log.Printf("remote: %v", err)
	}
}

func (c *conn) currentEpoch() string {
	c.m.mu.Lock()
	defer c.m.mu.Unlock()
	return c.epoch
}

func (c *conn) stream() error {
	// Each (re)connection first checks that the daemon answering is the one
	// this device paired with, before trusting the state it sends.
	_, port := c.address()
	_, epoch, err := call(c.ctx, c.peerTr, c.node, port, &c.cred, "", http.MethodGet, "/api/hello", nil, nil)
	if err != nil {
		var e *Error
		if errors.As(err, &e) && e.Status == http.StatusUnauthorized {
			return errRevoked
		}
		// The daemon may have restarted on another automatic port; follow
		// it only where it proves it holds the pairing key. Looking probes
		// every candidate port, so a computer that stays away is asked at
		// most once a minute.
		if time.Since(c.found) < time.Minute {
			return err
		}
		c.found = time.Now()
		found, _, foundEpoch, ferr := c.m.findPort(c.ctx, c.peerTr, c.node, &c.cred)
		if ferr != nil {
			return err
		}
		if found != port {
			c.moveTo(found)
		}
		epoch = foundEpoch
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
// connected device's DNS label. A server whose port is turned off gets
// ErrForwardingOff, with Peer and Port set for the answer.
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
	if c.off[port] {
		return Route{Peer: c.node.Name, Port: port}, ErrForwardingOff
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
	status, epoch, host := c.status, c.epoch, c.host
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
			pr.Out.URL.Host = host
			pr.Out.URL.RawPath = "/api" + rest
			pr.Out.URL.Path, _ = url.PathUnescape(pr.Out.URL.RawPath)
			pr.Out.Host = host
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
			// daemon holding the key. A refusal from the guard carries no
			// proof; its body is not passed on, since this daemon would
			// prove it to its own client as if it were its reply.
			if _, ok := c.cred.Proven(challenge, res.Header); !ok {
				switch res.StatusCode {
				case http.StatusUnauthorized:
					return errPeerRefused
				case http.StatusForbidden:
					return errPeerRejected
				}
				return errUnverified
			}
			sanitizeRelayed(res.Header)
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			if errors.Is(err, errUnverified) {
				writeError(w, http.StatusBadGateway, friendly(c.node.Name, err))
				return
			}
			if errors.Is(err, errPeerRefused) {
				writeError(w, http.StatusBadGateway, c.node.Name+" refused the request; pair with it again if this continues")
				return
			}
			if errors.Is(err, errPeerRejected) {
				writeError(w, http.StatusBadGateway, c.node.Name+" rejected the request; it may be reconnecting, or the two computers' clocks differ")
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

// sanitizeRelayed makes a reply from another computer safe to serve on the
// dashboard's origin. That computer is trusted to run servers, not to run
// script here: whatever it sends (an icon that is really HTML, a page it
// links to) is sandboxed, cannot set cookies or site data, and cannot
// redirect or preload. The proof headers stay between the two daemons.
func sanitizeRelayed(h http.Header) {
	for _, k := range []string{share.HeaderProof, share.HeaderEpoch, "Set-Cookie", "Clear-Site-Data", "Refresh", "Link", "Content-Security-Policy-Report-Only"} {
		h.Del(k)
	}
	h.Set("Content-Security-Policy", csp.Untrusted)
	h.Set("X-Content-Type-Options", "nosniff")
}
