package remote

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/padmk/portside/internal/app"
	"github.com/padmk/portside/internal/share"
	"github.com/padmk/portside/internal/tailnet"
)

// Finding, pairing with and connecting to other devices. Only devices of the
// same Tailscale user that are not tagged are considered: anything else
// could decide which local ports this daemon binds.

// Counts summarises a peer's servers.
type Counts struct {
	Total   int `json:"total"`
	Running int `json:"running"`
}

// Peer is a device that shares its daemon.
type Peer struct {
	tailnet.Node
	Version string `json:"version"`
	Paired  bool   `json:"paired"`
	// Servers is known only once paired.
	Servers   *Counts `json:"servers,omitempty"`
	Connected bool    `json:"connected"`
}

// Discovery is the answer to a scan of the tailnet.
type Discovery struct {
	Tailnet struct {
		Available bool   `json:"available"`
		Error     string `json:"error,omitempty"`
		Self      string `json:"self,omitempty"`
	} `json:"tailnet"`
	Peers []Peer `json:"peers"`
}

// Error carries the HTTP status and machine-readable code a failure maps to.
type Error struct {
	Status int
	Code   string
	Msg    string
}

func (e *Error) Error() string { return e.Msg }

// Codes of Error.
const (
	CodePairingRequired = "pairing_required"
	CodePairingFailed   = "pairing_failed"
	// CodeUnverified: something answered on the device's address but could
	// not prove it holds the pairing key. Pairing again would not help.
	CodeUnverified = "unverified"
)

// ── pairing credentials ─────────────────────────────────────────────────

// tokens keeps the credential each shared device issued to this one, by DNS
// name, in a file only this account can read.
type tokens struct {
	path string
	mu   sync.Mutex
}

func (t *tokens) load() map[string]share.Credential {
	out := map[string]share.Credential{}
	if b, err := os.ReadFile(t.path); err == nil {
		_ = json.Unmarshal(b, &out)
	}
	return out
}

// Get returns the credential for a device; ok is false when there is none.
func (t *tokens) Get(dnsName string) (share.Credential, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	c, ok := t.load()[strings.ToLower(dnsName)]
	return c, ok && c.ID != "" && c.Key != ""
}

// Set stores a credential; the zero value removes it.
func (t *tokens) Set(dnsName string, cred share.Credential) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	all := t.load()
	if cred.ID == "" {
		delete(all, strings.ToLower(dnsName))
	} else {
		all[strings.ToLower(dnsName)] = cred
	}
	b, err := json.MarshalIndent(all, "", "  ")
	if err != nil {
		return err
	}
	tmp := t.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, t.path)
}

func newTokens(stateDir string) *tokens {
	return &tokens{path: filepath.Join(stateDir, "remote-tokens.json")}
}

// instance identifies this daemon among others on the same device, so each
// pairs separately; it is created once and kept in the state directory.
func (m *Manager) instance() string {
	p := filepath.Join(filepath.Dir(m.tokens.path), "remote-instance")
	if b, err := os.ReadFile(p); err == nil && len(strings.TrimSpace(string(b))) == 16 {
		return strings.TrimSpace(string(b))
	}
	id := randomID()
	_ = os.WriteFile(p, []byte(id+"\n"), 0o600)
	return id
}

func randomID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// ── requests to a shared device ─────────────────────────────────────────

// remoteState is the part of a daemon's State this package reads.
type remoteState struct {
	Daemon struct {
		App     string `json:"app"`
		Version string `json:"version"`
	} `json:"daemon"`
	Servers []struct {
		Name   string `json:"name"`
		Port   int    `json:"port"`
		URL    string `json:"url"`
		PID    int    `json:"pid"`
		Status string `json:"status"`
	} `json:"servers"`
	Docker struct {
		Containers []struct {
			Name  string `json:"name"`
			State string `json:"state"`
			Ports []struct {
				Public int    `json:"public"`
				Proto  string `json:"proto"`
			} `json:"ports"`
		} `json:"containers"`
	} `json:"docker"`
}

type hello struct {
	App     string  `json:"app"`
	Version string  `json:"version"`
	Paired  bool    `json:"paired"`
	Servers *Counts `json:"servers"`
}

// peerHeader returns the headers of a request to a shared device: the peer
// marker and, with a credential, a signature over method, URI and body.
func peerHeader(cred *share.Credential, epoch, method, uri string, body []byte) http.Header {
	h := http.Header{app.PeerHeader: {"1"}}
	if cred != nil {
		cred.Sign(h, method, uri, body, epoch)
	}
	return h
}

