package server

import (
	"context"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/coder/websocket"

	"github.com/padmk/portside/internal/app"
	"github.com/padmk/portside/internal/config"
	"github.com/padmk/portside/internal/remote"
	"github.com/padmk/portside/internal/share"
)

// Connections between the owner's devices. On the device being used, the
// /api/remote routes find and connect to other devices and /api/peer relays
// the dashboard to the connected one. On the shared device, PeerHandler
// serves what package share accepted.

func (s *Server) remoteRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /api/remote", s.remoteOverview)
	m.HandleFunc("GET /api/remote/peers", s.remotePeers)
	m.HandleFunc("POST /api/remote/connect", s.remoteConnect)
	m.HandleFunc("POST /api/remote/disconnect", s.remoteDisconnect)
	m.HandleFunc("POST /api/remote/pair", s.remotePair)
	m.HandleFunc("PUT /api/remote/ports", s.remotePort)
	m.HandleFunc("PUT /api/remote/share", s.setShare)
	m.HandleFunc("POST /api/remote/share/code", s.shareCode)
	m.HandleFunc("DELETE /api/remote/share/devices/{id}", s.revokeDevice)
	m.HandleFunc(remote.PathPrefix+"/", s.opt.Remote.ServeAPI)
}

// RemoteOverview is the answer of GET /api/remote.
type RemoteOverview struct {
	Share      share.Status `json:"share"`
	Connection *remote.Info `json:"connection"`
}

func (s *Server) overview() RemoteOverview {
	return RemoteOverview{Share: s.opt.Share.Status(), Connection: s.opt.Remote.Info()}
}

func (s *Server) remoteOverview(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.overview())
}

func (s *Server) remotePeers(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	writeJSON(w, http.StatusOK, s.opt.Remote.Discover(ctx))
}

func (s *Server) remoteConnect(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Peer string `json:"peer"`
	}
	if err := readJSON(r, &body); err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	if strings.TrimSpace(body.Peer) == "" {
		fail(w, http.StatusBadRequest, errors.New("peer is required"))
		return
	}
	info, err := s.opt.Remote.Connect(r.Context(), body.Peer)
	if err != nil {
		failRemote(w, err)
		return
	}
	writeJSON(w, http.StatusOK, info)
}

// failRemote answers with a remote.Error's status and machine-readable code.
func failRemote(w http.ResponseWriter, err error) {
	var e *remote.Error
	if !errors.As(err, &e) {
		fail(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, e.Status, apiError{Error: e.Msg, Code: e.Code})
}

// remotePair redeems a pairing code shown on another device, then connects.
func (s *Server) remotePair(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Peer string `json:"peer"`
		Code string `json:"code"`
	}
	if err := readJSON(r, &body); err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	if strings.TrimSpace(body.Peer) == "" || strings.TrimSpace(body.Code) == "" {
		fail(w, http.StatusBadRequest, errors.New("peer and code are required"))
		return
	}
	info, err := s.opt.Remote.Pair(r.Context(), body.Peer, body.Code)
	if err != nil {
		failRemote(w, err)
		return
	}
	writeJSON(w, http.StatusOK, info)
}

// remotePort forwards a port of the connected computer to another local
// port; local 0 forwards it to the same number again.
func (s *Server) remotePort(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Port  int `json:"port"`
		Local int `json:"local"`
	}
	if err := readJSON(r, &body); err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	info, err := s.opt.Remote.SetLocalPort(body.Port, body.Local)
	if err != nil {
		failRemote(w, err)
		return
	}
	writeJSON(w, http.StatusOK, info)
}

func (s *Server) shareCode(w http.ResponseWriter, r *http.Request) {
	code, err := s.opt.Share.NewCode()
	if err != nil {
		fail(w, http.StatusConflict, err)
		return
	}
	writeJSON(w, http.StatusOK, code)
}

func (s *Server) revokeDevice(w http.ResponseWriter, r *http.Request) {
	ok, err := s.opt.Share.Revoke(r.PathValue("id"))
	switch {
	case errors.Is(err, share.ErrAmbiguous):
		fail(w, http.StatusConflict, err)
	case err != nil:
		fail(w, http.StatusInternalServerError, err)
	case !ok:
		fail(w, http.StatusNotFound, errors.New("no paired device "+r.PathValue("id")))
	default:
		writeJSON(w, http.StatusOK, s.overview())
	}
}

func (s *Server) remoteDisconnect(w http.ResponseWriter, r *http.Request) {
	s.opt.Remote.Disconnect()
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// setShare writes the share setting to the config and applies it at once.
func (s *Server) setShare(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Enabled bool `json:"enabled"`
	}
	if err := readJSON(r, &body); err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	s.editMu.Lock()
	err := config.SetTopLevel(s.opt.ConfigPath, "share", strconv.FormatBool(body.Enabled))
	if err == nil {
		// Applied below and waited for, so the reply carries the outcome.
		s.cfgMu.Lock()
		s.shareWant = body.Enabled
		s.cfgMu.Unlock()
		s.opt.Share.Set(body.Enabled)
		err = s.reload()
	}
	s.editMu.Unlock()
	if err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	s.opt.Share.Apply(ctx)
	writeJSON(w, http.StatusOK, s.overview())
}

// ── the shared side ─────────────────────────────────────────────────────

// peerDenied lists routes another device may not use: they act on this
// computer's desktop, stop or replace the daemon, mint sign-in links, or chain
// connections and change sharing.
func peerDenied(p string) bool {
	switch p {
	case "/api/open", "/api/apps", "/api/shutdown", "/api/upgrade", "/api/auth/code":
		return true
	}
	return p == "/api/remote" || strings.HasPrefix(p, "/api/remote/") || strings.HasPrefix(p, remote.PathPrefix+"/")
}

