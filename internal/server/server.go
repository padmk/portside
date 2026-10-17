// Package server exposes the supervisor over HTTP: the JSON API used by the
// CLI and dashboard, WebSocket streams, the embedded dashboard itself, and a
// reverse proxy that serves every server at http://<name>.localhost.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/padmk/portside/internal/app"
	"github.com/padmk/portside/internal/config"
	"github.com/padmk/portside/internal/docker"
	"github.com/padmk/portside/internal/remote"
	"github.com/padmk/portside/internal/share"
	"github.com/padmk/portside/internal/supervisor"
)

// Options configure a Server.
type Options struct {
	ConfigError string
	Supervisor  *supervisor.Supervisor
	Docker      *docker.Monitor
	ConfigPath  string
	StateDir    string
	Port        int
	UI          fs.FS // dashboard assets; nil serves a placeholder
	// Token authenticates API clients; empty disables authentication.
	Token string
	// Shutdown is called when a client asks the daemon to exit; restart is
	// true when the client starts a daemon again right after.
	Shutdown func(restart bool)
	// Upgrade re-executes the daemon from the binary at exe (its own when
	// empty), keeping servers running. It returns only when that did not
	// happen.
	Upgrade func(exe string) error
	// Remote connects to the daemon on another of the owner's devices.
	Remote *remote.Manager
	// Share lets the owner's other devices connect to this daemon.
	Share *share.Manager
}

// Server is the daemon's HTTP surface.
type Server struct {
	opt        Options
	sup        *supervisor.Supervisor
	started    time.Time
	editMu     sync.Mutex // serialises config file edits
	cfgMu      sync.Mutex
	cfgErr     string
	cfgMod     time.Time
	cfgRev     string
	devOrigins map[string]bool
	shareWant  bool // the share setting last handed to Options.Share
	mux        *http.ServeMux
	codes      codes
}

// New builds the HTTP server.
func New(opt Options) *Server {
	s := &Server{opt: opt, sup: opt.Supervisor, cfgErr: opt.ConfigError, started: time.Now(), devOrigins: map[string]bool{}}
	for _, o := range strings.Split(os.Getenv(app.Env("DEV_ORIGIN")), ",") {
		if o = strings.TrimSpace(o); o != "" {
			s.devOrigins[strings.TrimRight(o, "/")] = true
		}
	}
	if st, err := os.Stat(opt.ConfigPath); err == nil {
		s.cfgMod = st.ModTime()
	}
	if data, err := os.ReadFile(opt.ConfigPath); err == nil {
		s.cfgRev = config.Revision(data)
	}
	s.shareWant = opt.Supervisor.Config().Share
	opt.Share.Set(s.shareWant)
	s.routes()
	return s
}

// Handler returns the root handler: it routes by Host between the proxy and
// the dashboard, and rejects unknown hosts to defeat DNS rebinding.
func (s *Server) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := hostname(r.Host)
		if sub, ok := strings.CutSuffix(host, ".localhost"); ok && sub != app.ID && sub != "" {
			s.route(w, r, sub)
			return
		}
		if !s.dashboardHost(host) {
			http.Error(w, "host not allowed", http.StatusMisdirectedRequest)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead || isWebSocket(r) {
			if !s.originAllowed(r) {
				http.Error(w, "cross-origin request refused", http.StatusForbidden)
				return
			}
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "same-origin")
		if !public(r) && !s.authorized(r) {
			fail(w, http.StatusUnauthorized, errUnauthorized)
			return
		}
		s.mux.ServeHTTP(w, r)
	})
}

func hostname(hostport string) string {
	h := hostport
	if host, _, err := net.SplitHostPort(hostport); err == nil {
		h = host
	}
	return strings.ToLower(strings.Trim(h, "[]"))
}

func (s *Server) dashboardHost(h string) bool {
	switch h {
	case "127.0.0.1", "localhost", "::1", app.ID + ".localhost":
		return true
	}
	return false
}

