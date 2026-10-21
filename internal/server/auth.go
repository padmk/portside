package server

import (
	"bytes"
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/padmk/portside/internal/app"
	"github.com/padmk/portside/internal/config"
	"github.com/padmk/portside/internal/paths"
	"github.com/padmk/portside/internal/ports"
	"github.com/padmk/portside/internal/signing"
)

// The daemon runs commands as its user, so only that user may drive it. A
// random token in a 0600 file proves it, without ever being sent: the CLI,
// MCP server and macOS app sign each request with it (package signing), and
// the daemon checks the signature, its time, that its nonce is new and that
// it names this daemon's epoch, which is random per start. A client first
// asks GET /api/health with a fresh challenge and checks the daemon's proof
// of the token before it signs anything, so a program holding the port
// while the daemon is down gets no signature; one captured anyway is
// refused by the next daemon, whose epoch differs. Authenticated replies
// carry a proof of their status and body too. A plain
// `Authorization: Bearer <token>` header is accepted as well, for manual use
// with curl; no shipped client sends one.
//
// A browser gets a session cookie after opening a single-use link that one
// of those clients created. The link is on http://127.0.0.1:<port>, so
// process arguments, which other accounts can read, never show the
// dashboard's host. The daemon forwards the link to that host, and redeems
// it there, only on a connection from a process of its own account, so a
// link seen in process arguments is no use to other accounts. The cookie
// lives on the dashboard's own host, http://<label>.localhost:<port>, with
// a random label that is new on every cold start of the daemon (an
// in-place upgrade keeps it) and kept in another 0600 file for local
// clients. It too is accepted only on connections from the daemon's
// account, so a program of another account that got hold of it (by
// serving the dashboard host on a port of its own, where browsers send it
// as well) cannot use it. Browsers send
// cookies to every port of a host, so a cookie on 127.0.0.1 would reach
// every dev server, container or forwarded port opened there; and a page
// another program served on an old label while the port was free, a
// service worker say, has no hold on the new one. The label is never
// revealed to an unauthenticated request; the cookie value is derived from
// the token and the label, not the token itself, and is accepted only on
// requests to that host.

const (
	codeTTL = 2 * time.Minute
)

var cookieName = app.ID + "_session"

// signInHeader marks a reply that says the browser needs a new sign-in
// link, so the app's dashboard window can get one.
var signInHeader = signing.Header("Sign-In")

// TokenPath is where the daemon keeps its token.
func TokenPath(stateDir string) string { return filepath.Join(stateDir, paths.TokenName) }

// DashboardHostPath is where the daemon keeps its dashboard's host name.
func DashboardHostPath(stateDir string) string {
	return filepath.Join(stateDir, paths.DashboardHostName)
}

// Credentials are the daemon's API token and its dashboard's host name.
type Credentials struct {
	Token string
	Host  string // <label>.localhost
}

// IsDashboardHost reports whether host has the form <label>.localhost of a
// dashboard host.
func IsDashboardHost(host string) bool {
	label, ok := strings.CutSuffix(host, ".localhost")
	return ok && config.IsDashboardLabel(label)
}

// LoadCredentials returns the token, creating it with private permissions,
// and a dashboard host: keepHost when it is one (an upgrade carries it
// over), else a new one, written for local clients. A state directory
// without a dashboard host predates host-bound sessions, when browsers kept
// the token itself as a cookie for 127.0.0.1; the token is replaced then,
// so such cookies stop working.
func LoadCredentials(stateDir, keepHost string) (Credentials, error) {
	var c Credentials
	host, hostOK := readPrivate(DashboardHostPath(stateDir))
	if t, ok := readPrivate(TokenPath(stateDir)); ok && signing.ValidKey(t) && hostOK && IsDashboardHost(host) {
		c.Token = t
	}
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return c, err
	}
	if c.Token == "" {
		c.Token = signing.RandomHex(32)
		if err := writePrivate(TokenPath(stateDir), c.Token); err != nil {
			return c, err
		}
	}
	c.Host = keepHost
	if !IsDashboardHost(c.Host) {
		c.Host = config.NewDashboardLabel() + ".localhost"
	}
	if err := writePrivate(DashboardHostPath(stateDir), c.Host); err != nil {
		return c, err
	}
	return c, nil
}

