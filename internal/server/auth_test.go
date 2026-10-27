package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/padmk/portside/internal/ports"
	"github.com/padmk/portside/internal/signing"
)

const testDashboardHost = "d-0123456789abcdef01234567.localhost"

func authServer(t *testing.T) *Server {
	t.Helper()
	token := signing.RandomHex(32)
	s := &Server{
		opt:           Options{Token: token, DashboardHost: testDashboardHost, Port: 7999, StateDir: t.TempDir()},
		sessionCookie: sessionValue(token, testDashboardHost),
		epoch:         signing.NewEpoch(),
		connOwner:     ownedBy(os.Getuid(), false),
	}
	s.routes()
	return s
}

// ownedBy is a connection owner lookup that finds every connection held by
// uid, by the daemon itself when self is true.
func ownedBy(uid int, self bool) connOwnerFunc {
	return func(context.Context, netip.AddrPort, netip.AddrPort) (ports.ConnOwner, error) {
		return ports.ConnOwner{UID: uid, Self: self}, nil
	}
}

func TestOriginAllowedSameOriginOnForwardedPort(t *testing.T) {
	s := authServer(t)
	for _, c := range []struct {
		host, origin string
		want         bool
	}{
		{testDashboardHost + ":7999", "http://" + testDashboardHost + ":7999", true},
		// Through ssh -L 17878:127.0.0.1:7999 the page and its requests use 17878.
		{testDashboardHost + ":17878", "http://" + testDashboardHost + ":17878", true},
		{testDashboardHost + ":17878", "http://" + testDashboardHost + ":3000", false},
		{testDashboardHost + ":17878", "http://evil.example:17878", false},
		{"evil.example:17878", "http://evil.example:17878", false},
		{testDashboardHost + ":17878", "https://" + testDashboardHost + ":17878", false},
		// Not a serialized origin: a path, a user or a query.
		{testDashboardHost + ":7999", "http://" + testDashboardHost + ":7999/path", false},
		{testDashboardHost + ":7999", "http://x@" + testDashboardHost + ":7999", false},
		{testDashboardHost + ":7999", "http://" + testDashboardHost + ":7999?x", false},
	} {
		r := httptest.NewRequest(http.MethodPost, "http://"+c.host+"/api/servers/web/start", nil)
		r.Header.Set("Origin", c.origin)
		if got := s.originAllowed(r); got != c.want {
			t.Errorf("Host %s, Origin %s: allowed = %v, want %v", c.host, c.origin, got, c.want)
		}
	}
}

func TestSessionCookieBoundToDashboardHost(t *testing.T) {
	s := authServer(t)
	if s.sessionCookie == s.opt.Token || strings.Contains(s.sessionCookie, s.opt.Token) {
		t.Fatal("the session cookie holds the token")
	}
	withCookie := func(host, value string) *http.Request {
		r := overLoopback(httptest.NewRequest(http.MethodGet, "http://"+host+"/api/state", nil))
		r.AddCookie(&http.Cookie{Name: cookieName, Value: value})
		return r
	}
	if err := s.authenticate(withCookie(testDashboardHost+":7999", s.sessionCookie)); err != nil {
		t.Errorf("session cookie on the dashboard host refused: %v", err)
	}
	for _, host := range []string{"127.0.0.1:7999", "localhost:7999", "web.localhost:7999"} {
		if s.authenticate(withCookie(host, s.sessionCookie)) == nil {
			t.Errorf("session cookie accepted on %s", host)
		}
	}
	if s.authenticate(withCookie(testDashboardHost+":7999", s.opt.Token)) == nil {
		t.Error("the token accepted as a session cookie")
	}
	// After a cold start the label differs; the cookie of the old one is
	// no session there, though the token is the same.
	const next = "d-fedcba9876543210fedcba98.localhost"
	restarted := authServer(t)
	restarted.opt.Token, restarted.opt.DashboardHost = s.opt.Token, next
	restarted.sessionCookie = sessionValue(s.opt.Token, next)
	if restarted.sessionCookie == s.sessionCookie {
		t.Fatal("two labels share a session value")
	}
	if restarted.authenticate(withCookie(next+":7999", s.sessionCookie)) == nil {
		t.Error("the cookie of an old label accepted on the new one")
	}
}