// peerTransport dials the node's tailnet addresses directly, so connections
// work whether or not this machine resolves MagicDNS names.
func peerTransport(node tailnet.Node) *http.Transport {
	d := &net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}
	ips := append([]string(nil), node.IPs...)
	return &http.Transport{
		DialContext: func(ctx context.Context, _, addr string) (net.Conn, error) {
			_, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			last := errors.New("the device has no tailnet address")
			for _, ip := range ips {
				c, err := d.DialContext(ctx, "tcp", net.JoinHostPort(ip, port))
				if err == nil {
					return c, nil
				}
				last = err
			}
			return nil, last
		},
		// Something that accepts the connection but never completes the TLS
		// handshake must not hold a request forever.
		TLSHandshakeTimeout: 10 * time.Second,
		MaxIdleConnsPerHost: 16,
		IdleConnTimeout:     90 * time.Second,
	}
}

// call makes one request to a device's daemon and decodes a JSON reply.
// With a credential the request is signed for epoch and the daemon has to
// prove it holds the same key; the epoch it proves is returned.
func call(ctx context.Context, tr http.RoundTripper, node tailnet.Node, port int, cred *share.Credential, epoch, method, path string, body, out any) ([]byte, string, error) {
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	var raw []byte
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, "", err
		}
		raw = b
	}
	u := "https://" + net.JoinHostPort(node.DNSName, strconv.Itoa(port)) + path
	req, err := http.NewRequestWithContext(ctx, method, u, bytes.NewReader(raw))
	if err != nil {
		return nil, "", err
	}
	req.Header = peerHeader(cred, epoch, method, req.URL.RequestURI(), raw)
	challenge := ""
	if cred != nil {
		challenge = share.Challenge(req.Header)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := (&http.Client{Transport: tr}).Do(req)
	if err != nil {
		return nil, "", &Error{Status: http.StatusBadGateway, Msg: node.Name + " is not reachable"}
	}
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, 64<<20))
	if err != nil {
		return nil, "", &Error{Status: http.StatusBadGateway, Msg: node.Name + " is not reachable"}
	}
	var e struct {
		Error string `json:"error"`
		Code  string `json:"code"`
	}
	switch {
	case res.StatusCode == http.StatusUnauthorized:
		return nil, "", &Error{Status: http.StatusUnauthorized, Code: CodePairingRequired, Msg: "pair with " + node.Name + " first"}
	case res.StatusCode == http.StatusForbidden:
		_ = json.Unmarshal(data, &e)
		msg := node.Name + " refused the connection"
		if e.Code == CodePairingFailed {
			return nil, "", &Error{Status: http.StatusForbidden, Code: CodePairingFailed, Msg: e.Error}
		}
		if e.Error != "" {
			msg += ": " + e.Error
		}
		return nil, "", &Error{Status: http.StatusForbidden, Msg: msg}
	case res.StatusCode != http.StatusOK:
		return nil, "", &Error{Status: http.StatusBadGateway, Msg: app.Name + " on " + node.Name + " is not shared"}
	}
	if cred != nil && path == "/api/hello" && res.Header.Get(share.HeaderProof) == "" {
		// The daemon no longer knows this device: the pairing was removed.
		var h hello
		if json.Unmarshal(data, &h) == nil && h.App == app.ID && !h.Paired {
			// Either the pairing was removed there or something else answers;
			// the reply cannot tell which. Pairing again needs a code shown
			// on that computer, so it stays the user's explicit choice.
			return nil, "", &Error{Status: http.StatusUnauthorized, Code: CodePairingRequired,
				Msg: node.Name + " does not recognise this computer's pairing; if it was removed there, pair again with a new code"}
		}
	}
	proven, ok := "", true
	if cred != nil {
		proven, ok = cred.Proven(challenge, res.Header)
	}
	if !ok {
		// Something answers on the device's address without its key: not the
		// daemon this device paired with.
		return nil, "", &Error{Status: http.StatusBadGateway, Code: CodeUnverified, Msg: node.Name + " could not prove it is the computer this one paired with"}
	}
	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			return nil, "", &Error{Status: http.StatusBadGateway, Msg: app.Name + " on " + node.Name + " is not shared"}
		}
	}
	return data, proven, nil
}

func (m *Manager) ports(prefer int) []int {
	out := []int{}
	for _, p := range []int{prefer, m.localPort, app.DefaultPort} {
		if p > 0 && !containsInt(out, p) {
			out = append(out, p)
		}
	}
	return out
}

func containsInt(s []int, v int) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