// readPrivate reads a one-line file and makes sure only its owner can read it.
func readPrivate(p string) (string, bool) {
	b, err := os.ReadFile(p)
	if err != nil {
		return "", false
	}
	if st, err := os.Stat(p); err == nil && st.Mode().Perm()&0o077 != 0 {
		_ = os.Chmod(p, 0o600)
	}
	return strings.TrimSpace(string(b)), true
}

func writePrivate(p, value string) error {
	return paths.WriteFile(p, []byte(value+"\n"))
}

// sessionValue is the dashboard cookie for a token on a dashboard host:
// bound to both, but not usable as the token.
func sessionValue(token, host string) string { return signing.MAC(token, "dashboard session", host) }

// codes are the sign-in links not yet used. Each works once, within codeTTL.
type codes struct {
	mu sync.Mutex
	m  map[string]time.Time // expiry
}

func (c *codes) issue() string {
	code := signing.RandomHex(16)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.m == nil {
		c.m = map[string]time.Time{}
	}
	now := time.Now()
	for k, exp := range c.m {
		if now.After(exp) {
			delete(c.m, k)
		}
	}
	c.m[code] = now.Add(codeTTL)
	return code
}

// valid reports whether a code could be redeemed now, without using it.
func (c *codes) valid(code string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	exp, ok := c.m[code]
	return ok && time.Now().Before(exp)
}

// redeem uses up a code and reports whether it was valid.
func (c *codes) redeem(code string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	exp, ok := c.m[code]
	delete(c.m, code)
	return ok && time.Now().Before(exp)
}

// connOwnerFunc tells who holds the client end of a connection.
type connOwnerFunc func(ctx context.Context, client, server netip.AddrPort) (ports.ConnOwner, error)

// connCheck is a connection's owner check, made at most once per
// connection: a found owner is kept, a failed lookup is tried again.
type connCheck struct {
	mu    sync.Mutex
	done  bool
	owner bool
}

type connCheckKey struct{}

// ConnContext gives each connection of the daemon's http.Server a place
// for its owner check (http.Server.ConnContext), so a keep-alive
// connection is looked up once rather than on every request.
func ConnContext(ctx context.Context, _ net.Conn) context.Context {
	return context.WithValue(ctx, connCheckKey{}, &connCheck{})
}

