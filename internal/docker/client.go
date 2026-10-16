package docker

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// Client is a minimal Engine API client. Paths are unversioned, so the engine
// answers with its own current API version.
type Client struct {
	ep   Endpoint
	http *http.Client
}

// NewClient returns a client for ep.
func NewClient(ep Endpoint) *Client {
	dial := func(ctx context.Context) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, ep.Network, ep.Address)
	}
	tr := &http.Transport{
		DialContext:         func(ctx context.Context, _, _ string) (net.Conn, error) { return dial(ctx) },
		MaxIdleConnsPerHost: 8,
		IdleConnTimeout:     30 * time.Second,
	}
	return &Client{ep: ep, http: &http.Client{Transport: tr}}
}

// Endpoint returns where the client connects.
func (c *Client) Endpoint() Endpoint { return c.ep }

// Close drops idle connections.
func (c *Client) Close() { c.http.CloseIdleConnections() }

// APIError is an error answer from the engine.
type APIError struct {
	Status  int
	Message string
}

func (e *APIError) Error() string { return e.Message }

// IsNotFound reports whether err is the engine's 404.
func IsNotFound(err error) bool {
	var ae *APIError
	return errors.As(err, &ae) && ae.Status == http.StatusNotFound
}

func (c *Client) do(ctx context.Context, method, path string, q url.Values, body any) (*http.Response, error) {
	var rd io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rd = bytes.NewReader(data)
	}
	u := "http://docker" + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rd)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		defer resp.Body.Close()
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		var e struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(data, &e)
		if e.Message == "" {
			e.Message = fmt.Sprintf("%s %s: %s", method, path, resp.Status)
		}
		return nil, &APIError{Status: resp.StatusCode, Message: e.Message}
	}
	return resp, nil
}