// findPort asks each candidate port for the daemon's hello, all at once:
// devices without a shared daemon may drop packets instead of refusing.
func (m *Manager) findPort(ctx context.Context, tr http.RoundTripper, n tailnet.Node, cred *share.Credential) (int, *hello, string, error) {
	m.mu.Lock()
	prefer := m.known[n.DNSName]
	m.mu.Unlock()
	cands := m.ports(prefer)
	found := make([]*hello, len(cands))
	epochs := make([]string, len(cands))
	errs := make([]error, len(cands))
	var wg sync.WaitGroup
	for i, port := range cands {
		wg.Add(1)
		go func(i, port int) {
			defer wg.Done()
			var h hello
			if _, epoch, err := call(ctx, tr, n, port, cred, "", http.MethodGet, "/api/hello", nil, &h); err != nil {
				errs[i] = err
			} else if h.App == app.ID {
				found[i], epochs[i] = &h, epoch
			}
		}(i, port)
	}
	wg.Wait()
	for i, h := range found {
		if h != nil {
			m.mu.Lock()
			m.known[n.DNSName] = cands[i]
			m.mu.Unlock()
			return cands[i], h, epochs[i], nil
		}
	}
	// A failed proof, a refusal or a removed pairing says more than an
	// unreachable port, in that order.
	for _, want := range []func(*Error) bool{
		func(e *Error) bool { return e.Code == CodeUnverified },
		func(e *Error) bool { return e.Status == http.StatusForbidden || e.Status == http.StatusUnauthorized },
	} {
		for _, err := range errs {
			var e *Error
			if errors.As(err, &e) && want(e) {
				return 0, nil, "", err
			}
		}
	}
	return 0, nil, "", &Error{Status: http.StatusBadGateway, Msg: app.Name + " on " + n.Name + " is not running or not shared"}
}

// Discover lists the owner's online devices whose daemon is shared.
func (m *Manager) Discover(ctx context.Context) Discovery {
	var d Discovery
	d.Peers = []Peer{}
	st, err := m.ts.Fresh(ctx)
	if err != nil {
		d.Tailnet.Error = err.Error()
		return d
	}
	d.Tailnet.Available = true
	d.Tailnet.Self = st.Self.Name
	connected := ""
	if info := m.Info(); info != nil {
		connected = info.Peer.DNSName
	}
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, n := range st.Peers {
		if !n.Online || n.DNSName == "" || !st.Owned(n) {
			continue
		}
		wg.Add(1)
		go func(n tailnet.Node) {
			defer wg.Done()
			tr := peerTransport(n)
			defer tr.CloseIdleConnections()
			var cred *share.Credential
			if c, ok := m.tokens.Get(n.DNSName); ok {
				cred = &c
			}
			_, h, _, err := m.findPort(ctx, tr, n, cred)
			var e *Error
			switch {
			case err != nil && cred != nil && errors.As(err, &e) && e.Code == CodeUnverified:
				// Listed as paired: connecting shows why it cannot be trusted,
				// instead of an invitation to pair with whatever answered.
				mu.Lock()
				d.Peers = append(d.Peers, Peer{Node: n, Paired: true, Connected: n.DNSName == connected})
				mu.Unlock()
				return
			case err != nil && cred != nil && errors.As(err, &e) && e.Status == http.StatusUnauthorized:
				// Not recognised there: list it as unpaired.
				_, h, _, err = m.findPort(ctx, tr, n, nil)
			}
			if err != nil {
				return
			}
			p := Peer{Node: n, Version: h.Version, Paired: h.Paired, Connected: n.DNSName == connected}
			if h.Paired {
				p.Servers = h.Servers
			}
			mu.Lock()
			d.Peers = append(d.Peers, p)
			mu.Unlock()
		}(n)
	}
	wg.Wait()
	sort.Slice(d.Peers, func(i, j int) bool {
		a, b := strings.ToLower(d.Peers[i].Name), strings.ToLower(d.Peers[j].Name)
		return a < b || a == b && d.Peers[i].DNSName < d.Peers[j].DNSName
	})
	return d
}

