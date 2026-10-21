// Package share lets the owner's other devices connect to this daemon.
//
// Tailscale Serve publishes https://<this machine>:<daemon port> on the
// tailnet and forwards it to a loopback listener under a random path that
// only the Serve configuration knows. (A Unix socket would keep other local
// accounts out by itself, but macOS's sandboxed Tailscale cannot open one and
// Linux lets only administrators serve one.) A request is accepted when all
// of these hold:
//
//   - it arrived under that path, for this machine's tailnet name or address;
//   - it carries the peer header, which a web page cannot add without a CORS
//     preflight, so a browser on one of the devices cannot be abused;
//   - Tailscale reports it from another device of the user who owns this
//     machine, not tagged (Serve sets Tailscale-User-Login and
//     X-Forwarded-For itself); requests from this machine are refused, so
//     local accounts, apps and containers cannot use the tailnet way in;
//   - it is signed with the key this daemon issued to that same device when
//     it was paired with a one-time code (see devices.go and sign.go). Only
//     the hello and pairing endpoints work without a signature.
package share

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/padmk/portside/internal/app"
	"github.com/padmk/portside/internal/paths"
	"github.com/padmk/portside/internal/signing"
	"github.com/padmk/portside/internal/tailnet"
)

// Paths that work for an owned device without a signature.
const (
	HelloPath = "/api/hello"
	PairPath  = "/api/pair"
)

// Status describes sharing for the dashboard.
type Status struct {
	Enabled bool     `json:"enabled"`
	Active  bool     `json:"active"`
	URL     string   `json:"url,omitempty"`
	Error   string   `json:"error,omitempty"`
	Devices []Device `json:"devices"`
}

// Manager owns the listener, this daemon's Tailscale Serve entry and the
// paired devices.
type Manager struct {
	port   int // tailnet port: the daemon's own port
	record string
	ts     *tailnet.Cache
	devs   *devices
	nonces signing.Verifier

	applying chan struct{} // held while Apply, Close or Revoke runs

	mu      sync.Mutex
	closed  bool // Close ran; nothing may enable sharing again
	want    bool
	handler http.Handler
	srv     *http.Server
	stop    context.CancelFunc // ends requests in flight, including WebSockets
	target  string
	secret  string // path prefix the loopback listener requires, without slashes
	epoch   string // random per listener; see HeaderEpoch
	live    map[string]*liveDevice
	st      Status
}

// New creates a manager for a daemon on port that keeps its files in stateDir.
func New(port int, stateDir string, ts *tailnet.Cache) *Manager {
	return &Manager{port: port, record: filepath.Join(stateDir, "share-target"),
		ts: ts, devs: newDevices(stateDir), applying: make(chan struct{}, 1)}
}

// SetHandler sets the handler for accepted requests.
func (m *Manager) SetHandler(h http.Handler) {
	m.mu.Lock()
	m.handler = h
	m.mu.Unlock()
}

// Set records whether sharing should be on; Apply makes it so.
func (m *Manager) Set(enabled bool) {
	m.mu.Lock()
	m.want = enabled
	m.mu.Unlock()
}

func (m *Manager) wanted() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.want
}

// Status returns the current state.
func (m *Manager) Status() Status {
	m.mu.Lock()
	st := m.st
	m.mu.Unlock()
	st.Devices = m.devs.List()
	return st
}

func (m *Manager) setStatus(st Status) {
	m.mu.Lock()
	m.st = st
	m.mu.Unlock()
}

// NewCode issues a pairing code; sharing must be on and reachable, so the
// code is only ever entered for a daemon that is really serving.
func (m *Manager) NewCode() (Code, error) {
	if !m.wanted() {
		return Code{}, errors.New("sharing is off")
	}
	if st := m.Status(); !st.Active {
		msg := "sharing is not active yet"
		if st.Error != "" {
			msg += ": " + st.Error
		}
		return Code{}, errors.New(msg)
	}
	return m.devs.NewCode()
}

// Revoke removes a paired device by ID, DNS name or host name and ends its
// requests in flight.
func (m *Manager) Revoke(ref string) (bool, error) {
	ok, err := m.devs.Revoke(ref)
	if ok && err == nil {
		m.endRevoked()
	}
	return ok, err
}

// deviceCtx returns a context that ends when device id is revoked; every
// request of that device runs under it.
func (m *Manager) deviceCtx(id string) context.Context {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.live == nil {
		m.live = map[string]*liveDevice{}
	}
	d := m.live[id]
	if d == nil {
		ctx, cancel := context.WithCancel(context.Background())
		d = &liveDevice{ctx: ctx, cancel: cancel}
		m.live[id] = d
	}
	return d.ctx
}

