// Package client talks to a running daemon and starts one when needed.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/padmk/portside/internal/app"
	"github.com/padmk/portside/internal/paths"
)

// Client is an HTTP client bound to the daemon's port.
type Client struct {
	Port  int
	Base  string
	http  *http.Client
	token string
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
	req, err := http.NewRequestWithContext(ctx, method, c.Base+path, rd)
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
	App     string `json:"app"`
	Version string `json:"version"`
	PID     int    `json:"pid"`
	Port    int    `json:"port"`
}

// Ping returns the daemon's identity, or an error if none is answering.
func (c *Client) Ping(ctx context.Context) (Health, error) {
	ctx, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
	defer cancel()
	var h Health
	if err := c.Do(ctx, http.MethodGet, "/api/health", nil, &h); err != nil {
		return h, err
	}
	if h.App != app.ID {
		return h, fmt.Errorf("port %d is used by another program, not %s", c.Port, app.Name)
	}
	return h, nil
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
	if err := SpawnDaemon(); err != nil {
		return h, fmt.Errorf("starting daemon: %w", err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(150 * time.Millisecond)
		if h, err = c.Ping(ctx); err == nil {
			return h, nil
		}
	}
	return h, fmt.Errorf("daemon did not come up; see %s", paths.ShortenHome(paths.DaemonLog()))
}

// SpawnDaemon starts `<self> daemon` detached from the terminal, logging to
// the daemon log file.
func SpawnDaemon() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	if err := os.MkdirAll(paths.StateDir(), 0o755); err != nil {
		return err
	}
	logf, err := os.OpenFile(paths.DaemonLog(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer logf.Close()
	cmd := exec.Command(exe, "daemon")
	cmd.Stdout = logf
	cmd.Stderr = logf
	cmd.Stdin = nil
	cmd.Dir = "/"
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}
