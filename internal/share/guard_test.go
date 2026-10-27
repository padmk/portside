package share

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/padmk/portside/internal/app"
	"github.com/padmk/portside/internal/tailnet"
)

const (
	selfIP   = "100.0.0.1"
	mbpIP    = "100.0.0.2"
	taggedIP = "100.0.0.3"
	friendIP = "100.0.0.4"
	owner    = "me@example.com"
	host     = "devbox.t.ts.net:7878"
	secret   = "s3cret"
	epoch    = "0123456789abcdef0123456789abcdef"
)

func testStatus() *tailnet.Status {
	node := func(name, ip string, user int64, tagged bool) tailnet.Node {
		return tailnet.Node{Name: name, Label: strings.ToLower(name), DNSName: strings.ToLower(name) + ".t.ts.net",
			IPs: []string{ip}, Online: true, UserID: user, Tagged: tagged}
	}
	return &tailnet.Status{
		Self:  node("devbox", selfIP, 1, false),
		Owner: owner,
		Peers: []tailnet.Node{node("MBP", mbpIP, 1, false), node("build", taggedIP, 1, true), node("friend", friendIP, 2, false)},
	}
}

// guardFixture is a manager with a running listener's secret and epoch, one
// paired device (MBP) and a handler that records what reached it.
type guardFixture struct {
	m       *Manager
	cred    Credential
	reached *Peer
}

func newGuard(t *testing.T) *guardFixture {
	t.Helper()
	st := testStatus()
	f := &guardFixture{m: New(7878, t.TempDir(), tailnet.Preloaded(st))}
	f.m.secret, f.m.epoch = secret, epoch
	f.m.SetHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, _ := PeerFrom(r.Context())
		f.reached = &p
		w.WriteHeader(http.StatusOK)
	}))
	c, err := f.m.devs.NewCode()
	if err != nil {
		t.Fatal(err)
	}
	f.cred, err = f.m.Pair(Peer{Node: st.Peers[0]}, c.Code, "")
	if err != nil {
		t.Fatal(err)
	}
	return f
}

type reqOpt func(r *http.Request)

func from(ip string) reqOpt { return func(r *http.Request) { r.Header.Set("X-Forwarded-For", ip) } }
func login(l string) reqOpt { return func(r *http.Request) { r.Header.Set("Tailscale-User-Login", l) } }
func forwardedHost(h string) reqOpt {
	return func(r *http.Request) { r.Header.Set("X-Forwarded-Host", h) }
}
func without(name string) reqOpt { return func(r *http.Request) { r.Header.Del(name) } }
func signed(c Credential, e string) reqOpt {
	return func(r *http.Request) {
		uri := strings.TrimPrefix(r.RequestURI, "/"+secret)
		c.Sign(r.Header, r.Method, uri, nil, e)
	}
}

// request builds what Tailscale Serve hands the listener: the secret path in
// front, the peer header, and Serve's own identity and forwarding headers.
func request(path string, opts ...reqOpt) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/"+secret+path, nil)
	r.Host = "127.0.0.1:5555"
	r.Header.Set(app.PeerHeader, "1")
	from(mbpIP)(r)
	login(owner)(r)
	forwardedHost(host)(r)
	for _, o := range opts {
		o(r)
	}
	return r
}