func TestDashboardHostRotatesOnColdStart(t *testing.T) {
	state := t.TempDir()
	first, err := LoadCredentials(state, "")
	if err != nil {
		t.Fatal(err)
	}
	second, err := LoadCredentials(state, "")
	if err != nil {
		t.Fatal(err)
	}
	if second.Host == first.Host || !IsDashboardHost(second.Host) {
		t.Errorf("cold start: host %q after %q, want a new dashboard host", second.Host, first.Host)
	}
	if second.Token != first.Token {
		t.Error("the token changed on a restart")
	}
	// An upgrade hands the host over.
	upgraded, err := LoadCredentials(state, second.Host)
	if err != nil || upgraded.Host != second.Host || upgraded.Token != first.Token {
		t.Errorf("upgrade: %+v, %v; want host %q and the same token", upgraded, err, second.Host)
	}
	if b, err := os.ReadFile(DashboardHostPath(state)); err != nil || strings.TrimSpace(string(b)) != second.Host {
		t.Errorf("dashboard-host file %q, %v", b, err)
	}
	if st, err := os.Stat(DashboardHostPath(state)); err != nil || st.Mode().Perm() != 0o600 {
		t.Errorf("dashboard-host mode: %v, %v", st, err)
	}
	// Anything else handed over is no dashboard host.
	if c, _ := LoadCredentials(state, "web.localhost"); c.Host == "web.localhost" || !IsDashboardHost(c.Host) {
		t.Errorf("handed-over host %q kept", c.Host)
	}
}

func TestOldDashboardHostGetsHint(t *testing.T) {
	s := authServer(t)
	const old = "d-fedcba9876543210fedcba98.localhost:7999"
	for _, path := range []string{"/", "/auth?code=x", "/api/state", "/api/session"} {
		r := httptest.NewRequest(http.MethodGet, "http://"+old+path, nil)
		r.AddCookie(&http.Cookie{Name: cookieName, Value: s.sessionCookie})
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		res := w.Result()
		body, _ := io.ReadAll(res.Body)
		if strings.Contains(string(body), testDashboardHost) || strings.Contains(res.Header.Get("Location"), testDashboardHost) {
			t.Errorf("%s: the reply reveals the dashboard host", path)
		}
		if res.Header.Get(signInHeader) == "" {
			t.Errorf("%s: no sign-in marker", path)
		}
		var expired bool
		for _, c := range res.Cookies() {
			expired = expired || (c.Name == cookieName && c.MaxAge < 0)
		}
		if !expired {
			t.Errorf("%s: the old cookie is kept", path)
		}
		switch path {
		case "/api/session":
			if !strings.Contains(string(body), `"authenticated":false`) {
				t.Errorf("session probe: %s", body)
			}
		default:
			if res.StatusCode != http.StatusUnauthorized {
				t.Errorf("%s: status %d, want 401", path, res.StatusCode)
			}
		}
		if path == "/" && !strings.Contains(string(body), " open") {
			t.Errorf("hint page: %s", body)
		}
	}
}

func signedRequest(s *Server, epoch string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/api/servers/web/start", nil)
	signing.Sign(r.Header, s.opt.Token, r.Method, r.RequestURI, nil, epoch)
	return r
}

func TestSignedRequestAcceptedOnce(t *testing.T) {
	s := authServer(t)
	r := signedRequest(s, s.epoch)
	if err := s.authenticate(r); err != nil {
		t.Fatalf("signed request refused: %v", err)
	}
	replay := httptest.NewRequest(r.Method, r.RequestURI, nil)
	replay.Header = r.Header.Clone()
	if err := s.authenticate(replay); !errors.Is(err, signing.ErrReplay) {
		t.Errorf("replay: %v, want ErrReplay", err)
	}
}