// fromOwner reports whether r came over a connection from another process
// of the daemon's own account. Where that cannot be told, it did not.
func (s *Server) fromOwner(r *http.Request) bool {
	c, ok := r.Context().Value(connCheckKey{}).(*connCheck)
	if !ok {
		owner, _ := s.lookupOwner(r)
		return owner
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.done {
		owner, err := s.lookupOwner(r)
		c.owner, c.done = owner, err == nil
		return owner
	}
	return c.owner
}

// lookupOwner finds who holds the client end of r's connection. An error
// means the lookup failed, which may pass; false without one is final.
func (s *Server) lookupOwner(r *http.Request) (bool, error) {
	client, err := netip.ParseAddrPort(r.RemoteAddr)
	if err != nil {
		return false, nil
	}
	la, ok := r.Context().Value(http.LocalAddrContextKey).(net.Addr)
	if !ok {
		return false, nil
	}
	server, err := netip.ParseAddrPort(la.String())
	if err != nil {
		return false, nil
	}
	lookup := s.connOwner
	if lookup == nil {
		lookup = ports.ConnectionOwner
	}
	o, err := lookup(r.Context(), client, server)
	if err != nil {
		return false, err
	}
	return sameAccount(o), nil
}

// sameAccount is the decision of fromOwner: a process of this account that
// is not the daemon itself (which may dial its own port for a tunnel).
func sameAccount(o ports.ConnOwner) bool {
	return o.UID == os.Getuid() && !o.Self
}

// authenticate checks a request's credentials: a signature made with the
// token, the token as a bearer token, or the session cookie on the
// dashboard host over a connection from the daemon's account. A signed request's body is read to be hashed and put
// back for the handler.
func (s *Server) authenticate(r *http.Request) error {
	if s.opt.Token == "" {
		return nil
	}
	if signing.Signed(r.Header) {
		var body []byte
		if r.Body != nil {
			b, err := io.ReadAll(io.LimitReader(r.Body, signing.MaxBody+1))
			if err != nil {
				return err
			}
			if len(b) > signing.MaxBody {
				return errors.New("request body too large")
			}
			body = b
			r.Body = io.NopCloser(bytes.NewReader(body))
		}
		if err := s.signatures.Verify(r, s.opt.Token, r.RequestURI, body); err != nil {
			return fmt.Errorf("request signature: %w", err)
		}
		if err := signing.CheckEpoch(r.Header, s.epoch); err != nil {
			return fmt.Errorf("request signature: %w", err)
		}
		return nil
	}
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		if subtle.ConstantTimeCompare([]byte(strings.TrimPrefix(h, "Bearer ")), []byte(s.opt.Token)) == 1 {
			return nil
		}
		return errors.New("wrong API token")
	}
	if s.onDashboardHost(r) {
		if c, err := r.Cookie(cookieName); err == nil && subtle.ConstantTimeCompare([]byte(c.Value), []byte(s.sessionCookie)) == 1 {
			if s.fromOwner(r) {
				return nil
			}
			return errForeignSession
		}
	}
	return errUnauthorized
}

func (s *Server) authorized(r *http.Request) bool { return s.authenticate(r) == nil }

func (s *Server) onDashboardHost(r *http.Request) bool {
	return s.opt.DashboardHost != "" && hostname(r.Host) == s.opt.DashboardHost
}

// public endpoints work without credentials: identity, the session probe and
// the sign-in redirect. Static dashboard files are public too; they hold no data.
func public(r *http.Request) bool {
	switch r.URL.Path {
	case "/api/health", "/api/session", "/auth":
		return true
	}
	return !strings.HasPrefix(r.URL.Path, "/api/")
}

func (s *Server) session(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]bool{"authenticated": s.authorized(r)})
}

// dashboardOrigin is the dashboard's address at port.
func (s *Server) dashboardOrigin(port int) string {
	return "http://" + net.JoinHostPort(s.opt.DashboardHost, strconv.Itoa(port))
}

// authCode creates a single-use sign-in link for a browser. The link is on
// 127.0.0.1, so the opener's arguments do not show the dashboard host; the
// reply names the dashboard separately, for the client to show.
func (s *Server) authCode(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Next string `json:"next"`
	}
	if err := readJSON(r, &body); err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	q := url.Values{"code": {s.codes.issue()}}
	if safeNext(body.Next) {
		q.Set("next", body.Next)
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"url":       "http://" + net.JoinHostPort("127.0.0.1", strconv.Itoa(s.opt.Port)) + "/auth?" + q.Encode(),
		"dashboard": s.dashboardOrigin(s.opt.Port),
	})
}

// safeNext reports whether next is a path on the dashboard that no browser
// reads as another host: no control characters or backslashes (browsers
// drop tabs and newlines and read a backslash as a slash), and a relative
// URL that is only a path, with an optional query and fragment.
func safeNext(next string) bool {
	if !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") || strings.ContainsRune(next, '\\') {
		return false
	}
	for _, c := range next {
		if unicode.IsControl(c) {
			return false
		}
	}
	u, err := url.Parse(next)
	return err == nil && u.Scheme == "" && u.User == nil && u.Host == "" && u.Opaque == "" &&
		strings.HasPrefix(u.Path, "/") && !strings.HasPrefix(u.Path, "//")
}