func TestGuard(t *testing.T) {
	f := newGuard(t)
	wrongSecret := httptest.NewRequest(http.MethodGet, "/nope/api/state", nil)
	wrongSecret.Header = request("/api/state", signed(f.cred, epoch)).Header
	cases := []struct {
		name   string
		req    *http.Request
		status int
		paired bool // the handler saw a paired device
	}{
		{"signed request from the paired device", request("/api/state", signed(f.cred, epoch)), 200, true},
		{"no peer header (a web page)", request("/api/state", signed(f.cred, epoch), without(app.PeerHeader)), 403, false},
		{"wrong secret path", wrongSecret, 403, false},
		{"rebound host name", request("/api/state", signed(f.cred, epoch), forwardedHost("evil.example:7878")), 403, false},
		{"Host rewritten for the backend", func() *http.Request {
			r := request("/api/state", signed(f.cred, epoch))
			r.Host = "localhost"
			return r
		}(), 200, true},
		{"another Tailscale user", request("/api/state", signed(f.cred, epoch), login("friend@example.com"), from(friendIP)), 403, false},
		{"tagged device of the owner", request("/api/state", signed(f.cred, epoch), from(taggedIP)), 403, false},
		{"from this computer itself", request("/api/state", signed(f.cred, epoch), from(selfIP)), 403, false},
		{"unknown address", request("/api/state", signed(f.cred, epoch), from("100.9.9.9")), 403, false},
		{"forged first X-Forwarded-For entry", request("/api/state", signed(f.cred, epoch), from(mbpIP+", "+selfIP)), 403, false},
		{"unsigned", request("/api/state"), 401, false},
		{"signed for another listener", request("/api/state", signed(f.cred, "epoch0")), 403, false},
		{"hello without pairing", request(HelloPath), 200, false},
		{"hello with an old epoch still proves", request(HelloPath, signed(f.cred, "epoch0")), 200, true},
		{"pairing without signature", request(PairPath), 200, false},
		{"key of another device", request("/api/state", signed(Credential{ID: f.cred.ID, Key: strings.Repeat("cd", 32)}, epoch)), 403, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f.reached = nil
			w := httptest.NewRecorder()
			f.m.guard().ServeHTTP(w, c.req)
			if w.Code != c.status {
				t.Fatalf("status %d (%s), want %d", w.Code, strings.TrimSpace(w.Body.String()), c.status)
			}
			if c.status != 200 {
				if f.reached != nil {
					t.Error("a refused request reached the handler")
				}
				return
			}
			if f.reached == nil {
				t.Fatal("handler not reached")
			}
			if got := f.reached.Device != nil; got != c.paired {
				t.Errorf("paired device = %v, want %v", got, c.paired)
			}
		})
	}
}

func TestGuardProvesItself(t *testing.T) {
	f := newGuard(t)
	r := request(HelloPath, signed(f.cred, ""))
	ch := Challenge(r.Header)
	w := httptest.NewRecorder()
	f.m.guard().ServeHTTP(w, r)
	got, ok := f.cred.Proven(ch, w.Header())
	if !ok || got != epoch {
		t.Errorf("Proven = %q, %v; want the listener's epoch", got, ok)
	}
}

func TestGuardBindsPairingToDevice(t *testing.T) {
	f := newGuard(t)
	// MBP's credential presented by another device of the same owner.
	st := testStatus()
	st.Peers = append(st.Peers, tailnet.Node{Name: "ipad", DNSName: "ipad.t.ts.net", IPs: []string{"100.0.0.5"}, Online: true, UserID: 1})
	f.m.ts = tailnet.Preloaded(st)
	w := httptest.NewRecorder()
	f.m.guard().ServeHTTP(w, request("/api/state", signed(f.cred, epoch), from("100.0.0.5")))
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status %d, want 401", w.Code)
	}
}

func TestRevokeEndsRequestsInFlight(t *testing.T) {
	f := newGuard(t)
	started := make(chan struct{})
	ended := make(chan struct{})
	f.m.SetHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
		close(ended)
	}))
	go f.m.guard().ServeHTTP(httptest.NewRecorder(), request("/api/events", signed(f.cred, epoch)))
	<-started
	if ok, err := f.m.Revoke(f.cred.ID); !ok || err != nil {
		t.Fatalf("Revoke: %v, %v", ok, err)
	}
	select {
	case <-ended:
	case <-time.After(2 * time.Second):
		t.Fatal("the revoked device's request kept running")
	}
	w := httptest.NewRecorder()
	f.m.guard().ServeHTTP(w, request("/api/state", signed(f.cred, epoch)))
	if w.Code != http.StatusUnauthorized {
		t.Errorf("after revoke: status %d, want 401", w.Code)
	}
}

func TestClosedManagerStaysClosed(t *testing.T) {
	m := New(7878, t.TempDir(), tailnet.Preloaded(testStatus()))
	m.Set(true)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	m.Close(ctx)
	if st := m.Apply(ctx); st.Active {
		t.Error("Apply after Close made sharing active")
	}
	if _, err := m.listen(); err == nil {
		t.Error("listen after Close started a listener")
	}
}