func TestSignatureForAnotherEpochRefused(t *testing.T) {
	s := authServer(t)
	// A request captured while another program held the port, or signed for
	// the daemon before a restart, names another epoch.
	if err := s.authenticate(signedRequest(s, signing.NewEpoch())); !errors.Is(err, signing.ErrEpoch) {
		t.Errorf("another epoch: %v, want ErrEpoch", err)
	}
	if err := s.authenticate(signedRequest(s, "")); !errors.Is(err, signing.ErrEpoch) {
		t.Errorf("no epoch: %v, want ErrEpoch", err)
	}
	// The same request in a new daemon life: its nonce is new there, its
	// epoch is not.
	r := signedRequest(s, s.epoch)
	restarted := authServer(t)
	restarted.opt.Token, restarted.sessionCookie = s.opt.Token, s.sessionCookie
	if err := restarted.authenticate(r); !errors.Is(err, signing.ErrEpoch) {
		t.Errorf("replay after a restart: %v, want ErrEpoch", err)
	}
}

func TestReplyProofCoversBody(t *testing.T) {
	s := authServer(t)
	r := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:7999/api/health", nil)
	challenge := signing.Challenge(r.Header)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	res := w.Result()
	body, _ := io.ReadAll(res.Body)
	if _, ok := signing.ProvenReply(s.opt.Token, challenge, res.Header, res.StatusCode, body); !ok {
		t.Fatalf("reply not proved: %d %s", res.StatusCode, body)
	}
	altered := strings.Replace(string(body), `"pid":`, `"pid":1`, 1)
	if _, ok := signing.ProvenReply(s.opt.Token, challenge, res.Header, res.StatusCode, []byte(altered)); ok {
		t.Error("the proof holds for an altered body")
	}
	if _, ok := signing.ProvenReply(s.opt.Token, challenge, res.Header, http.StatusTeapot, body); ok {
		t.Error("the proof holds for another status")
	}
	// The short proof of a WebSocket upgrade is no proof of a reply.
	if _, ok := signing.Proven(s.opt.Token, challenge, res.Header); ok {
		t.Error("a reply proof passes as an upgrade proof")
	}
}

// overLoopback gives r the addresses of a loopback connection.
func overLoopback(r *http.Request) *http.Request {
	r.RemoteAddr = "127.0.0.1:52000"
	local := &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 7999}
	return r.WithContext(context.WithValue(r.Context(), http.LocalAddrContextKey, net.Addr(local)))
}

// signInRequest is a browser's request for link over a loopback connection.
func signInRequest(link string) *http.Request {
	return overLoopback(httptest.NewRequest(http.MethodGet, link, nil))
}

// newSignInLink returns a sign-in link as a client gets it, which is on
// 127.0.0.1 and does not name the dashboard host.
func newSignInLink(t *testing.T, s *Server) string {
	t.Helper()
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/auth/code", strings.NewReader(`{"next":"/"}`))
	req.ContentLength = int64(len(`{"next":"/"}`))
	s.authCode(w, req)
	var link struct{ URL, Dashboard string }
	if err := json.NewDecoder(w.Body).Decode(&link); err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(link.URL)
	if err != nil || u.Host != "127.0.0.1:7999" || u.Path != "/auth" || u.Query().Get("code") == "" {
		t.Fatalf("sign-in URL %q", link.URL)
	}
	if label, _, _ := strings.Cut(testDashboardHost, "."); strings.Contains(link.URL, label) {
		t.Fatalf("sign-in URL %q names the dashboard host", link.URL)
	}
	if link.Dashboard != "http://"+testDashboardHost+":7999" {
		t.Fatalf("dashboard %q", link.Dashboard)
	}
	return link.URL
}

// onDashboard is link as the daemon forwards it to the dashboard host.
func onDashboard(link string) string {
	return strings.Replace(link, "//127.0.0.1:7999/", "//"+testDashboardHost+":7999/", 1)
}