// signIn redeems a code, sets the session cookie and continues to the
// dashboard. On another host it only forwards a valid link to the
// dashboard host, keeping the port the browser used (the Vite dev server
// proxies to the daemon from its own port). A code is forwarded and
// redeemed only for a connection from this account; any other learns
// nothing of the dashboard host and leaves the code unused.
func (s *Server) signIn(w http.ResponseWriter, r *http.Request) {
	code := r.URL.Query().Get("code")
	if !s.onDashboardHost(r) {
		if s.opt.DashboardHost != "" && s.codes.valid(code) {
			if !s.fromOwner(r) {
				s.signInPage(w, http.StatusForbidden, ownAccountOnly)
				return
			}
			port := s.opt.Port
			if _, p, err := net.SplitHostPort(r.Host); err == nil {
				if n, err := strconv.Atoi(p); err == nil {
					port = n
				}
			}
			http.Redirect(w, r, s.dashboardOrigin(port)+"/auth?"+r.URL.RawQuery, http.StatusFound)
			return
		}
		s.signInPage(w, http.StatusUnauthorized, "This sign-in link has expired")
		return
	}
	if !s.codes.valid(code) {
		if s.authorized(r) {
			http.Redirect(w, r, "/", http.StatusFound)
			return
		}
		s.signInPage(w, http.StatusUnauthorized, "This sign-in link has expired")
		return
	}
	if !s.fromOwner(r) {
		s.signInPage(w, http.StatusForbidden, ownAccountOnly)
		return
	}
	if !s.codes.redeem(code) {
		s.signInPage(w, http.StatusUnauthorized, "This sign-in link has expired")
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    s.sessionCookie,
		Path:     "/",
		MaxAge:   int((365 * 24 * time.Hour).Seconds()),
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	})
	next := r.URL.Query().Get("next")
	if !safeNext(next) {
		next = "/"
	}
	http.Redirect(w, r, next, http.StatusFound)
}

// staleDashboard answers a request for a dashboard host that is not the
// current one, such as one from before the daemon's last cold start: its
// cookie is cleared, a browser gets the page on how to open the dashboard
// and the dashboard's own probe hears that it is signed out. Nothing points
// to the current host.
func (s *Server) staleDashboard(w http.ResponseWriter, r *http.Request) {
	expireLegacyCookie(w, r)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set(signInHeader, "required")
	switch {
	case r.URL.Path == "/api/session":
		writeJSON(w, http.StatusOK, map[string]bool{"authenticated": false})
	case strings.HasPrefix(r.URL.Path, "/api/"):
		fail(w, http.StatusUnauthorized, errUnauthorized)
	default:
		s.signInPage(w, http.StatusUnauthorized, "This dashboard address is no longer in use")
	}
}

// signInPage explains how to open the dashboard. It is what a browser gets
// at 127.0.0.1, localhost or an old dashboard host, which never reveal the
// current dashboard host.
func (s *Server) signInPage(w http.ResponseWriter, code int, heading string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set(signInHeader, "required")
	w.WriteHeader(code)
	fmt.Fprintf(w, `<!doctype html><meta charset="utf-8"><title>%s</title>
<body style="font:15px system-ui;background:#191724;color:#e0def4;display:grid;place-items:center;height:100vh;margin:0">
<main style="max-width:28rem;text-align:center"><h1 style="font-size:20px">%s</h1>
<p>Open the dashboard from the menu bar app, or run <code>%s open</code>.</p></main>`, app.Name, htmlEscape(heading), app.ID)
}

// expireLegacyCookie removes a session cookie a browser still keeps for a
// host other than the dashboard's.
func expireLegacyCookie(w http.ResponseWriter, r *http.Request) {
	if _, err := r.Cookie(cookieName); err == nil {
		http.SetCookie(w, &http.Cookie{Name: cookieName, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteStrictMode})
	}
}

var ownAccountOnly = "This sign-in link works only for the account running " + app.Name

// errForeignSession refuses a valid session cookie sent over a connection
// from another account.
var errForeignSession = errors.New("this session works only for the account running " + app.Name)

var errUnauthorized = errors.New("not signed in: open the dashboard with `" + app.ID + " open` or from the menu bar app")
