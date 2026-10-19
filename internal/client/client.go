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
	"sync"
	"syscall"
	"time"

	"github.com/coder/websocket"

	"github.com/padmk/portside/internal/app"
	"github.com/padmk/portside/internal/paths"
	"github.com/padmk/portside/internal/service"
	"github.com/padmk/portside/internal/signing"
)

// Client is an HTTP client bound to the daemon's port.
type Client struct {
	Port int
	Base string
	http *http.Client
	// peer is the DNS name of the connected computer that API requests go
	// to, through this daemon; empty for this computer.
	peer string
	// proved holds the epoch the daemon last proved, shared by the copies
	// On makes.
	proved *proved
}

type proved struct {
	mu    sync.Mutex
	epoch string
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

// tokenFile and hostFile mirror server.TokenPath and
// server.DashboardHostPath. The client reads the token on every request, so
// a daemon started after the client still works.
func tokenFile() string { return filepath.Join(paths.StateDir(), "auth-token") }
func hostFile() string  { return filepath.Join(paths.StateDir(), "dashboard-host") }

// Token returns the daemon's API token, or "" if the daemon never ran.
func (c *Client) Token() string {
	b, err := os.ReadFile(tokenFile())
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// ErrUnverified means that what answers on the daemon's port could not prove
// it holds this account's token, so nothing signed is sent to it: another
// program holds the port, or a daemon from before verified requests.
var ErrUnverified = errors.New("cannot prove it is this account's daemon")

// Unverified is the error for a daemon that did not prove itself.
func (c *Client) Unverified() error {
	return fmt.Errorf("refusing to talk to port %d: what answers there %w (another program holds the port, or a daemon from before verified requests runs; `%s daemon restart` replaces an old daemon)", c.Port, ErrUnverified, app.ID)
}

// refused is the message for a request refused without a proof: the
// daemon's own refusals of a signature carry none, and neither does a
// reply from a program that is not the daemon.
func (c *Client) refused() string {
	return fmt.Sprintf("the daemon on port %d refused this request's signature (`%s daemon restart` starts it afresh)", c.Port, app.ID)
}

func (c *Client) epoch() string {
	c.proved.mu.Lock()
	defer c.proved.mu.Unlock()
	return c.proved.epoch
}

func (c *Client) setEpoch(e string) {
	c.proved.mu.Lock()
	c.proved.epoch = e
	c.proved.mu.Unlock()
}

// hello asks this computer's daemon for its identity with a fresh challenge
// and checks the proof that it holds the token. The proven epoch is kept
// for signing.
func (c *Client) hello(ctx context.Context) (Health, error) {
	ctx, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
	defer cancel()
	var h Health
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.Base+"/api/health", nil)
	if err != nil {
		return h, err
	}
	challenge := signing.Challenge(req.Header)
	resp, err := c.http.Do(req)
	if err != nil {
		return h, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return h, err
	}
	if resp.StatusCode != http.StatusOK {
		return h, &APIError{Status: resp.StatusCode, Msg: fmt.Sprintf("GET /api/health: %s", resp.Status), Body: data}
	}
	if err := json.Unmarshal(data, &h); err != nil || h.App != app.ID {
		return h, fmt.Errorf("port %d is used by another program, not %s", c.Port, app.Name)
	}
	epoch, ok := signing.ProvenReply(c.Token(), challenge, resp.Header, resp.StatusCode, data)
	if !ok {
		c.setEpoch("")
		return h, nil
	}
	h.Verified = true
	c.setEpoch(epoch)
	return h, nil
}

// proven returns the epoch to sign for, after a handshake when none is
// known. It fails without sending anything signed when the daemon cannot
// prove itself.
func (c *Client) proven(ctx context.Context) (string, error) {
	if e := c.epoch(); e != "" {
		return e, nil
	}
	h, err := c.hello(ctx)
	if err != nil {
		return "", err
	}
	if !h.Verified {
		return "", c.Unverified()
	}
	return c.epoch(), nil
}

// renewed repeats the handshake after the daemon refused a request signed
// for epoch, and reports whether it proved a new epoch (it restarted), so
// the request is worth sending again.
func (c *Client) renewed(ctx context.Context, epoch string) bool {
	c.setEpoch("")
	h, err := c.hello(ctx)
	return err == nil && h.Verified && c.epoch() != epoch
}

// signed adds the signature and a fresh challenge for a request to h and
// returns the challenge. uri is the request URI as sent on the wire. The
// token itself is never sent.
func (c *Client) signed(h http.Header, method, uri string, body []byte, epoch string) string {
	signing.Sign(h, c.Token(), method, uri, body, epoch)
	return signing.Challenge(h)
}

// DialWebSocket opens a signed WebSocket to an API path, routed as Path does.
func (c *Client) DialWebSocket(ctx context.Context, p string) (*websocket.Conn, error) {
	raw := c.WebSocketURL(p)
	u, err := url.Parse(raw)
	if err != nil {
		return nil, err
	}
	for retried := false; ; retried = true {
		epoch, err := c.proven(ctx)
		if err != nil {
			return nil, err
		}
		h := http.Header{}
		challenge := c.signed(h, http.MethodGet, u.RequestURI(), nil, epoch)
		ws, resp, err := websocket.Dial(ctx, raw, &websocket.DialOptions{HTTPHeader: h})
		if err != nil {
			if resp != nil && resp.StatusCode == http.StatusUnauthorized && !retried && c.renewed(ctx, epoch) {
				continue
			}
			return nil, err
		}
		if _, ok := signing.Proven(c.Token(), challenge, resp.Header); !ok {
			ws.CloseNow()
			c.setEpoch("")
			return nil, c.Unverified()
		}
		return ws, nil
	}
}

// DashboardURL returns the dashboard's address for the daemon on port; a
// browser signs in there through `open`.
func DashboardURL(port int) string {
	host := "127.0.0.1"
	if b, err := os.ReadFile(hostFile()); err == nil {
		if h := strings.TrimSpace(string(b)); strings.HasSuffix(h, ".localhost") {
			host = h
		}
	}
	return "http://" + net.JoinHostPort(host, strconv.Itoa(port))
}

// New returns a client for the daemon on port. It dials IPv4 loopback by
// address, never a name that could resolve to another listener.
func New(port int) *Client { return NewAt("127.0.0.1", port) }

// NewAt returns a client for the daemon on port at the loopback address
// host, such as "::1" for a check of the daemon's IPv6 listener.
func NewAt(host string, port int) *Client {
	return &Client{
		Port:   port,
		Base:   "http://" + net.JoinHostPort(host, strconv.Itoa(port)),
		http:   &http.Client{Timeout: 0},
		proved: &proved{},
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

// Do sends a signed request with an optional JSON body and decodes a JSON
// response into out. The daemon must have proved itself first, and its
// reply must carry a proof as well; a request refused for an old epoch is
// sent again once after a new handshake.
func (c *Client) Do(ctx context.Context, method, path string, body, out any) error {
	var payload []byte
	if body != nil {
		var err error
		if payload, err = json.Marshal(body); err != nil {
			return err
		}
	}
	for retried := false; ; retried = true {
		epoch, err := c.proven(ctx)
		if err != nil {
			return err
		}
		req, err := http.NewRequestWithContext(ctx, method, c.Base+c.Path(path), bytes.NewReader(payload))
		if err != nil {
			return err
		}
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		challenge := c.signed(req.Header, method, req.URL.RequestURI(), payload, epoch)
		resp, err := c.http.Do(req)
		if err != nil {
			return err
		}
		data, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return err
		}
		if _, ok := signing.ProvenReply(c.Token(), challenge, resp.Header, resp.StatusCode, data); !ok && resp.StatusCode == http.StatusUnauthorized {
			if !retried && c.renewed(ctx, epoch) {
				continue
			}
			// Whatever answered did not prove itself, so its words are not shown.
			return &APIError{Status: resp.StatusCode, Msg: c.refused()}
		} else if !ok {
			c.setEpoch("")
			return c.Unverified()
		}
		return decodeReply(resp, data, method, path, out)
	}
}

func decodeReply(resp *http.Response, data []byte, method, path string, out any) error {
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
	// Verified is true when it proved that it holds this account's token;
	// only then does the client send it signed requests.
	Verified bool `json:"-"`
}

// Ping returns the identity of this computer's daemon (also for a client
// made by On), or an error if none answers. It repeats the handshake, so
// Verified tells whether what answers proved itself.
func (c *Client) Ping(ctx context.Context) (Health, error) { return c.hello(ctx) }

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

// ErrSandboxed means no daemon answered and none was started because the
// caller runs in an agent's sandbox: a daemon started there would inherit
// the sandbox's file and network limits, and every server it starts too.
var ErrSandboxed = errors.New("not starting the daemon from inside a sandbox")

// sandboxEnv are variables that agent sandboxes set for the commands they
// run: Codex sets CODEX_SANDBOX (seatbelt on macOS) and
// CODEX_SANDBOX_NETWORK_DISABLED, Claude Code's sandbox SANDBOX_RUNTIME.
var sandboxEnv = []string{"CODEX_SANDBOX", "CODEX_SANDBOX_NETWORK_DISABLED", "SANDBOX_RUNTIME"}

// Sandboxed reports whether this process appears to run in an agent's sandbox.
func Sandboxed() bool {
	for _, k := range sandboxEnv {
		if os.Getenv(k) != "" {
			return true
		}
	}
	return false
}

// sandboxedError explains why no daemon is started from a sandbox. When
// this account's daemon runs (it holds the state lock) the sandbox keeps
// this process from reaching it.
func sandboxedError(port int) error {
	if DaemonRunning() {
		return fmt.Errorf("cannot reach the %s daemon from this sandbox: it runs, but connections to 127.0.0.1:%d are blocked here; allow local network access for this command or run it outside the sandbox (%w)", app.Name, port, ErrSandboxed)
	}
	return fmt.Errorf("no %s daemon answers on port %d, and %w (it would inherit the sandbox); if one runs, the sandbox blocks local connections, so allow them; otherwise run `%s daemon start` in a normal terminal or open the %s app, then try again", app.Name, port, ErrSandboxed, app.ID, app.Name)
}

// DaemonRunning reports whether a daemon holds this state directory's lock.
// The test takes a shared lock for a moment; a daemon starting meanwhile
// retries its own.
func DaemonRunning() bool {
	f, err := os.Open(filepath.Join(paths.StateDir(), "daemon.lock"))
	if err != nil {
		return false
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_SH|syscall.LOCK_NB); err != nil {
		return errors.Is(err, syscall.EWOULDBLOCK)
	}
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return false
}

// follow points the client at the port a daemon recorded, when that differs
// from its own: a daemon whose default port was taken listens on another.
func (c *Client) follow() {
	p := LivePort()
	if p <= 0 || p == c.Port {
		return
	}
	host := "127.0.0.1"
	if u, err := url.Parse(c.Base); err == nil && u.Hostname() != "" {
		host = u.Hostname()
	}
	c.Port = p
	c.Base = "http://" + net.JoinHostPort(host, strconv.Itoa(p))
	c.setEpoch("")
}

// Ensure makes sure a daemon answers and proves itself, starting one in the
// background if none answers. When what answers cannot prove itself, it
// returns its claimed identity and an error wrapping ErrUnverified. When
// another program, or another account's daemon, holds the port and no
// daemon of this account runs, it starts one anyway: a daemon without a
// configured port then listens on another port, which the client follows.
// It starts none while this product of this account holds the port (a
// daemon with another state directory), and says which one.
func (c *Client) Ensure(ctx context.Context) (Health, error) {
	h, err := c.Ping(ctx)
	if err == nil && h.Verified {
		return h, nil
	}
	var apiErr *APIError
	foreign := err == nil || errors.As(err, &apiErr) || (h.App != "" && h.App != app.ID)
	var held error
	if foreign {
		if err == nil {
			err = c.Unverified()
		}
		if DaemonRunning() {
			return h, err
		}
		// This product of this account, such as a daemon with another state
		// directory, keeps its port: a daemon started now would refuse it.
		if ph := PortHolder(ctx, c.Port); ph.Ours() {
			return h, errors.New(ph.Refusal(c.Port))
		}
		held = err
	}
	if Sandboxed() {
		if held != nil {
			return h, fmt.Errorf("%w; %w", held, sandboxedError(c.Port))
		}
		return h, sandboxedError(c.Port)
	}
	exited, err := startDaemon()
	if err != nil {
		return h, fmt.Errorf("starting daemon: %w", err)
	}
	failed := fmt.Errorf("daemon did not come up; see %s", paths.ShortenHome(paths.DaemonLog()))
	if held != nil {
		failed = fmt.Errorf("%w; the daemon could not start (see %s)", held, paths.ShortenHome(paths.DaemonLog()))
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(150 * time.Millisecond)
		c.follow()
		if h, err = c.Ping(ctx); err == nil {
			if h.Verified {
				return h, nil
			}
			// What held the port before still does; the new daemon has not
			// reported its own port yet.
			if held == nil {
				return h, c.Unverified()
			}
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
	if err := os.MkdirAll(paths.StateDir(), paths.DirMode); err != nil {
		return nil, err
	}
	logf, err := os.OpenFile(paths.DaemonLog(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, paths.FileMode)
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