// endRevoked ends the requests in flight, WebSockets included, of devices
// that are no longer paired. The listener stays: moving to a new one would
// leave Tailscale Serve pointing at a freed port for a moment.
func (m *Manager) endRevoked() {
	paired := map[string]bool{}
	for _, d := range m.devs.List() {
		paired[d.ID] = true
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, d := range m.live {
		if !paired[id] {
			d.cancel()
			delete(m.live, id)
		}
	}
}

type liveDevice struct {
	ctx    context.Context
	cancel context.CancelFunc
}

// lockApply serialises Apply, Close and Revoke, giving up when ctx ends.
func (m *Manager) lockApply(ctx context.Context) bool {
	select {
	case m.applying <- struct{}{}:
		return true
	case <-ctx.Done():
		return false
	}
}

func (m *Manager) unlockApply() { <-m.applying }

// Apply makes the live state match the setting. Problems are kept in Status.
func (m *Manager) Apply(ctx context.Context) Status {
	if !m.lockApply(ctx) {
		return m.Status()
	}
	defer m.unlockApply()
	m.mu.Lock()
	closed := m.closed
	m.mu.Unlock()
	switch {
	case closed:
	case m.wanted():
		if err := m.enable(ctx); err != nil {
			m.setStatus(Status{Enabled: true, Error: err.Error()})
		}
	default:
		if err := m.disable(ctx); err != nil {
			m.setStatus(Status{Error: "could not withdraw the Tailscale Serve entry: " + err.Error()})
		} else {
			m.setStatus(Status{})
		}
	}
	return m.Status()
}

// Run re-applies sharing periodically, so it comes up once Tailscale starts,
// returns if someone resets Tailscale Serve, and retries a withdrawal that
// failed.
func (m *Manager) Run(ctx context.Context) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if m.wanted() || len(m.recorded()) > 0 {
				m.Apply(ctx)
			}
		}
	}
}

// Close withdraws the Serve entry and stops the listener for good: an Apply
// still queued afterwards does nothing. It gives up waiting when ctx ends.
func (m *Manager) Close(ctx context.Context) {
	m.mu.Lock()
	m.closed = true
	m.mu.Unlock()
	if !m.lockApply(ctx) {
		m.closeListener()
		log.Printf("share: shutting down before the Tailscale Serve entry could be withdrawn")
		return
	}
	defer m.unlockApply()
	if err := m.disable(ctx); err != nil {
		log.Printf("share: could not withdraw the Tailscale Serve entry: %v", err)
	}
}

func (m *Manager) enable(ctx context.Context) error {
	st, err := m.ts.Fresh(ctx)
	if err != nil {
		return err
	}
	if st.Owner == "" {
		return errors.New("this device is tagged in Tailscale; sharing needs a device owned by a user")
	}
	target, err := m.listen()
	if err != nil {
		return err
	}
	e, err := tailnet.ServeTarget(ctx, st.Self.DNSName, m.port)
	if err != nil {
		return err
	}
	recs := m.recorded()
	// Only an entry this daemon recorded may be replaced; anything else on
	// the port, including a configuration too complex to read, is left alone.
	if e.Served && e.Target != target && (e.Target == "" || !recs[e.Target]) {
		what := "another service"
		if e.Target != "" {
			what = e.Target
		}
		return fmt.Errorf("port %d on the tailnet is already served by Tailscale Serve for %s", m.port, what)
	}
	if e.Target != target || !e.HTTPS {
		// Recorded first, next to the entry being replaced: an entry must
		// never exist without the record that lets a later run withdraw it,
		// whether or not the replacement succeeds.
		recs[target] = true
		if e.Target != "" {
			recs[e.Target] = true
		}
		if err := m.writeRecord(recs); err != nil {
			return err
		}
		if e.Served && !e.HTTPS {
			// A plain HTTP entry of ours cannot be switched to HTTPS in place.
			if err := tailnet.Unserve(ctx, m.port, false); err != nil {
				return err
			}
		}
		m.mu.Lock()
		closed := m.closed
		m.mu.Unlock()
		if closed {
			return errors.New("shutting down")
		}
		if err := tailnet.Serve(ctx, m.port, target); err != nil {
			return fmt.Errorf("%w (Tailscale needs HTTPS certificates enabled for the tailnet; on Linux also, once, `sudo tailscale set --operator=$USER`)", err)
		}
		m.mu.Lock()
		closed = m.closed
		m.mu.Unlock()
		if closed {
			// Close gave up waiting while the entry was being added: take it
			// back now rather than leave it behind.
			if err := tailnet.Unserve(context.WithoutCancel(ctx), m.port, true); err != nil {
				log.Printf("share: could not withdraw the Tailscale Serve entry: %v", err)
			} else {
				_ = os.Remove(m.record)
			}
			m.closeListener()
			return errors.New("shutting down")
		}
	}
	if len(recs) > 1 || !recs[target] {
		if err := m.writeRecord(map[string]bool{target: true}); err != nil {
			return err
		}
	}
	url := "https://" + st.Self.DNSName + ":" + strconv.Itoa(m.port)
	m.setStatus(Status{Enabled: true, Active: true, URL: url})
	go warmCertificate(url)
	return nil
}

