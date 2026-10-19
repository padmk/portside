package server

import (
	"bytes"
	"crypto/subtle"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/padmk/portside/internal/app"
	"github.com/padmk/portside/internal/config"
	"github.com/padmk/portside/internal/signing"
)

// The daemon runs commands as its user, so only that user may drive it. A
// random token in a 0600 file proves it, without ever being sent: the CLI,
// MCP server and macOS app sign each request with it (package signing), and
// the daemon checks the signature, its time and that its nonce is new. A
// plain `Authorization: Bearer <token>` header is accepted too, for manual
// use with curl; no shipped client sends one.
//
// A browser gets a session cookie after opening a one-time link that one of
// those clients created. The cookie lives on the dashboard's own host,
// http://<label>.localhost:<port>, with a random label kept in another 0600
// file: browsers send cookies to every port of a host, so a cookie on
// 127.0.0.1 would reach every dev server, container or forwarded port opened
// there. The label is never revealed to an unauthenticated request, and the
// cookie value is derived from the token, not the token itself, and is
// accepted only on requests to that host.

const (
	tokenFile = "auth-token"
	hostFile  = "dashboard-host"
	codeTTL   = 2 * time.Minute
	// codeReuse lets a link survive a browser sending the same request twice
	// (prefetch, retries) without turning it into a long-lived credential.
	codeReuse = 10 * time.Second
)

var cookieName = app.ID + "_session"

// TokenPath is where the daemon keeps its token.
func TokenPath(stateDir string) string { return filepath.Join(stateDir, tokenFile) }

// DashboardHostPath is where the daemon keeps its dashboard's host name.
func DashboardHostPath(stateDir string) string { return filepath.Join(stateDir, hostFile) }

// Credentials are the daemon's API token and its dashboard's host name.
type Credentials struct {
	Token string
	Host  string // <label>.localhost
}

// LoadCredentials returns the token and dashboard host, creating them with
// private permissions. A state directory without a dashboard host predates
// host-bound sessions, when browsers kept the token itself as a cookie for
// 127.0.0.1; the token is replaced then, so such cookies stop working.
func LoadCredentials(stateDir string) (Credentials, error) {
	var c Credentials
	host, hostOK := readPrivate(DashboardHostPath(stateDir))
	if label, ok := strings.CutSuffix(host, ".localhost"); hostOK && ok && config.IsDashboardLabel(label) {
		c.Host = host
	}
	if t, ok := readPrivate(TokenPath(stateDir)); ok && signing.ValidKey(t) && c.Host != "" {
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
	if c.Host == "" {
		c.Host = "d-" + signing.RandomHex(12) + ".localhost"
		if err := writePrivate(DashboardHostPath(stateDir), c.Host); err != nil {
			return c, err
		}
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
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, []byte(value+"\n"), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

// sessionValue is the dashboard cookie for a token: bound to it, but not
// usable as the token.
func sessionValue(token string) string { return signing.MAC(token, "dashboard session") }

type codes struct {
	mu sync.Mutex
	m  map[string]time.Time
}

func (c *codes) issue() (string, error) {
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
	return code, nil
}

// valid reports whether a code could be redeemed now, without using it.
func (c *codes) valid(code string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	exp, ok := c.m[code]
	return ok && time.Now().Before(exp)
}

// redeem consumes a code: after the first use it stays valid only briefly.
func (c *codes) redeem(code string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	exp, ok := c.m[code]
	now := time.Now()
	if !ok || now.After(exp) {
		delete(c.m, code)
		return false
	}
	if short := now.Add(codeReuse); short.Before(exp) {
		c.m[code] = short
	}
	return true
}

// authenticate checks a request's credentials: a signature made with the
// token, the token as a bearer token, or the session cookie on the
// dashboard host. A signed request's body is read to be hashed and put
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
			return nil
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

// authCode creates a one-time sign-in link for a browser.
func (s *Server) authCode(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Next string `json:"next"`
	}
	if err := readJSON(r, &body); err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	code, err := s.codes.issue()
	if err != nil {
		fail(w, http.StatusInternalServerError, err)
		return
	}
	q := url.Values{"code": {code}}
	if safeNext(body.Next) {
		q.Set("next", body.Next)
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"url": s.dashboardOrigin(s.opt.Port) + "/auth?" + q.Encode(),
	})
}

func safeNext(next string) bool {
	return strings.HasPrefix(next, "/") && !strings.HasPrefix(next, "//") && !strings.ContainsAny(next, "\\\r\n")
}

// signIn redeems a code, sets the session cookie and continues to the
// dashboard. On another host it only forwards a valid link to the
// dashboard host, keeping the port the browser used (the Vite dev server
// proxies to the daemon from its own port).
func (s *Server) signIn(w http.ResponseWriter, r *http.Request) {
	code := r.URL.Query().Get("code")
	if !s.onDashboardHost(r) {
		if s.opt.DashboardHost != "" && s.codes.valid(code) {
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
	if !s.codes.redeem(code) {
		if s.authorized(r) {
			http.Redirect(w, r, "/", http.StatusFound)
			return
		}
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

// signInPage explains how to open the dashboard. It is what a browser gets
// at 127.0.0.1 or localhost, which never reveal the dashboard's host.
func (s *Server) signInPage(w http.ResponseWriter, code int, heading string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
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

var errUnauthorized = errors.New("not signed in: open the dashboard with `" + app.ID + " open` or from the menu bar app")