func TestSignInLinkForwardedToDashboardHost(t *testing.T) {
	s := authServer(t)
	link := newSignInLink(t, s)
	w := httptest.NewRecorder()
	s.signIn(w, signInRequest(link))
	res := w.Result()
	if res.StatusCode != http.StatusFound || res.Header.Get("Location") != onDashboard(link) || len(res.Cookies()) != 0 {
		t.Fatalf("forward: %d to %q, cookies %v", res.StatusCode, res.Header.Get("Location"), res.Cookies())
	}
	// Forwarding left the code for the dashboard host.
	if !s.codes.valid(mustCode(t, link)) {
		t.Error("forwarding used the code up")
	}
}

func TestSignInLinkForwardedOnlyForOwnAccount(t *testing.T) {
	s := authServer(t)
	link := newSignInLink(t, s)
	for name, lookup := range map[string]connOwnerFunc{
		"another account":   ownedBy(os.Getuid()+1, false),
		"the daemon itself": ownedBy(os.Getuid(), true),
		"an owner not found": func(context.Context, netip.AddrPort, netip.AddrPort) (ports.ConnOwner, error) {
			return ports.ConnOwner{}, ports.ErrNoOwner
		},
	} {
		s.connOwner = lookup
		w := httptest.NewRecorder()
		s.signIn(w, signInRequest(link))
		res := w.Result()
		body, _ := io.ReadAll(res.Body)
		label, _, _ := strings.Cut(testDashboardHost, ".")
		if res.StatusCode != http.StatusForbidden || res.Header.Get("Location") != "" || len(res.Cookies()) != 0 {
			t.Errorf("%s: %d to %q, cookies %v; want 403", name, res.StatusCode, res.Header.Get("Location"), res.Cookies())
		}
		if strings.Contains(string(body), label) {
			t.Errorf("%s: the refusal names the dashboard host", name)
		}
	}
	if !s.codes.valid(mustCode(t, link)) {
		t.Error("a refused forward used the code up")
	}
}

func mustCode(t *testing.T, link string) string {
	t.Helper()
	u, err := url.Parse(link)
	if err != nil {
		t.Fatal(err)
	}
	return u.Query().Get("code")
}

func TestSignInCodeWorksOnce(t *testing.T) {
	s := authServer(t)
	link := onDashboard(newSignInLink(t, s))
	signIn := func() *http.Response {
		w := httptest.NewRecorder()
		s.signIn(w, signInRequest(link))
		return w.Result()
	}
	first := signIn()
	if first.StatusCode != http.StatusFound || len(first.Cookies()) != 1 || first.Cookies()[0].Value != s.sessionCookie {
		t.Fatalf("first use: %d, cookies %v", first.StatusCode, first.Cookies())
	}
	second := signIn()
	if second.StatusCode != http.StatusUnauthorized || len(second.Cookies()) != 0 {
		t.Errorf("second use: %d, cookies %v", second.StatusCode, second.Cookies())
	}
}

func TestSignInCodeOnlyForOwnAccount(t *testing.T) {
	s := authServer(t)
	link := onDashboard(newSignInLink(t, s))
	try := func(lookup connOwnerFunc, r *http.Request) *http.Response {
		s.connOwner = lookup
		w := httptest.NewRecorder()
		s.signIn(w, r)
		return w.Result()
	}
	refused := map[string]struct {
		lookup connOwnerFunc
		req    *http.Request
	}{
		"another account":   {ownedBy(os.Getuid()+1, false), signInRequest(link)},
		"the daemon itself": {ownedBy(os.Getuid(), true), signInRequest(link)},
		"an owner not found": {func(context.Context, netip.AddrPort, netip.AddrPort) (ports.ConnOwner, error) {
			return ports.ConnOwner{}, ports.ErrNoOwner
		}, signInRequest(link)},
		"no connection addresses": {ownedBy(os.Getuid(), false), httptest.NewRequest(http.MethodGet, link, nil)},
	}
	for name, c := range refused {
		if res := try(c.lookup, c.req); res.StatusCode != http.StatusForbidden || len(res.Cookies()) != 0 {
			t.Errorf("%s: %d, cookies %v; want 403 without a session", name, res.StatusCode, res.Cookies())
		}
	}
	// None of those used the code up.
	if res := try(ownedBy(os.Getuid(), false), signInRequest(link)); res.StatusCode != http.StatusFound || len(res.Cookies()) != 1 {
		t.Errorf("own account: %d, cookies %v", res.StatusCode, res.Cookies())
	}
}