// stalledBody is a request body that tells when it is first read and
// delivers its bytes only once released.
type stalledBody struct {
	reading chan struct{}
	release chan struct{}
	once    bool
	r       io.Reader
}

func (b *stalledBody) Read(p []byte) (int, error) {
	if !b.once {
		b.once = true
		close(b.reading)
		<-b.release
	}
	return b.r.Read(p)
}

func TestRevokeWhileBodyPendingRefusesRequest(t *testing.T) {
	f := newGuard(t)
	body := []byte(`{"wait":true}`)
	path := "/api/servers/victim/start"
	r := httptest.NewRequest(http.MethodPost, "/"+secret+path, nil)
	r.Header = request(path).Header
	r.Host = "127.0.0.1:5555"
	f.cred.Sign(r.Header, http.MethodPost, path, body, epoch)
	sb := &stalledBody{reading: make(chan struct{}), release: make(chan struct{}), r: bytes.NewReader(body)}
	r.Body = io.NopCloser(sb)
	w := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		f.m.guard().ServeHTTP(w, r)
		close(done)
	}()
	<-sb.reading
	if ok, err := f.m.Revoke(f.cred.ID); !ok || err != nil {
		t.Fatalf("Revoke: %v, %v", ok, err)
	}
	close(sb.release)
	<-done
	if f.reached != nil {
		t.Error("a request of a device revoked while its body was arriving reached the handler")
	}
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status %d, want 401", w.Code)
	}
	if len(f.m.live) != 0 {
		t.Error("the revoked device has a live connection record")
	}
}

func TestAttachRefusesRevokedOrReplacedPairing(t *testing.T) {
	f := newGuard(t)
	dev, ok := f.m.devs.Lookup(f.cred.ID)
	if !ok {
		t.Fatal("device not paired")
	}
	replaced := dev
	replaced.Key = strings.Repeat("cd", 32)
	if _, _, ok := f.m.attach(replaced, true); ok {
		t.Error("attach accepted a key the device no longer has")
	}
	if ok, err := f.m.Revoke(f.cred.ID); !ok || err != nil {
		t.Fatalf("Revoke: %v, %v", ok, err)
	}
	if _, _, ok := f.m.attach(dev, true); ok {
		t.Error("attach accepted a revoked device")
	}
	if len(f.m.live) != 0 {
		t.Error("attach after revoke left a connection context nobody cancels")
	}
}

func TestPairingAgainEndsOldConnections(t *testing.T) {
	f := newGuard(t)
	started := make(chan struct{})
	ended := make(chan struct{})
	f.m.SetHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
		close(ended)
	}))
	go f.m.guard().ServeHTTP(httptest.NewRecorder(), request("/api/events", signed(f.cred, epoch)))
	<-started
	c, err := f.m.devs.NewCode()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.m.Pair(Peer{Node: testStatus().Peers[0]}, c.Code, ""); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ended:
	case <-time.After(2 * time.Second):
		t.Fatal("the replaced pairing's request kept running")
	}
}

func TestGuardBoundsBodyReading(t *testing.T) {
	f := newGuard(t)
	old := bodyTimeout
	bodyTimeout = 200 * time.Millisecond
	t.Cleanup(func() { bodyTimeout = old })
	srv := httptest.NewServer(f.m.guard())
	defer srv.Close()
	conn, err := net.Dial("tcp", srv.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	path := "/api/servers/victim/start"
	h := request(path).Header
	f.cred.Sign(h, http.MethodPost, path, []byte("0123456789"), epoch)
	var sb strings.Builder
	fmt.Fprintf(&sb, "POST /%s%s HTTP/1.1\r\nHost: 127.0.0.1\r\nContent-Length: 10\r\n", secret, path)
	for k, vs := range h {
		for _, v := range vs {
			fmt.Fprintf(&sb, "%s: %s\r\n", k, v)
		}
	}
	sb.WriteString("\r\n01234")
	if _, err := io.WriteString(conn, sb.String()); err != nil {
		t.Fatal(err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	res, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatalf("no reply while the body was withheld: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusForbidden || f.reached != nil {
		t.Errorf("status %d, reached %v; want 403 and the handler not reached", res.StatusCode, f.reached != nil)
	}
}