// warmCertificate makes one request to the new entry, so Tailscale fetches
// the certificate now rather than during another device's first, short
// timed connection. The guard refuses the request itself; only the TLS
// handshake matters.
func warmCertificate(url string) {
	c := &http.Client{Timeout: time.Minute}
	if res, err := c.Get(url + HelloPath); err == nil {
		res.Body.Close()
	}
}

// disable stops the listener and withdraws the Serve entry if it is ours.
// The record is kept until the entry is gone, so a failure is retried.
func (m *Manager) disable(ctx context.Context) error {
	// The entry goes first and the listener after it, so Serve never points
	// at a port that is already free for anyone to bind.
	defer m.closeListener()
	m.mu.Lock()
	target := m.target
	m.mu.Unlock()
	recs := m.recorded()
	if target == "" && len(recs) == 0 {
		return nil
	}
	st, err := m.ts.Get(ctx)
	if err != nil {
		return err
	}
	e, err := tailnet.ServeTarget(ctx, st.Self.DNSName, m.port)
	if err != nil {
		return err
	}
	if e.Served && e.Target != "" && (e.Target == target || recs[e.Target]) {
		if err := tailnet.Unserve(ctx, m.port, e.HTTPS); err != nil {
			return err
		}
	}
	if err := os.Remove(m.record); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// recorded returns the Serve targets this daemon may own, one per line in
// the record file.
func (m *Manager) recorded() map[string]bool {
	out := map[string]bool{}
	b, _ := os.ReadFile(m.record)
	for _, l := range strings.Split(string(b), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out[l] = true
		}
	}
	return out
}

func (m *Manager) writeRecord(targets map[string]bool) error {
	var sb strings.Builder
	for t := range targets {
		sb.WriteString(t + "\n")
	}
	return paths.WriteFile(m.record, []byte(sb.String()))
}

// closeListener stops the listener and returns the target it served.
func (m *Manager) closeListener() string {
	m.mu.Lock()
	target, srv, stop := m.target, m.srv, m.stop
	m.target, m.srv, m.stop, m.secret, m.epoch = "", nil, nil, "", ""
	m.mu.Unlock()
	if srv != nil {
		stop()
		_ = srv.Close()
	}
	return target
}

// listen starts the listener once and returns the Serve target for it.
func (m *Manager) listen() (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return "", errors.New("shutting down")
	}
	if m.srv != nil {
		return m.target, nil
	}
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	secret := hex.EncodeToString(b)
	target := "http://127.0.0.1:" + strconv.Itoa(ln.Addr().(*net.TCPAddr).Port) + "/" + secret
	base, stop := context.WithCancel(context.Background())
	m.stop = stop
	m.srv = &http.Server{
		Handler:           m.guard(),
		ReadHeaderTimeout: 10 * time.Second,
		BaseContext:       func(net.Listener) context.Context { return base },
	}
	m.target, m.secret, m.epoch = target, secret, signing.NewEpoch()
	go func(srv *http.Server) {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("share: %v", err)
		}
	}(m.srv)
	return target, nil
}

// ── accepted requests ───────────────────────────────────────────────────

// Peer describes the device behind an accepted request.
type Peer struct {
	Node tailnet.Node
	// Device is the pairing the request presented, nil on the hello and
	// pairing endpoints when there is none.
	Device *Device
}

type peerKey struct{}

// PeerFrom returns the device behind a request the guard accepted.
func PeerFrom(ctx context.Context) (Peer, bool) {
	p, ok := ctx.Value(peerKey{}).(Peer)
	return p, ok
}

// Pair redeems a code for the daemon instance behind an accepted request
// and returns the credential it signs with from now on.
func (m *Manager) Pair(p Peer, code, instance string) (Credential, error) {
	id, key, err := m.devs.Pair(code, Device{Name: p.Node.Name, DNSName: p.Node.DNSName, OS: p.Node.OS, Instance: instance})
	return Credential{ID: id, Key: key}, err
}

func writeJSONError(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg, "code": code})
}