func (s *Server) originAllowed(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		// Browsers always send Origin on cross-site writes and WebSockets, so
		// its absence means a non-browser client such as the CLI.
		return true
	}
	if s.devOrigins[origin] {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil || u.Scheme != "http" {
		return false
	}
	port := u.Port()
	if port == "" {
		port = "80"
	}
	return s.dashboardHost(hostname(u.Host)) && port == strconv.Itoa(s.opt.Port)
}

func isWebSocket(r *http.Request) bool {
	return strings.EqualFold(r.Header.Get("Upgrade"), "websocket")
}

// route picks the server for <sub>.localhost: <name>.<computer> is a server
// on the connected remote device; a plain <name> is a local server, else a
// remote one of that name, so links copied on the other device work here.
func (s *Server) route(w http.ResponseWriter, r *http.Request, sub string) {
	name, label, onRemote := strings.Cut(sub, ".")
	if onRemote {
		if !s.proxyRemote(w, r, name, label) {
			msg := "There is no server called " + name + " on " + label + "."
			if _, err := s.opt.Remote.Route(name, label); errors.Is(err, remote.ErrNotConnected) {
				msg = app.Name + " is not connected to " + label + "."
			}
			s.proxyError(w, name, http.StatusNotFound, msg)
		}
		return
	}
	if _, local := s.sup.ActivePort(name); !local && s.proxyRemote(w, r, name, "") {
		return
	}
	s.proxy(w, r, name)
}

// proxy forwards a request for <name>.localhost to that server's port.
func (s *Server) proxy(w http.ResponseWriter, r *http.Request, name string) {
	port, ok := s.sup.ActivePort(name)
	if !ok || port == 0 {
		s.proxyError(w, name, http.StatusNotFound, "There is no server called "+name+".")
		return
	}
	address, _ := s.sup.ActiveAddress(name)
	target := &url.URL{Scheme: "http", Host: address}
	rp := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(target)
			pr.SetXForwarded()
			pr.Out.Host = pr.In.Host // dev servers build absolute URLs from Host
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			s.proxyError(w, name, http.StatusBadGateway, name+" is not responding on port "+strconv.Itoa(port)+".")
		},
	}
	rp.ServeHTTP(w, r)
}

func (s *Server) proxyError(w http.ResponseWriter, name string, code int, msg string) {
	dash := "http://127.0.0.1:" + strconv.Itoa(s.opt.Port) + "/#/" + url.PathEscape(name)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(code)
	fmt.Fprintf(w, `<!doctype html><meta charset="utf-8"><title>%s</title>
<style>body{font:15px/1.5 system-ui,sans-serif;background:#171d23;color:#e8e4dc;display:grid;place-items:center;height:100vh;margin:0}
main{max-width:28rem;text-align:center}a{color:#e8836f}code{font-family:ui-monospace,monospace}</style>
<main><p><code>%s.localhost</code></p><h1 style="font-weight:600;font-size:20px">%s</h1>
<p><a href="%s">Open %s</a></p></main>`, app.Name, htmlEscape(name), htmlEscape(msg), dash, app.Name)
}

func htmlEscape(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&#39;")
	return r.Replace(s)
}

// WatchConfig reloads the configuration when the file changes on disk.
func (s *Server) WatchConfig(ctx context.Context) {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		st, err := os.Stat(s.opt.ConfigPath)
		if err != nil {
			continue
		}
		s.cfgMu.Lock()
		changed := !st.ModTime().Equal(s.cfgMod)
		s.cfgMu.Unlock()
		if changed {
			// Serialised with edits, so a reload never applies a file
			// older than one an edit has already applied.
			s.editMu.Lock()
			s.reload()
			s.editMu.Unlock()
		}
	}
}

