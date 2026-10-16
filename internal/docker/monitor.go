package docker

import (
	"context"
	"errors"
	"io/fs"
	"net"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Compose labels identify a container's project, service and files.
const (
	labelProject     = "com.docker.compose.project"
	labelService     = "com.docker.compose.service"
	labelWorkingDir  = "com.docker.compose.project.working_dir"
	labelConfigFiles = "com.docker.compose.project.config_files"
	labelEnvFiles    = "com.docker.compose.project.environment_file"
	labelOneOff      = "com.docker.compose.oneoff"
)

// Engine states.
const (
	Connecting  = "connecting"
	Ready       = "ready"
	Unavailable = "unavailable"
)

// Port is a published container port.
type Port struct {
	Private int    `json:"private"`
	Public  int    `json:"public"`
	Proto   string `json:"proto"`
	IP      string `json:"ip,omitempty"`
}

// Container is the JSON view of one container.
type Container struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Image      string     `json:"image"`
	Project    string     `json:"project,omitempty"`
	Service    string     `json:"service,omitempty"`
	OneOff     bool       `json:"oneOff,omitempty"`
	WorkingDir string     `json:"workingDir,omitempty"`
	State      string     `json:"state"` // created, running, paused, restarting, removing, exited, dead
	Health     string     `json:"health,omitempty"`
	Status     string     `json:"status"` // the engine's own summary, e.g. "Up 3 hours (healthy)"
	StartedAt  *time.Time `json:"startedAt,omitempty"`
	FinishedAt *time.Time `json:"finishedAt,omitempty"`
	ExitCode   *int       `json:"exitCode,omitempty"`
	OOMKilled  bool       `json:"oomKilled,omitempty"`
	Restarts   int        `json:"restarts,omitempty"`
	Ports      []Port     `json:"ports,omitempty"`
	CPU        float64    `json:"cpu"`
	Mem        int64      `json:"mem"`
	MemLimit   int64      `json:"memLimit,omitempty"`
	CPUHistory []float64  `json:"cpuHistory,omitempty"`
	MemHistory []int64    `json:"memHistory,omitempty"`
	TTY        bool       `json:"-"`
	// Server is the configured server this container belongs to; the HTTP
	// layer fills it in.
	Server string   `json:"server,omitempty"`
	Job    *JobView `json:"job,omitempty"`

	configFiles []string
	envFiles    []string
}

// Compose reports whether the container belongs to a compose service, which
// can be recreated and rebuilt.
func (c *Container) Compose() bool {
	return c.Project != "" && c.Service != "" && c.WorkingDir != "" && !c.OneOff
}

// State is the JSON view of the engine connection and its containers.
type State struct {
	Status     string      `json:"status"`
	Error      string      `json:"error,omitempty"`
	Host       string      `json:"host,omitempty"`
	Context    string      `json:"context,omitempty"`
	Engine     string      `json:"engine"`
	Version    string      `json:"version,omitempty"`
	Starting   bool        `json:"starting,omitempty"`
	Containers []Container `json:"containers"`
}

// Host gives the monitor what it needs from the rest of the daemon: the
// user's login-shell environment and executable lookup on its PATH.
type Host interface {
	Environ() []string
	LookPath(name string) string
}

type entry struct {
	c        Container
	cpu      []float64
	mem      []int64
	sampled  time.Time
	cancel   context.CancelFunc // stops the stats stream
	tracking bool
}

// Monitor keeps a live picture of the engine: whether it is reachable, its
// containers, and their resource usage. Changes are pushed by the engine's
// event stream; a periodic resync covers anything missed.
type Monitor struct {
	host     Host
	interval time.Duration // spacing of history samples
	custom   func() string // the configured engine start command

	mu        sync.Mutex
	client    *Client
	status    string
	err       string
	ep        Endpoint
	version   string
	startedAt time.Time // when the user asked to start the engine
	startErr  string    // why the last start failed; outlives reconnect attempts
	entries   map[string]*entry
	sampled   bool            // usage samples arrived since the last change
	jobs      map[string]*job // by container name
	subs      map[chan struct{}]struct{}
	kick      chan struct{}
}

// NewMonitor returns a monitor; call Run to connect. custom returns the
// configured engine start command, "" for automatic detection.
func NewMonitor(host Host, interval time.Duration, custom func() string) *Monitor {
	return &Monitor{
		host:     host,
		interval: interval,
		custom:   custom,
		status:   Connecting,
		entries:  map[string]*entry{},
		jobs:     map[string]*job{},
		subs:     map[chan struct{}]struct{}{},
		kick:     make(chan struct{}, 1),
	}
}