func hostOnly(h string) string {
	if host, _, err := net.SplitHostPort(h); err == nil {
		h = host
	}
	return strings.ToLower(strings.Trim(h, "[]"))
}

func (m *Manager) guard() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		refuse := func(msg string) { writeJSONError(w, http.StatusForbidden, "refused", msg) }
		if r.Header.Get(app.PeerHeader) == "" {
			refuse("this address only accepts connections from " + app.Name + " on your other devices")
			return
		}
		m.mu.Lock()
		h, secret, epoch := m.handler, m.secret, m.epoch
		m.mu.Unlock()
		// The request URI as the device signed it, before the secret prefix.
		uri := r.RequestURI
		if secret != "" {
			uri = strings.TrimPrefix(uri, "/"+secret)
			seg, _, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, "/"), "/")
			if subtle.ConstantTimeCompare([]byte(seg), []byte(secret)) != 1 {
				refuse("connection refused")
				return
			}
			r.URL.Path = strings.TrimPrefix(r.URL.Path, "/"+secret)
			r.URL.RawPath = strings.TrimPrefix(r.URL.RawPath, "/"+secret)
		}
		st, err := m.ts.Get(r.Context())
		if err != nil {
			refuse(err.Error())
			return
		}
		// Serve routes by host already; checking again keeps a rebound name
		// out even if that changes. Serve sets X-Forwarded-Host to the name the
		// device used; Host may be rewritten for the backend.
		fwd := r.Header.Get("X-Forwarded-Host")
		if fwd == "" {
			fwd = r.Host
		}
		host := hostOnly(strings.TrimSpace(fwd[strings.LastIndex(fwd, ",")+1:]))
		hostOK := host == strings.ToLower(st.Self.DNSName) || host == st.Self.Label
		for _, ip := range st.Self.IPs {
			hostOK = hostOK || host == ip
		}
		if !hostOK {
			refuse("unknown host")
			return
		}
		login := r.Header.Get("Tailscale-User-Login")
		if login == "" || login != st.Owner {
			refuse("only devices of this computer's Tailscale account may connect")
			return
		}
		// Serve appends the real source address last.
		xff := r.Header.Get("X-Forwarded-For")
		src := strings.TrimSpace(xff[strings.LastIndex(xff, ",")+1:])
		node, ok := st.ByIP(src)
		if !ok {
			if fresh, err := m.ts.Fresh(r.Context()); err == nil {
				st = fresh
				node, ok = st.ByIP(src)
			}
		}
		switch {
		case !ok:
			refuse("unknown device")
			return
		case node.DNSName == st.Self.DNSName:
			refuse("connections from this computer itself are not accepted")
			return
		case !st.Owned(node):
			refuse("only devices of this computer's Tailscale account may connect")
			return
		}
		peer := Peer{Node: node}
		open := r.URL.Path == HelloPath || r.URL.Path == PairPath
		if id := r.Header.Get(HeaderDevice); id != "" {
			// An unknown or revoked device is simply unpaired (401 outside the
			// open endpoints, which tells it to pair again); a known device
			// whose signature fails is refused (403) and keeps its pairing.
			if dev, ok := m.devs.Lookup(id); ok && strings.EqualFold(dev.DNSName, node.DNSName) {
				var body []byte
				if r.Body != nil {
					body, err = io.ReadAll(io.LimitReader(r.Body, signing.MaxBody+1))
					if err != nil || len(body) > signing.MaxBody {
						refuse("request body too large")
						return
					}
					r.Body = io.NopCloser(bytes.NewReader(body))
				}
				if err := verify(&m.nonces, r, dev, uri, body); err != nil {
					writeJSONError(w, http.StatusForbidden, "signature", err.Error())
					return
				}
				// Outside hello, a request must be meant for this listener;
				// hello is how a device learns the current epoch.
				if !open && signing.CheckEpoch(r.Header, epoch) != nil {
					writeJSONError(w, http.StatusForbidden, "epoch", "the shared computer restarted sharing; reconnecting")
					return
				}
				peer.Device = &dev
				signing.Prove(w.Header(), r.Header, dev.Key, epoch)
				ctx, cancel := context.WithCancel(r.Context())
				defer cancel()
				defer context.AfterFunc(m.deviceCtx(dev.ID), cancel)()
				r = r.WithContext(ctx)
			}
		}
		if peer.Device == nil && !open {
			writeJSONError(w, http.StatusUnauthorized, "pairing_required", "pair this device first: get a code on "+st.Self.Name)
			return
		}
		if h == nil {
			refuse("not ready")
			return
		}
		h.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), peerKey{}, peer)))
	})
}