// PeerHandler serves requests from the owner's other devices, after package
// share has verified them. It exposes the API and port tunnels, nothing else.
func (s *Server) PeerHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		w.Header().Set("X-Content-Type-Options", "nosniff")
		switch {
		case p == share.HelloPath && r.Method == http.MethodGet:
			s.peerHello(w, r)
		case p == share.PairPath && r.Method == http.MethodPost:
			s.peerPair(w, r)
		case p == "/api/tunnel":
			s.tunnel(w, r)
		case !strings.HasPrefix(p, "/api/") || peerDenied(p):
			fail(w, http.StatusForbidden, errors.New("not available to other devices"))
		default:
			s.mux.ServeHTTP(w, r)
		}
	})
}

// Hello is what a device learns before and after pairing.
type Hello struct {
	App     string         `json:"app"`
	Version string         `json:"version"`
	Paired  bool           `json:"paired"`
	Servers *remote.Counts `json:"servers,omitempty"` // only when paired
}

func (s *Server) peerHello(w http.ResponseWriter, r *http.Request) {
	h := Hello{App: app.ID, Version: app.Version}
	if p, ok := share.PeerFrom(r.Context()); ok && p.Device != nil {
		h.Paired = true
		var c remote.Counts
		for _, v := range s.sup.Snapshot(s.opt.Port) {
			c.Total++
			if v.PID != 0 {
				c.Running++
			}
		}
		h.Servers = &c
	}
	writeJSON(w, http.StatusOK, h)
}

func (s *Server) peerPair(w http.ResponseWriter, r *http.Request) {
	p, ok := share.PeerFrom(r.Context())
	if !ok {
		fail(w, http.StatusForbidden, errors.New("not a peer request"))
		return
	}
	var body struct {
		Code     string `json:"code"`
		Instance string `json:"instance"`
	}
	if err := readJSON(r, &body); err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	if len(body.Instance) > 64 || strings.Trim(body.Instance, "0123456789abcdef") != "" {
		fail(w, http.StatusBadRequest, errors.New("invalid instance"))
		return
	}
	cred, err := s.opt.Share.Pair(p, body.Code, body.Instance)
	if errors.Is(err, share.ErrPairing) {
		writeJSON(w, http.StatusForbidden, apiError{Error: err.Error(), Code: "pairing_failed"})
		return
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, err)
		return
	}
	log.Printf("share: paired %s", p.Node.DNSName)
	writeJSON(w, http.StatusOK, cred)
}

// fromPeer reports whether a request came from another device.
func fromPeer(r *http.Request) bool {
	_, ok := share.PeerFrom(r.Context())
	return ok
}

// tunnelPorts are the ports another device may reach: those of configured
// servers and the TCP ports published by running containers.
func (s *Server) tunnelPorts() map[int]bool {
	out := map[int]bool{}
	for _, v := range s.sup.Snapshot(s.opt.Port) {
		if v.Port > 0 {
			out[v.Port] = true
		}
		if u, err := url.Parse(v.URL); err == nil {
			if p, err := strconv.Atoi(u.Port()); err == nil {
				out[p] = true
			}
		}
	}
	for _, c := range s.dockerState(nil).Containers {
		if c.State != "running" {
			continue
		}
		for _, p := range c.Ports {
			if p.Proto == "tcp" && p.Public > 0 {
				out[p.Public] = true
			}
		}
	}
	return out
}

// tunnel carries one TCP connection to a local port in a WebSocket.
func (s *Server) tunnel(w http.ResponseWriter, r *http.Request) {
	port, err := strconv.Atoi(r.URL.Query().Get("port"))
	if err != nil || !s.tunnelPorts()[port] {
		fail(w, http.StatusForbidden, errors.New("port not available"))
		return
	}
	var local net.Conn
	for _, host := range []string{"127.0.0.1", "::1"} {
		if local, err = net.DialTimeout("tcp", net.JoinHostPort(host, strconv.Itoa(port)), 3*time.Second); err == nil {
			break
		}
	}
	if err != nil {
		fail(w, http.StatusBadGateway, err)
		return
	}
	defer local.Close()
	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return
	}
	nc := websocket.NetConn(r.Context(), ws, websocket.MessageBinary)
	defer nc.Close()
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(local, nc); done <- struct{}{} }()
	go func() { _, _ = io.Copy(nc, local); done <- struct{}{} }()
	select {
	case <-done:
	case <-r.Context().Done():
	}
}

// ── *.localhost for remote servers ──────────────────────────────────────

// proxyRemote serves a remote server at <name>.<label>.localhost, or at
// <name>.localhost when label is empty. It reports false when there is no
// such remote server, so the caller can answer.
func (s *Server) proxyRemote(w http.ResponseWriter, r *http.Request, name, label string) bool {
	rt, err := s.opt.Remote.Route(name, label)
	if err != nil {
		return false
	}
	r, release := remote.Bind(r, rt.Done)
	defer release()
	target := &url.URL{Scheme: "http", Host: "127.0.0.1:" + strconv.Itoa(rt.Port)}
	rp := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(target)
			pr.SetXForwarded()
			pr.Out.Host = pr.In.Host
		},
		Transport: rt.Transport,
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			s.proxyError(w, name, http.StatusBadGateway, name+" on "+rt.Peer+" is not responding on port "+strconv.Itoa(rt.Port)+".")
		},
	}
	rp.ServeHTTP(w, r)
	return true
}