// Subscribe returns a channel signalled on every change.
func (m *Monitor) Subscribe() chan struct{} {
	ch := make(chan struct{}, 1)
	m.mu.Lock()
	m.subs[ch] = struct{}{}
	m.mu.Unlock()
	return ch
}

// Unsubscribe stops signals to ch.
func (m *Monitor) Unsubscribe(ch chan struct{}) {
	m.mu.Lock()
	delete(m.subs, ch)
	m.mu.Unlock()
}

func (m *Monitor) changedLocked() {
	for ch := range m.subs {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

func (m *Monitor) getenv(key string) string {
	prefix := key + "="
	env := m.host.Environ()
	for i := len(env) - 1; i >= 0; i-- {
		if strings.HasPrefix(env[i], prefix) {
			return env[i][len(prefix):]
		}
	}
	return ""
}

// Snapshot returns the current state with containers sorted by project,
// service and name.
func (m *Monitor) Snapshot() State {
	m.mu.Lock()
	defer m.mu.Unlock()
	st := State{
		Status:   m.status,
		Error:    m.err,
		Host:     m.ep.Host,
		Context:  m.ep.Context,
		Engine:   engineName(m.ep),
		Version:  m.version,
		Starting: m.status != Ready && !m.startedAt.IsZero() && time.Since(m.startedAt) < startWait,
	}
	if m.startErr != "" && m.status != Ready {
		st.Error = m.startErr
	}
	st.Containers = make([]Container, 0, len(m.entries))
	for _, e := range m.entries {
		c := e.c
		if c.State == "running" {
			c.CPUHistory = append([]float64(nil), e.cpu...)
			c.MemHistory = append([]int64(nil), e.mem...)
		}
		if j := m.jobs[c.Name]; j != nil {
			v := j.view()
			c.Job = &v
		}
		st.Containers = append(st.Containers, c)
	}
	sort.Slice(st.Containers, func(i, j int) bool {
		a, b := st.Containers[i], st.Containers[j]
		if a.Project != b.Project {
			return a.Project < b.Project
		}
		if a.Service != b.Service {
			return a.Service < b.Service
		}
		return a.Name < b.Name
	})
	return st
}

// Find returns a container by name or ID.
func (m *Monitor) Find(ref string) (Container, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if e, ok := m.entries[ref]; ok {
		return e.c, true
	}
	for _, e := range m.entries {
		if e.c.Name == ref {
			return e.c, true
		}
	}
	return Container{}, false
}

// Client returns the connected client, or an error while the engine is unavailable.
func (m *Monitor) Client() (*Client, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.client == nil || m.status != Ready {
		return nil, errors.New("Docker is not running")
	}
	return m.client, nil
}

// Kick asks the monitor to reconnect or resync now.
func (m *Monitor) Kick() {
	select {
	case m.kick <- struct{}{}:
	default:
	}
}

// Run connects to the engine and follows it until ctx ends.
func (m *Monitor) Run(ctx context.Context) {
	backoff := time.Second
	for ctx.Err() == nil {
		ep, err := Resolve(m.getenv)
		var c *Client
		var version string
		if err == nil {
			c = NewClient(ep)
			pctx, cancel := context.WithTimeout(ctx, 3*time.Second)
			version, err = c.Ping(pctx)
			cancel()
		}
		if err != nil {
			if c != nil {
				c.Close()
			}
			m.setUnavailable(ep, err)
			m.expireStart()
			// Retry quickly while the user is starting the engine.
			wait := backoff
			if m.startRequested() {
				wait = time.Second
			} else if backoff < 15*time.Second {
				backoff *= 2
			}
			select {
			case <-ctx.Done():
				return
			case <-m.kick:
				backoff = time.Second
			case <-time.After(wait):
			}
			continue
		}
		backoff = time.Second
		m.mu.Lock()
		m.client, m.ep, m.version, m.status, m.err = c, ep, version, Ready, ""
		m.startedAt, m.startErr = time.Time{}, ""
		m.changedLocked()
		m.mu.Unlock()
		m.session(ctx, c)
		c.Close()
	}
	m.stopStats()
}

// expireStart ends the "starting" state once the engine has had its time.
func (m *Monitor) expireStart() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.startedAt.IsZero() && time.Since(m.startedAt) >= startWait {
		m.startedAt = time.Time{}
		m.changedLocked()
	}
}

func (m *Monitor) startRequested() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return !m.startedAt.IsZero() && time.Since(m.startedAt) < startWait
}

