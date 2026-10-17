// Package client talks to a running daemon and starts one when needed.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/padmk/portside/internal/app"
	"github.com/padmk/portside/internal/paths"
	"github.com/padmk/portside/internal/service"
)

// Client is an HTTP client bound to the daemon's port.
type Client struct {
	Port  int
	Base  string
	http  *http.Client
	token string
	// peer is the DNS name of the connected computer that API requests go
	// to, through this daemon; empty for this computer.
	peer string
}

// Path returns where the daemon serves an API path: the path itself, or
// the same path on the connected computer for a client made by On.
func (c *Client) Path(p string) string {
	if c.peer == "" || !strings.HasPrefix(p, "/api/") {
		return p
	}
	return "/api/peer/" + url.PathEscape(c.peer) + strings.TrimPrefix(p, "/api")
}

// WebSocketURL returns the ws:// URL of an API path, routed as Path does.
func (c *Client) WebSocketURL(p string) string {
	return "ws://127.0.0.1:" + strconv.Itoa(c.Port) + c.Path(p)
}

// Connection is this daemon's connection to another computer, as
// GET /api/remote reports it.
type Connection struct {
	Peer struct {
		Name    string `json:"name"`
		Label   string `json:"label"`
		DNSName string `json:"dnsName"`
	} `json:"peer"`
	Status    string `json:"status"`
	Error     string `json:"error,omitempty"`
	LocalPort int    `json:"localPort"`
	Mirrors   []struct {
		Port  int    `json:"port"`
		Local int    `json:"local"`
		State string `json:"state"`
	} `json:"mirrors"`
}

// Is reports whether name is the connected computer's name, DNS label or
// DNS name.
func (cn *Connection) Is(name string) bool {
	name = strings.TrimSuffix(strings.TrimSpace(name), ".")
	return strings.EqualFold(name, cn.Peer.Name) || strings.EqualFold(name, cn.Peer.Label) || strings.EqualFold(name, cn.Peer.DNSName)
}

// LocalURL returns how this computer opens a URL of the connected
// computer's server: at the local port its port is forwarded to, else
// through this daemon's proxy at <server>.<computer>.localhost.
func (cn *Connection) LocalURL(server, raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return raw
	}
	port, _ := strconv.Atoi(u.Port())
	for _, m := range cn.Mirrors {
		if m.Port == port && m.State == "mirrored" {
			if m.Local > 0 && m.Local != port {
				u.Host = net.JoinHostPort(u.Hostname(), strconv.Itoa(m.Local))
			}
			return u.String()
		}
	}
	u.Host = server + "." + cn.Peer.Label + ".localhost:" + strconv.Itoa(cn.LocalPort)
	return u.String()
}

// On returns a client whose API requests go to the connected computer
// called name (its name, DNS label or DNS name), relayed by this daemon,
// and the connection. It fails when this daemon is not connected to that
// computer.
func (c *Client) On(ctx context.Context, name string) (*Client, *Connection, error) {
	var ov struct {
		Connection *Connection `json:"connection"`
	}
	if err := c.Do(ctx, http.MethodGet, "/api/remote", nil, &ov); err != nil {
		return nil, nil, err
	}
	cn := ov.Connection
	switch {
	case cn == nil:
		return nil, nil, fmt.Errorf("not connected to %s; connect first with `%s connect %s`", name, app.ID, name)
	case !cn.Is(name):
		return nil, nil, fmt.Errorf("connected to %s, not %s; switch with `%s connect %s`", cn.Peer.Name, name, app.ID, name)
	}
	on := *c
	on.peer = cn.Peer.DNSName
	return &on, cn, nil
}

// tokenFile mirrors server.TokenPath; the client reads it on every request so
// a daemon started after the client still works.
func tokenFile() string { return filepath.Join(paths.StateDir(), "auth-token") }

// Token returns the daemon's API token, or "" if the daemon never ran.
func (c *Client) Token() string {
	if b, err := os.ReadFile(tokenFile()); err == nil {
		c.token = strings.TrimSpace(string(b))
	}
	return c.token
}

// Header returns the headers that authenticate a request, for WebSocket dials.
func (c *Client) Header() http.Header {
	h := http.Header{}
	if t := c.Token(); t != "" {
		h.Set("Authorization", "Bearer "+t)
	}
	return h
}

