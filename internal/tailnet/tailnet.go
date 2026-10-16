// Package tailnet reads this machine's Tailscale state and manages Tailscale
// Serve entries through the tailscale CLI, which works the same with every
// Tailscale variant (macOS app, Homebrew, Linux packages).
package tailnet

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Node is one device of the tailnet.
type Node struct {
	// Name is the device's host name, e.g. "devbox".
	Name string `json:"name"`
	// DNSName is the MagicDNS name without the trailing dot.
	DNSName string `json:"dnsName"`
	// Label is the first DNS label: a host name safe for URLs.
	Label  string   `json:"label"`
	OS     string   `json:"os"`
	IPs    []string `json:"-"`
	Online bool     `json:"-"`
	Tagged bool     `json:"-"`
	UserID int64    `json:"-"`
}

// Status is a snapshot of this machine's view of the tailnet.
type Status struct {
	Self Node
	// Owner is the login name of the user that owns this machine's node.
	Owner string
	Peers []Node
}

// ErrUnavailable means the tailscale CLI is missing or Tailscale is not connected.
var ErrUnavailable = errors.New("Tailscale is not available")

// candidates are tried when tailscale is not on PATH, which is common for a
// daemon started by launchd with a minimal environment.
var candidates = []string{
	"/usr/local/bin/tailscale",
	"/opt/homebrew/bin/tailscale",
	"/Applications/Tailscale.app/Contents/MacOS/Tailscale",
	"/usr/bin/tailscale",
	"/usr/sbin/tailscale",
}

func binary() (string, error) {
	if p, err := exec.LookPath("tailscale"); err == nil {
		return p, nil
	}
	for _, c := range candidates {
		if p, err := exec.LookPath(c); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("%w: the tailscale command was not found", ErrUnavailable)
}

func run(ctx context.Context, args ...string) ([]byte, error) {
	bin, err := binary()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("tailscale %s: %s", args[0], msg)
	}
	return stdout.Bytes(), nil
}

type rawNode struct {
	HostName     string
	DNSName      string
	OS           string
	TailscaleIPs []string
	Online       bool
	UserID       int64
	Tags         []string
}

type rawStatus struct {
	BackendState string
	Self         *rawNode
	Peer         map[string]*rawNode
	User         map[string]struct{ LoginName string }
}

func node(r *rawNode) Node {
	dns := strings.TrimSuffix(r.DNSName, ".")
	label, _, _ := strings.Cut(dns, ".")
	if label == "" {
		label = strings.ToLower(r.HostName)
	}
	return Node{Name: r.HostName, DNSName: dns, Label: label, OS: r.OS, IPs: r.TailscaleIPs,
		Online: r.Online, Tagged: len(r.Tags) > 0, UserID: r.UserID}
}

// Fetch reads the current status.
func Fetch(ctx context.Context) (*Status, error) {
	out, err := run(ctx, "status", "--json")
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	return parseStatus(out)
}

// parseStatus reads the output of `tailscale status --json`.
func parseStatus(out []byte) (*Status, error) {
	var raw rawStatus
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, fmt.Errorf("tailscale status: %w", err)
	}
	if raw.BackendState != "Running" || raw.Self == nil {
		return nil, fmt.Errorf("%w: Tailscale is %s", ErrUnavailable, strings.ToLower(raw.BackendState))
	}
	st := &Status{Self: node(raw.Self)}
	if u, ok := raw.User[strconv.FormatInt(raw.Self.UserID, 10)]; ok && !st.Self.Tagged {
		st.Owner = u.LoginName
	}
	for _, p := range raw.Peer {
		st.Peers = append(st.Peers, node(p))
	}
	return st, nil
}

// Owned reports whether n belongs to the same user as this machine and is
// not a tagged device. Only such devices may connect in either direction.
func (st *Status) Owned(n Node) bool {
	return !n.Tagged && !st.Self.Tagged && n.UserID == st.Self.UserID
}

// ByIP returns the node, this machine included, that has address ip.
func (st *Status) ByIP(ip string) (Node, bool) {
	for _, n := range append([]Node{st.Self}, st.Peers...) {
		for _, a := range n.IPs {
			if a == ip {
				return n, true
			}
		}
	}
	return Node{}, false
}

// Cache keeps the last status for a short while: identity checks run on every
// peer connection and must not spawn the CLI each time.
type Cache struct {
	mu   sync.Mutex
	st   *Status
	err  error
	at   time.Time
	ttl  time.Duration
	busy chan struct{}
	// fixed caches never read the status again.
	fixed bool
}