func (m *Monitor) setUnavailable(ep Endpoint, err error) {
	m.stopStats()
	m.mu.Lock()
	defer m.mu.Unlock()
	msg := describe(err)
	if m.status == Unavailable && m.err == msg && m.ep == ep && len(m.entries) == 0 {
		return
	}
	m.client, m.ep, m.status, m.err, m.version = nil, ep, Unavailable, msg, ""
	m.entries = map[string]*entry{}
	m.changedLocked()
}

// describe turns connection errors into a short reason.
func describe(err error) string {
	var ne *net.OpError
	switch {
	case errors.Is(err, fs.ErrNotExist), errors.Is(err, syscall.ECONNREFUSED), errors.Is(err, syscall.ENOENT):
		return "Docker is not running"
	case errors.Is(err, syscall.EACCES), errors.Is(err, os.ErrPermission):
		return "no permission to use the Docker socket"
	case errors.As(err, &ne):
		return "Docker is not running"
	}
	return err.Error()
}

// session follows one connection: events trigger resyncs until the stream
// breaks, which means the engine went away.
func (m *Monitor) session(ctx context.Context, c *Client) {
	sctx, cancel := context.WithCancel(ctx)
	defer cancel()
	defer m.stopStats()
	dirty := make(chan struct{}, 1)
	mark := func() {
		select {
		case dirty <- struct{}{}:
		default:
		}
	}
	go func() {
		defer cancel()
		_ = c.events(sctx, func(ev event) {
			switch {
			case strings.HasPrefix(ev.Action, "exec_"), ev.Action == "top", ev.Action == "attach", ev.Action == "resize":
				// Shells and inspections do not change what is shown.
			default:
				mark()
			}
		})
	}()
	mark()
	resync := time.NewTicker(30 * time.Second)
	defer resync.Stop()
	// Usage samples of all containers go out together, once per interval.
	samples := time.NewTicker(m.interval)
	defer samples.Stop()
	for {
		select {
		case <-sctx.Done():
			return
		case <-samples.C:
			m.mu.Lock()
			if m.sampled {
				m.sampled = false
				m.changedLocked()
			}
			m.mu.Unlock()
		case <-m.kick:
			if m.moved(c) {
				return
			}
			mark()
		case <-resync.C:
			if m.moved(c) {
				return
			}
			mark()
		case <-dirty:
			// Bursts of events (compose up) settle into one resync.
			select {
			case <-sctx.Done():
				return
			case <-time.After(150 * time.Millisecond):
			}
			m.refresh(sctx, c)
		}
	}
}

// moved reports whether the active Docker context now points at another
// engine than c, so the session must reconnect.
func (m *Monitor) moved(c *Client) bool {
	ep, err := Resolve(m.getenv)
	return err != nil || ep.Host != c.ep.Host
}

// refresh lists and inspects every container and starts or stops the stats
// streams to match.
func (m *Monitor) refresh(ctx context.Context, c *Client) {
	lctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	list, err := c.list(lctx)
	if err != nil {
		return
	}
	infos := make([]*Inspect, len(list))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 8)
	for i, it := range list {
		wg.Add(1)
		go func(i int, id string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			infos[i], _ = c.Inspect(lctx, id)
		}(i, it.ID)
	}
	wg.Wait()

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.client != c {
		return
	}
	seen := map[string]bool{}
	for i, it := range list {
		if infos[i] == nil {
			// Keep what is known until a later inspect succeeds.
			if m.entries[it.ID] != nil {
				seen[it.ID] = true
			}
			continue
		}
		next := build(it, infos[i])
		seen[next.ID] = true
		e := m.entries[next.ID]
		if e == nil {
			e = &entry{}
			m.entries[next.ID] = e
		}
		running := next.State == "running" || next.State == "paused"
		if running {
			next.CPU, next.Mem, next.MemLimit = e.c.CPU, e.c.Mem, e.c.MemLimit
		} else {
			e.cpu, e.mem = nil, nil
		}
		e.c = next
		if running && !e.tracking {
			sctx, scancel := context.WithCancel(ctx)
			e.cancel, e.tracking = scancel, true
			go m.follow(sctx, c, next.ID)
		} else if !running && e.tracking {
			e.cancel()
			e.tracking = false
		}
	}
	for id, e := range m.entries {
		if !seen[id] {
			if e.tracking {
				e.cancel()
			}
			delete(m.entries, id)
		}
	}
	m.changedLocked()
}