// New returns a client for the daemon on port.
func New(port int) *Client {
	return &Client{
		Port: port,
		Base: "http://127.0.0.1:" + strconv.Itoa(port),
		http: &http.Client{Timeout: 0},
	}
}

// APIError is an error response from the daemon.
type APIError struct {
	Status int
	Msg    string
	Code   string
	Body   json.RawMessage
}

func (e *APIError) Error() string { return e.Msg }

// Do sends a request with an optional JSON body and decodes a JSON response into out.
func (c *Client) Do(ctx context.Context, method, path string, body, out any) error {
	var rd io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.Base+c.Path(path), rd)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if t := c.Token(); t != "" {
		req.Header.Set("Authorization", "Bearer "+t)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode >= 400 {
		var e struct {
			Error string `json:"error"`
			Code  string `json:"code"`
		}
		_ = json.Unmarshal(data, &e)
		if e.Error == "" {
			e.Error = fmt.Sprintf("%s %s: %s", method, path, resp.Status)
		}
		return &APIError{Status: resp.StatusCode, Msg: e.Error, Code: e.Code, Body: data}
	}
	if out == nil {
		return nil
	}
	if w, ok := out.(io.Writer); ok {
		_, err := w.Write(data)
		return err
	}
	return json.Unmarshal(data, out)
}

// Health is the daemon's identity.
type Health struct {
	App       string    `json:"app"`
	Version   string    `json:"version"`
	PID       int       `json:"pid"`
	Port      int       `json:"port"`
	StartedAt time.Time `json:"startedAt"`
}

// Ping returns the daemon's identity, or an error if none is answering.
func (c *Client) Ping(ctx context.Context) (Health, error) {
	ctx, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
	defer cancel()
	// Always this computer's daemon, also for a client made by On.
	local := *c
	local.peer = ""
	var h Health
	if err := local.Do(ctx, http.MethodGet, "/api/health", nil, &h); err != nil {
		return h, err
	}
	if h.App != app.ID {
		return h, fmt.Errorf("port %d is used by another program, not %s", c.Port, app.Name)
	}
	return h, nil
}

// LivePort returns the port recorded by a running daemon, or 0.
func LivePort() int {
	b, err := os.ReadFile(filepath.Join(paths.StateDir(), "daemon.port"))
	if err != nil {
		return 0
	}
	p, _ := strconv.Atoi(strings.TrimSpace(string(b)))
	return p
}

// ErrNotRunning means no daemon answered and none was started.
var ErrNotRunning = errors.New("daemon is not running")

// Ensure makes sure a daemon answers, starting one in the background if not.
func (c *Client) Ensure(ctx context.Context) (Health, error) {
	h, err := c.Ping(ctx)
	if err == nil {
		return h, nil
	}
	var apiErr *APIError
	if errors.As(err, &apiErr) || (h.App != "" && h.App != app.ID) {
		return h, err
	}
	exited, err := startDaemon()
	if err != nil {
		return h, fmt.Errorf("starting daemon: %w", err)
	}
	failed := fmt.Errorf("daemon did not come up; see %s", paths.ShortenHome(paths.DaemonLog()))
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(150 * time.Millisecond)
		if h, err = c.Ping(ctx); err == nil {
			return h, nil
		}
		select {
		case err := <-exited:
			// A daemon that lost the lock to another one exits cleanly and
			// the other answers soon; any other exit is a failed start.
			if err != nil {
				return h, failed
			}
			exited = nil
		default:
		}
	}
	return h, failed
}

// startDaemon starts a daemon for the current settings: through the login
// service when it runs that daemon, else as a detached child process. The
// channel delivers the child's exit (nil for a clean exit); it is nil when
// the service started the daemon.
func startDaemon() (<-chan error, error) {
	if service.Owns(paths.ConfigFile()) && service.Start() == nil {
		return nil, nil
	}
	return spawnDaemon()
}

// spawnDaemon starts `<self> daemon` detached from the terminal, logging to
// the daemon log file.
func spawnDaemon() (<-chan error, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	if err := os.MkdirAll(paths.StateDir(), 0o755); err != nil {
		return nil, err
	}
	logf, err := os.OpenFile(paths.DaemonLog(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	defer logf.Close()
	cmd := exec.Command(exe, "daemon")
	cmd.Stdout = logf
	cmd.Stderr = logf
	cmd.Stdin = nil
	cmd.Dir = "/"
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	// Waiting reaps the child if it exits while this process still runs.
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	return exited, nil
}