func (c *Client) call(ctx context.Context, method, path string, q url.Values, body, out any) error {
	resp, err := c.do(ctx, method, path, q, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if out == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// Ping checks that the engine answers and returns its version.
func (c *Client) Ping(ctx context.Context) (string, error) {
	var v struct {
		Version string `json:"Version"`
	}
	if err := c.call(ctx, http.MethodGet, "/version", nil, nil, &v); err != nil {
		return "", err
	}
	return v.Version, nil
}

// apiContainer is one entry of GET /containers/json.
type apiContainer struct {
	ID     string            `json:"Id"`
	Names  []string          `json:"Names"`
	Image  string            `json:"Image"`
	State  string            `json:"State"`
	Status string            `json:"Status"`
	Labels map[string]string `json:"Labels"`
}

func (c *Client) list(ctx context.Context) ([]apiContainer, error) {
	var out []apiContainer
	err := c.call(ctx, http.MethodGet, "/containers/json", url.Values{"all": {"1"}}, nil, &out)
	return out, err
}

// Inspect is the part of GET /containers/{id}/json that is shown or used.
type Inspect struct {
	ID      string    `json:"Id"`
	Name    string    `json:"Name"`
	Created time.Time `json:"Created"`
	Path    string    `json:"Path"`
	Args    []string  `json:"Args"`
	State   struct {
		Status     string    `json:"Status"`
		Running    bool      `json:"Running"`
		Restarting bool      `json:"Restarting"`
		OOMKilled  bool      `json:"OOMKilled"`
		ExitCode   int       `json:"ExitCode"`
		Error      string    `json:"Error"`
		StartedAt  time.Time `json:"StartedAt"`
		FinishedAt time.Time `json:"FinishedAt"`
		Health     *struct {
			Status        string `json:"Status"`
			FailingStreak int    `json:"FailingStreak"`
			Log           []struct {
				Start    time.Time `json:"Start"`
				ExitCode int       `json:"ExitCode"`
				Output   string    `json:"Output"`
			} `json:"Log"`
		} `json:"Health"`
	} `json:"State"`
	RestartCount int `json:"RestartCount"`
	Config       struct {
		Image      string            `json:"Image"`
		Env        []string          `json:"Env"`
		Cmd        []string          `json:"Cmd"`
		Entrypoint []string          `json:"Entrypoint"`
		WorkingDir string            `json:"WorkingDir"`
		Labels     map[string]string `json:"Labels"`
		Tty        bool              `json:"Tty"`
	} `json:"Config"`
	HostConfig struct {
		RestartPolicy struct {
			Name string `json:"Name"`
		} `json:"RestartPolicy"`
	} `json:"HostConfig"`
	Mounts []struct {
		Type        string `json:"Type"`
		Name        string `json:"Name"`
		Source      string `json:"Source"`
		Destination string `json:"Destination"`
		RW          bool   `json:"RW"`
	} `json:"Mounts"`
	NetworkSettings struct {
		Ports    map[string][]struct{ HostIP, HostPort string } `json:"Ports"`
		Networks map[string]struct {
			IPAddress string   `json:"IPAddress"`
			Aliases   []string `json:"Aliases"`
		} `json:"Networks"`
	} `json:"NetworkSettings"`
}

// Inspect returns the full description of one container.
func (c *Client) Inspect(ctx context.Context, id string) (*Inspect, error) {
	var out Inspect
	if err := c.call(ctx, http.MethodGet, "/containers/"+url.PathEscape(id)+"/json", nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Start, Stop and Restart change a container's state.
func (c *Client) Start(ctx context.Context, id string) error {
	return c.call(ctx, http.MethodPost, "/containers/"+url.PathEscape(id)+"/start", nil, nil, nil)
}

func (c *Client) Stop(ctx context.Context, id string) error {
	return c.call(ctx, http.MethodPost, "/containers/"+url.PathEscape(id)+"/stop", nil, nil, nil)
}

func (c *Client) Restart(ctx context.Context, id string) error {
	return c.call(ctx, http.MethodPost, "/containers/"+url.PathEscape(id)+"/restart", nil, nil, nil)
}

// event is one message of GET /events.
type event struct {
	Type   string `json:"Type"`
	Action string `json:"Action"`
	Actor  struct {
		ID         string            `json:"ID"`
		Attributes map[string]string `json:"Attributes"`
	} `json:"Actor"`
}

// events streams container events until ctx ends or the engine goes away.
func (c *Client) events(ctx context.Context, fn func(event)) error {
	filters, _ := json.Marshal(map[string][]string{"type": {"container"}})
	resp, err := c.do(ctx, http.MethodGet, "/events", url.Values{"filters": {string(filters)}}, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	dec := json.NewDecoder(resp.Body)
	for {
		var ev event
		if err := dec.Decode(&ev); err != nil {
			return err
		}
		fn(ev)
	}
}

// statsSample is the part of a stats message used for CPU and memory.
type statsSample struct {
	CPU struct {
		Usage struct {
			Total uint64 `json:"total_usage"`
		} `json:"cpu_usage"`
		System uint64 `json:"system_cpu_usage"`
		Online int    `json:"online_cpus"`
	} `json:"cpu_stats"`
	PreCPU struct {
		Usage struct {
			Total uint64 `json:"total_usage"`
		} `json:"cpu_usage"`
		System uint64 `json:"system_cpu_usage"`
	} `json:"precpu_stats"`
	Memory struct {
		Usage uint64            `json:"usage"`
		Limit uint64            `json:"limit"`
		Stats map[string]uint64 `json:"stats"`
	} `json:"memory_stats"`
}

// cpuPercent follows the docker CLI: share of host CPU time, 100% per core.
func (s *statsSample) cpuPercent() float64 {
	cpu := float64(s.CPU.Usage.Total) - float64(s.PreCPU.Usage.Total)
	sys := float64(s.CPU.System) - float64(s.PreCPU.System)
	if cpu <= 0 || sys <= 0 || s.PreCPU.System == 0 {
		return 0
	}
	n := s.CPU.Online
	if n == 0 {
		n = 1
	}
	return cpu / sys * float64(n) * 100
}

// memBytes is usage without the page cache, as the docker CLI reports it.
func (s *statsSample) memBytes() int64 {
	u := s.Memory.Usage
	for _, k := range []string{"inactive_file", "total_inactive_file"} {
		if v, ok := s.Memory.Stats[k]; ok && v < u {
			return int64(u - v)
		}
	}
	return int64(u)
}

// stats streams usage samples (about one per second) until ctx ends or the
// container stops.
func (c *Client) stats(ctx context.Context, id string, fn func(*statsSample)) error {
	resp, err := c.do(ctx, http.MethodGet, "/containers/"+url.PathEscape(id)+"/stats", url.Values{"stream": {"1"}}, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	dec := json.NewDecoder(resp.Body)
	for {
		var s statsSample
		if err := dec.Decode(&s); err != nil {
			return err
		}
		fn(&s)
	}
}

// Logs streams a container's output. tail limits the backlog ("all" or a
// number); since, when set, starts after that time; stamped prefixes each
// line with its RFC 3339 time and a space. Output of containers without a
// TTY is demultiplexed into one stream.
func (c *Client) Logs(ctx context.Context, id string, tty bool, tail string, since time.Time, follow, stamped bool) (io.ReadCloser, error) {
	q := url.Values{"stdout": {"1"}, "stderr": {"1"}, "tail": {tail}}
	if follow {
		q.Set("follow", "1")
	}
	if stamped {
		q.Set("timestamps", "1")
	}
	if !since.IsZero() {
		q.Set("since", strconv.FormatFloat(float64(since.UnixNano())/1e9, 'f', 9, 64))
	}
	resp, err := c.do(ctx, http.MethodGet, "/containers/"+url.PathEscape(id)+"/logs", q, nil)
	if err != nil {
		return nil, err
	}
	// Engines before API 1.42 label multiplexed output raw-stream too, so the
	// container's TTY decides unless the engine says multiplexed.
	if tty && resp.Header.Get("Content-Type") != "application/vnd.docker.multiplexed-stream" {
		return resp.Body, nil
	}
	pr, pw := io.Pipe()
	go func() {
		defer resp.Body.Close()
		pw.CloseWithError(demux(pw, resp.Body))
	}()
	return pr, nil
}

// demux copies the payload of the engine's multiplexed stream format: an
// 8-byte header (stream, 3 zero bytes, big-endian length) before each frame.
func demux(dst io.Writer, src io.Reader) error {
	r := bufio.NewReaderSize(src, 32<<10)
	var hdr [8]byte
	for {
		if _, err := io.ReadFull(r, hdr[:]); err != nil {
			return err
		}
		n := int64(binary.BigEndian.Uint32(hdr[4:]))
		if _, err := io.CopyN(dst, r, n); err != nil {
			return err
		}
	}
}

// ExecCreate prepares an interactive command with a TTY in a running container.
func (c *Client) ExecCreate(ctx context.Context, id string, cmd []string, env []string) (string, error) {
	body := map[string]any{
		"AttachStdin": true, "AttachStdout": true, "AttachStderr": true,
		"Tty": true, "Cmd": cmd, "Env": env,
	}
	var out struct {
		ID string `json:"Id"`
	}
	if err := c.call(ctx, http.MethodPost, "/containers/"+url.PathEscape(id)+"/exec", nil, body, &out); err != nil {
		return "", err
	}
	return out.ID, nil
}

// ExecRunning reports whether an exec instance's command is still running.
func (c *Client) ExecRunning(ctx context.Context, execID string) (bool, error) {
	var out struct {
		Running bool `json:"Running"`
	}
	if err := c.call(ctx, http.MethodGet, "/exec/"+url.PathEscape(execID)+"/json", nil, nil, &out); err != nil {
		return false, err
	}
	return out.Running, nil
}

// ExecDetached runs a command in a running container without waiting for it
// or reading its output.
func (c *Client) ExecDetached(ctx context.Context, id string, cmd []string) error {
	var out struct {
		ID string `json:"Id"`
	}
	if err := c.call(ctx, http.MethodPost, "/containers/"+url.PathEscape(id)+"/exec", nil, map[string]any{"Cmd": cmd}, &out); err != nil {
		return err
	}
	return c.call(ctx, http.MethodPost, "/exec/"+url.PathEscape(out.ID)+"/start", nil, map[string]any{"Detach": true}, nil)
}

// ExecAttach starts an exec instance and returns the raw, bidirectional
// stream of its TTY. Closing the connection does not end the command; the
// engine keeps it running until it exits.
func (c *Client) ExecAttach(ctx context.Context, execID string) (net.Conn, io.Reader, error) {
	var d net.Dialer
	conn, err := d.DialContext(ctx, c.ep.Network, c.ep.Address)
	if err != nil {
		return nil, nil, err
	}
	// Bound the handshake; the stream itself stays open as long as it is used.
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	body := []byte(`{"Detach":false,"Tty":true}`)
	req, _ := http.NewRequest(http.MethodPost, "http://docker/exec/"+url.PathEscape(execID)+"/start", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "tcp")
	if err := req.Write(conn); err != nil {
		conn.Close()
		return nil, nil, err
	}
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, req)
	if err != nil {
		conn.Close()
		return nil, nil, err
	}
	if resp.StatusCode != http.StatusSwitchingProtocols && resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		conn.Close()
		var e struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(data, &e)
		if e.Message == "" {
			e.Message = "exec: " + resp.Status
		}
		return nil, nil, &APIError{Status: resp.StatusCode, Message: e.Message}
	}
	_ = conn.SetDeadline(time.Time{})
	return conn, br, nil
}

// ExecResize changes the TTY size of an exec instance.
func (c *Client) ExecResize(ctx context.Context, execID string, cols, rows uint16) error {
	q := url.Values{"w": {strconv.Itoa(int(cols))}, "h": {strconv.Itoa(int(rows))}}
	return c.call(ctx, http.MethodPost, "/exec/"+url.PathEscape(execID)+"/resize", q, nil, nil)
}