// follow records usage samples of one running container.
func (m *Monitor) follow(ctx context.Context, c *Client, id string) {
	began := time.Now()
	_ = c.stats(ctx, id, func(s *statsSample) {
		m.mu.Lock()
		defer m.mu.Unlock()
		e := m.entries[id]
		if e == nil {
			return
		}
		e.c.CPU, e.c.Mem, e.c.MemLimit = s.cpuPercent(), s.memBytes(), int64(s.Memory.Limit)
		if time.Since(e.sampled) >= m.interval-200*time.Millisecond {
			e.sampled = time.Now()
			e.cpu = appendRing(e.cpu, e.c.CPU)
			e.mem = appendRing(e.mem, e.c.Mem)
			m.sampled = true
		}
	})
	m.mu.Lock()
	ended := false
	if e := m.entries[id]; e != nil && ctx.Err() == nil {
		// The stream ended on its own: the container stopped or restarted.
		// A refresh starts a new one if it runs again.
		e.cancel()
		e.tracking = false
		e.c.CPU, e.c.Mem = 0, 0
		ended = true
	}
	m.mu.Unlock()
	// A stream that fails right away waits for the next resync instead.
	if ended && time.Since(began) > 2*time.Second {
		m.Kick()
	}
}

const historyLen = 60

func appendRing[T any](s []T, v T) []T {
	s = append(s, v)
	if len(s) > historyLen {
		s = s[len(s)-historyLen:]
	}
	return s
}

func (m *Monitor) stopStats() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, e := range m.entries {
		if e.tracking {
			e.cancel()
			e.tracking = false
		}
	}
}

func build(it apiContainer, in *Inspect) Container {
	labels := in.Config.Labels
	if labels == nil {
		labels = it.Labels
	}
	c := Container{
		ID:         in.ID,
		Name:       strings.TrimPrefix(in.Name, "/"),
		Image:      in.Config.Image,
		Project:    labels[labelProject],
		Service:    labels[labelService],
		OneOff:     strings.EqualFold(labels[labelOneOff], "true"),
		WorkingDir: labels[labelWorkingDir],
		State:      in.State.Status,
		Status:     it.Status,
		OOMKilled:  in.State.OOMKilled,
		Restarts:   in.RestartCount,
		TTY:        in.Config.Tty,
	}
	if c.Image == "" {
		c.Image = it.Image
	}
	// The last health result of a stopped container is stale.
	if in.State.Health != nil && in.State.Running {
		c.Health = in.State.Health.Status
	}
	if !in.State.StartedAt.IsZero() && in.State.StartedAt.Year() > 1 {
		t := in.State.StartedAt
		c.StartedAt = &t
	}
	if c.State == "exited" || c.State == "dead" {
		code := in.State.ExitCode
		c.ExitCode = &code
		if !in.State.FinishedAt.IsZero() && in.State.FinishedAt.Year() > 1 {
			t := in.State.FinishedAt
			c.FinishedAt = &t
		}
	}
	c.configFiles = splitList(labels[labelConfigFiles])
	c.envFiles = splitList(labels[labelEnvFiles])
	seen := map[string]bool{}
	for key, binds := range in.NetworkSettings.Ports {
		num, proto, _ := strings.Cut(key, "/")
		private, _ := strconv.Atoi(num)
		for _, b := range binds {
			public, _ := strconv.Atoi(b.HostPort)
			k := strconv.Itoa(public) + "/" + proto
			if public == 0 || seen[k] {
				continue
			}
			seen[k] = true
			c.Ports = append(c.Ports, Port{Private: private, Public: public, Proto: proto, IP: b.HostIP})
		}
	}
	sort.Slice(c.Ports, func(i, j int) bool { return c.Ports[i].Public < c.Ports[j].Public })
	return c
}

func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// engineName names the product behind an endpoint for messages.
func engineName(ep Endpoint) string {
	s := strings.ToLower(ep.Context + " " + ep.Host)
	switch {
	case strings.Contains(s, "orbstack"):
		return "OrbStack"
	case strings.Contains(s, "colima"):
		return "Colima"
	case strings.Contains(s, "rancher") || strings.Contains(s, "/.rd/"):
		return "Rancher Desktop"
	case strings.Contains(s, "desktop-linux") || strings.Contains(s, "/.docker/run/") || strings.Contains(s, "/.docker/desktop/"):
		return "Docker Desktop"
	}
	return "Docker"
}