// resolve finds the owned device called target: its DNS name, or a host
// name or label that only one device has.
func (m *Manager) resolve(ctx context.Context, target string) (*tailnet.Status, tailnet.Node, int, error) {
	name, portText, _ := strings.Cut(strings.TrimSpace(target), ":")
	port := 0
	if portText != "" {
		p, err := strconv.Atoi(portText)
		if err != nil || p <= 0 || p > 65535 {
			return nil, tailnet.Node{}, 0, &Error{Status: http.StatusBadRequest, Msg: "invalid port " + portText}
		}
		port = p
	}
	st, err := m.ts.Fresh(ctx)
	if err != nil {
		return nil, tailnet.Node{}, 0, &Error{Status: http.StatusServiceUnavailable, Msg: err.Error()}
	}
	name = strings.TrimSuffix(name, ".")
	var exact, loose []tailnet.Node
	for _, n := range append([]tailnet.Node{st.Self}, st.Peers...) {
		switch {
		case strings.EqualFold(n.DNSName, name):
			exact = append(exact, n)
		case strings.EqualFold(n.Name, name) || strings.EqualFold(n.Label, name):
			loose = append(loose, n)
		}
	}
	matches := exact
	if len(matches) == 0 {
		matches = loose
	}
	switch {
	case len(matches) == 0:
		return nil, tailnet.Node{}, 0, &Error{Status: http.StatusNotFound, Msg: "no device called " + name + " on the tailnet"}
	case len(matches) > 1:
		return nil, tailnet.Node{}, 0, &Error{Status: http.StatusConflict, Msg: "several devices are called " + name + "; use the full name, e.g. " + matches[0].DNSName}
	}
	n := matches[0]
	switch {
	case n.DNSName == st.Self.DNSName:
		return nil, tailnet.Node{}, 0, &Error{Status: http.StatusBadRequest, Msg: n.Name + " is this computer"}
	case !st.Owned(n):
		return nil, tailnet.Node{}, 0, &Error{Status: http.StatusForbidden, Msg: n.Name + " belongs to another Tailscale user or is tagged"}
	case !n.Online:
		return nil, tailnet.Node{}, 0, &Error{Status: http.StatusBadGateway, Msg: n.Name + " is offline"}
	}
	if port != 0 {
		m.mu.Lock()
		m.known[n.DNSName] = port
		m.mu.Unlock()
	}
	return st, n, port, nil
}

// Pair redeems a pairing code shown on the device called target, keeps the
// token it issues, and connects.
func (m *Manager) Pair(ctx context.Context, target, code string) (Info, error) {
	_, node, _, err := m.resolve(ctx, target)
	if err != nil {
		return Info{}, err
	}
	tr := peerTransport(node)
	defer tr.CloseIdleConnections()
	port, _, _, err := m.findPort(ctx, tr, node, nil)
	if err != nil {
		return Info{}, err
	}
	var cred share.Credential
	body := map[string]string{"code": code, "instance": m.instance()}
	if _, _, err := call(ctx, tr, node, port, nil, "", http.MethodPost, "/api/pair", body, &cred); err != nil {
		return Info{}, err
	}
	if cred.ID == "" || cred.Key == "" {
		return Info{}, &Error{Status: http.StatusBadGateway, Msg: node.Name + " did not issue a pairing"}
	}
	if err := m.tokens.Set(node.DNSName, cred); err != nil {
		return Info{}, err
	}
	log.Printf("remote: paired with %s", node.DNSName)
	return m.Connect(ctx, node.DNSName)
}

// Connect connects to the device called target ("name" or "name:port"),
// replacing any current connection.
func (m *Manager) Connect(ctx context.Context, target string) (Info, error) {
	_, node, _, err := m.resolve(ctx, target)
	if err != nil {
		return Info{}, err
	}
	cred, ok := m.tokens.Get(node.DNSName)
	if !ok {
		return Info{}, &Error{Status: http.StatusUnauthorized, Code: CodePairingRequired, Msg: "pair with " + node.Name + " first"}
	}
	tr := peerTransport(node)
	// A 401 (pairing removed) is answered with pairing_required; the stored
	// credential is kept, since the reply cannot prove where it came from, and
	// pairing again replaces it.
	port, _, epoch, err := m.findPort(ctx, tr, node, &cred)
	if err != nil {
		tr.CloseIdleConnections()
		return Info{}, err
	}
	var rs remoteState
	data, _, err := call(ctx, tr, node, port, &cred, epoch, http.MethodGet, "/api/state", nil, &rs)
	if err != nil {
		tr.CloseIdleConnections()
		return Info{}, err
	}
	c := newConn(m, node, port, cred, tr)
	c.epoch = epoch
	m.mu.Lock()
	old := m.c
	m.c = c
	m.mu.Unlock()
	if old != nil {
		old.close()
	}
	c.apply(data, &rs)
	go c.run()
	m.notify(true)
	log.Printf("remote: connected to %s", node.DNSName)
	m.mu.Lock()
	info := c.infoLocked()
	m.mu.Unlock()
	return info, nil
}