// reload re-reads the config; a broken file keeps the previous config live.
func (s *Server) reload(rename ...string) error {
	// Save automatic ports first; an invalid file is reported by Parse below.
	_, portsErr := config.LoadAssignedPorts(s.opt.ConfigPath, s.sup.TemporaryPorts())
	st, statErr := os.Stat(s.opt.ConfigPath)
	data, err := os.ReadFile(s.opt.ConfigPath)
	var cfg *config.Config
	if err == nil {
		cfg, err = config.Parse(s.opt.ConfigPath, data)
	}
	if err == nil && portsErr != nil {
		log.Printf("config: saving automatic ports: %v", portsErr)
	}
	s.cfgMu.Lock()
	if statErr == nil {
		s.cfgMod = st.ModTime()
	}
	s.cfgRev = config.Revision(data)
	if err != nil {
		s.cfgErr = err.Error()
		s.cfgMu.Unlock()
		log.Printf("config: %v", err)
		s.sup.Apply(s.sup.Config()) // nudge subscribers so the error shows
		return err
	}
	if err := config.CacheGood(s.opt.StateDir, s.opt.ConfigPath, data); err != nil {
		log.Printf("cache config: %v", err)
	}
	s.cfgErr = ""
	shareChanged := cfg.Share != s.shareWant
	s.shareWant = cfg.Share
	s.opt.Share.Set(cfg.Share)
	s.cfgMu.Unlock()
	if shareChanged {
		go s.opt.Share.Apply(context.Background())
	}
	if cfg.Port != s.opt.Port {
		cfg.Warnings = append(cfg.Warnings, fmt.Sprintf("port changed to %d; restart the daemon to apply", cfg.Port))
		cfg.Port = s.opt.Port
	}
	if len(rename) == 2 {
		s.sup.ApplyRenamed(cfg, rename[0], rename[1])
	} else {
		s.sup.Apply(cfg)
	}
	return nil
}

func (s *Server) configError() string {
	s.cfgMu.Lock()
	defer s.cfgMu.Unlock()
	return s.cfgErr
}

func (s *Server) configRevision() string {
	s.cfgMu.Lock()
	defer s.cfgMu.Unlock()
	return s.cfgRev
}

// ── JSON helpers ─────────────────────────────────────────────────────────

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

type apiError struct {
	Error string `json:"error"`
	Code  string `json:"code,omitempty"`
}

func fail(w http.ResponseWriter, code int, err error) {
	e := apiError{Error: err.Error()}
	if errors.Is(err, supervisor.ErrPortBusy) {
		e.Code = "port_busy"
		code = http.StatusConflict
	}
	writeJSON(w, code, e)
}

func readJSON(r *http.Request, v any) error {
	if r.ContentLength == 0 {
		return nil
	}
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20))
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("invalid JSON body: %w", err)
	}
	return nil
}

// ── static UI ────────────────────────────────────────────────────────────

func (s *Server) serveUI(w http.ResponseWriter, r *http.Request) {
	if s.opt.UI == nil {
		s.placeholder(w)
		return
	}
	p := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
	if p == "" {
		p = "index.html"
	}
	f, err := fs.Stat(s.opt.UI, p)
	if err != nil || f.IsDir() {
		p = "index.html" // client-side routes
		if _, err := fs.Stat(s.opt.UI, p); err != nil {
			s.placeholder(w)
			return
		}
	}
	if strings.HasPrefix(p, "assets/") {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		w.Header().Set("Cache-Control", "no-cache")
	}
	http.ServeFileFS(w, r, s.opt.UI, p)
}

func (s *Server) placeholder(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, `<!doctype html><meta charset="utf-8"><title>%s</title>
<body style="font:15px system-ui;background:#171d23;color:#e8e4dc;padding:3rem">
<h1 style="font-size:20px">%s is running</h1><p>The dashboard was not built into this binary. Run <code>make build</code>.</p>
<p>API: <a style="color:#e8836f" href="/api/state">/api/state</a></p>`, app.Name, app.Name)
}