// NewCache returns a cache whose entries live for ttl.
func NewCache(ttl time.Duration) *Cache { return &Cache{ttl: ttl} }

// Preloaded returns a cache that always answers st, for callers (and tests)
// that already hold a status.
func Preloaded(st *Status) *Cache {
	return &Cache{st: st, at: time.Now(), fixed: true}
}

// Get returns a status no older than the cache's ttl.
func (c *Cache) Get(ctx context.Context) (*Status, error) {
	return c.get(ctx, c.ttl)
}

// Fresh returns a status read now, unless one was read in the last second.
func (c *Cache) Fresh(ctx context.Context) (*Status, error) {
	return c.get(ctx, time.Second)
}

func (c *Cache) get(ctx context.Context, maxAge time.Duration) (*Status, error) {
	for {
		c.mu.Lock()
		if c.fixed {
			defer c.mu.Unlock()
			return c.st, c.err
		}
		age := maxAge
		if c.err != nil {
			// A failed read (a CLI timeout, Tailscale restarting) is retried
			// soon instead of refusing every caller for the full lifetime.
			age = min(age, 2*time.Second)
		}
		if !c.at.IsZero() && time.Since(c.at) < age {
			st, err := c.st, c.err
			c.mu.Unlock()
			return st, err
		}
		if c.busy == nil {
			// One fetch at a time, detached from any caller: a cancelled
			// caller must not leave an error cached for everyone else.
			c.busy = make(chan struct{})
			go func(done chan struct{}) {
				st, err := Fetch(context.Background())
				c.mu.Lock()
				c.st, c.err, c.at, c.busy = st, err, time.Now(), nil
				c.mu.Unlock()
				close(done)
			}(c.busy)
		}
		wait := c.busy
		c.mu.Unlock()
		// Each caller waits no longer than its own deadline allows.
		select {
		case <-wait:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

// ServeEntry is how Tailscale Serve uses one port of this machine.
type ServeEntry struct {
	Served bool
	// Target is the backend URL when the whole port goes to one backend;
	// empty for anything else (TCP forwarding, several paths).
	Target string
	HTTPS  bool
}

// ServeTarget reads how Tailscale Serve uses port on this machine.
func ServeTarget(ctx context.Context, dnsName string, port int) (ServeEntry, error) {
	out, err := run(ctx, "serve", "status", "--json")
	if err != nil {
		return ServeEntry{}, err
	}
	return parseServe(out, dnsName, port)
}

// parseServe reads the output of `tailscale serve status --json` for port.
func parseServe(out []byte, dnsName string, port int) (ServeEntry, error) {
	var cfg struct {
		TCP map[string]struct {
			HTTP, HTTPS bool
			TCPForward  string
		}
		Web map[string]struct {
			Handlers map[string]struct{ Proxy string }
		}
	}
	if len(bytes.TrimSpace(out)) > 0 {
		if err := json.Unmarshal(out, &cfg); err != nil {
			return ServeEntry{}, fmt.Errorf("tailscale serve status: %w", err)
		}
	}
	p := strconv.Itoa(port)
	tcp, ok := cfg.TCP[p]
	if !ok {
		return ServeEntry{}, nil
	}
	e := ServeEntry{Served: true, HTTPS: tcp.HTTPS}
	web := cfg.Web[dnsName+":"+p]
	if len(web.Handlers) == 1 && tcp.TCPForward == "" {
		if h, ok := web.Handlers["/"]; ok {
			e.Target = h.Proxy
		}
	}
	return e, nil
}

// Serve makes Tailscale Serve proxy HTTPS on port of the tailnet to target,
// with this machine's certificate for its tailnet name, so clients can tell
// it apart from anything else answering on that address.
// --yes skips confirmation prompts; a CLI without that flag runs without it.
func Serve(ctx context.Context, port int, target string) error {
	_, err := run(ctx, "serve", "--bg", "--yes", "--https="+strconv.Itoa(port), target)
	if err != nil && strings.Contains(err.Error(), "flag provided but not defined: -yes") {
		_, err = run(ctx, "serve", "--bg", "--https="+strconv.Itoa(port), target)
	}
	return err
}

// Unserve removes the entry for port.
func Unserve(ctx context.Context, port int, https bool) error {
	mode := "--http="
	if https {
		mode = "--https="
	}
	_, err := run(ctx, "serve", mode+strconv.Itoa(port), "off")
	return err
}
