package server

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/padmk/portside/internal/app"
)

// The daemon runs commands as its user, so only that user may drive it. A
// random token in a 0600 file proves it: the CLI, MCP server and macOS app
// send it as a bearer token, and a browser receives it as an HttpOnly cookie
// after opening a one-time link that one of those clients created.

const (
	tokenFile = "auth-token"
	codeTTL   = 2 * time.Minute
	// codeReuse lets a link survive a browser sending the same request twice
	// (prefetch, retries) without turning it into a long-lived credential.
	codeReuse = 10 * time.Second
)

var cookieName = app.ID + "_session"

// TokenPath is where the daemon keeps its token.
func TokenPath(stateDir string) string { return filepath.Join(stateDir, tokenFile) }

// LoadOrCreateToken returns the token, creating it with private permissions.
func LoadOrCreateToken(stateDir string) (string, error) {
	p := TokenPath(stateDir)
	if b, err := os.ReadFile(p); err == nil {
		if t := strings.TrimSpace(string(b)); len(t) >= 32 {
			if st, err := os.Stat(p); err == nil && st.Mode().Perm()&0o077 != 0 {
				_ = os.Chmod(p, 0o600)
			}
			return t, nil
		}
	}
	t, err := randomHex(32)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return "", err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, []byte(t+"\n"), 0o600); err != nil {
		return "", err
	}
	return t, os.Rename(tmp, p)
}

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

type codes struct {
	mu sync.Mutex
	m  map[string]time.Time
}

func (c *codes) issue() (string, error) {
	code, err := randomHex(16)
	if err != nil {
		return "", err
	}
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

func (s *Server) authorized(r *http.Request) bool {
	if s.opt.Token == "" {
		return true
	}
	given := ""
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		given = strings.TrimPrefix(h, "Bearer ")
	} else if c, err := r.Cookie(cookieName); err == nil {
		given = c.Value
	}
	return given != "" && subtle.ConstantTimeCompare([]byte(given), []byte(s.opt.Token)) == 1
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
		"url": fmt.Sprintf("http://127.0.0.1:%d/auth?%s", s.opt.Port, q.Encode()),
	})
}

func safeNext(next string) bool {
	return strings.HasPrefix(next, "/") && !strings.HasPrefix(next, "//") && !strings.ContainsAny(next, "\\\r\n")
}

// signIn redeems a code, sets the session cookie and continues to the dashboard.
func (s *Server) signIn(w http.ResponseWriter, r *http.Request) {
	if !s.codes.redeem(r.URL.Query().Get("code")) {
		if s.authorized(r) {
			http.Redirect(w, r, "/", http.StatusFound)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprintf(w, `<!doctype html><meta charset="utf-8"><title>%s</title>
<body style="font:15px system-ui;background:#191724;color:#e0def4;display:grid;place-items:center;height:100vh;margin:0">
<main style="max-width:28rem;text-align:center"><h1 style="font-size:20px">This sign-in link has expired</h1>
<p>Open the dashboard from the menu bar app, or run <code>%s open</code>.</p></main>`, app.Name, app.ID)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    s.opt.Token,
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

var errUnauthorized = errors.New("not signed in: open the dashboard with `" + app.ID + " open` or from the menu bar app")