func TestSessionCookieOnlyFromOwnAccount(t *testing.T) {
	s := authServer(t)
	var lookups atomic.Int32
	uid := os.Getuid()
	s.connOwner = func(context.Context, netip.AddrPort, netip.AddrPort) (ports.ConnOwner, error) {
		lookups.Add(1)
		return ports.ConnOwner{UID: uid}, nil
	}
	srv := httptest.NewUnstartedServer(s.Handler())
	srv.Config.ConnContext = ConnContext
	srv.Start()
	defer srv.Close()
	client := srv.Client()
	session := func() bool {
		t.Helper()
		req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/session", nil)
		req.Host = testDashboardHost + ":7999"
		req.AddCookie(&http.Cookie{Name: cookieName, Value: s.sessionCookie})
		res, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		var v struct{ Authenticated bool }
		if err := json.NewDecoder(res.Body).Decode(&v); err != nil {
			t.Fatal(err)
		}
		return v.Authenticated
	}
	for i := range 3 {
		if !session() {
			t.Fatalf("request %d: the owner's cookie refused", i)
		}
	}
	if n := lookups.Load(); n != 1 {
		t.Errorf("%d owner lookups for one keep-alive connection, want 1", n)
	}
	// A connection of another account: the same cookie is no session, on
	// the API or a WebSocket.
	client.CloseIdleConnections()
	uid = os.Getuid() + 1
	if session() {
		t.Error("the cookie accepted from another account")
	}
	if n := lookups.Load(); n != 2 {
		t.Errorf("%d owner lookups after a new connection, want 2", n)
	}
	for _, path := range []string{"/api/state", "/api/events"} {
		req, _ := http.NewRequest(http.MethodGet, srv.URL+path, nil)
		req.Host = testDashboardHost + ":7999"
		req.AddCookie(&http.Cookie{Name: cookieName, Value: s.sessionCookie})
		if path == "/api/events" {
			req.Header.Set("Connection", "Upgrade")
			req.Header.Set("Upgrade", "websocket")
			req.Header.Set("Sec-WebSocket-Version", "13")
			req.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")
		}
		res, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s from another account: %d, want 401", path, res.StatusCode)
		}
	}
	// A lookup that fails is refused, and tried again on the next request.
	client.CloseIdleConnections()
	before := lookups.Load()
	s.connOwner = func(context.Context, netip.AddrPort, netip.AddrPort) (ports.ConnOwner, error) {
		lookups.Add(1)
		return ports.ConnOwner{}, ports.ErrNoOwner
	}
	if first, second := session(), session(); first || second {
		t.Error("the cookie accepted when the owner is unknown")
	}
	if n := lookups.Load() - before; n != 2 {
		t.Errorf("%d lookups for two requests while the owner is unknown, want 2", n)
	}
}

func TestSafeNext(t *testing.T) {
	for _, next := range []string{"/", "/#/web", "/#/web%20app", "/?x=1#/a"} {
		if !safeNext(next) {
			t.Errorf("safeNext(%q) = false", next)
		}
	}
	for _, next := range []string{"", "web", "//example.com", "/\\example.com", "\\/example.com", "/\t/example.com",
		"/\n/example.com", "/\r/x", "/\x00", "/\x7f", "/\u0085x", "/%", "http://example.com/", "/x\\y"} {
		if safeNext(next) {
			t.Errorf("safeNext(%q) = true", next)
		}
	}
}
