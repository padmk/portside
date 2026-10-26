package supervisor

import (
	"net"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/padmk/portside/internal/config"
	"github.com/padmk/portside/internal/paths"
	"github.com/padmk/portside/internal/ports"
)

// View is the public, JSON-serialisable state of one server.
type View struct {
	Name           string          `json:"name"`
	Group          string          `json:"group,omitempty"`
	Dir            string          `json:"dir"`
	DirDisplay     string          `json:"dirDisplay"`
	Cmd            string          `json:"cmd"`
	Port           int             `json:"port"`
	PortAuto       bool            `json:"portAuto,omitempty"`
	URL            string          `json:"url,omitempty"`
	Alias          string          `json:"alias,omitempty"`
	Status         Status          `json:"status"`
	Note           string          `json:"note,omitempty"`
	PID            int             `json:"pid,omitempty"`
	StartedAt      *time.Time      `json:"startedAt,omitempty"`
	ReadyAt        *time.Time      `json:"readyAt,omitempty"`
	ExitedAt       *time.Time      `json:"exitedAt,omitempty"`
	ExitCode       *int            `json:"exitCode,omitempty"`
	RestartPending bool            `json:"restartPending"`
	ListenPorts    []int           `json:"listenPorts,omitempty"`
	Listening      bool            `json:"listening"`
	External       *ports.Listener `json:"external,omitempty"`
	Stale          bool            `json:"stale,omitempty"`
	Removed        bool            `json:"removed,omitempty"`
	Enabled        bool            `json:"enabled"`
	DependsOn      []string        `json:"dependsOn,omitempty"`
	Restart        string          `json:"restart"`
	Restarts       int             `json:"restarts,omitempty"`
	CPU            float64         `json:"cpu"`
	Mem            int64           `json:"mem"`
	CPUHistory     []float64       `json:"cpuHistory,omitempty"`
	MemHistory     []int64         `json:"memHistory,omitempty"`
	HasIcon        bool            `json:"hasIcon"`
	Tail           []string        `json:"tail"` // last few plain output lines
	Wanted         bool            `json:"wanted"`
	// Tool is the environment tool the live process runs under, such as
	// direnv or mise ("custom" for a configured wrap).
	Tool string `json:"tool,omitempty"`

	// Temporary marks a copy of the configured server Origin that runs in
	// the checkout at Worktree; it is not in the config file.
	Temporary bool   `json:"temporary,omitempty"`
	Origin    string `json:"origin,omitempty"`
	Worktree  string `json:"worktree,omitempty"`

	// ForwardedFrom names the connected computer whose server answers on
	// this stopped server's port, forwarded here.
	ForwardedFrom string `json:"forwardedFrom,omitempty"`

	// Exposure is where the running server's listeners can be reached from
	// (loopback, tailnet or network); absent until read. NetworkWarning
	// marks one reachable from the network whose config does not set
	// `lan = true`, which LAN reports. Kind names the tool the command
	// starts (see discover.CommandKind) while NetworkWarning is set, for the
	// dashboard's fix.
	Exposure       ports.Exposure `json:"exposure,omitempty"`
	NetworkWarning bool           `json:"networkWarning,omitempty"`
	LAN            bool           `json:"lan,omitempty"`
	Kind           string         `json:"kind,omitempty"`
}

// Health is whether a server should run and whether it is ready, the rule
// behind monitoring checks. A server should run when it crashed, when it
// exited although its restart policy is always (the policy gave up), and
// while it is wanted (started and not stopped by the user). Reason explains
// a server that should run and is not ready: its note, else its status.
type Health struct {
	Expected bool
	Ready    bool
	Reason   string
}

// Degraded reports a server that should run and is not ready.
func (h Health) Degraded() bool { return h.Expected && !h.Ready }

// Health classifies the server for monitoring; a required server should
// run whatever its state, also after a stop.
func (v View) Health(required bool) Health {
	h := Health{
		Expected: required || v.Wanted || v.Status == Crashed || (v.Status == Exited && v.Restart == config.RestartAlways),
		Ready:    v.Status == Running,
	}
	if h.Degraded() {
		h.Reason = v.Note
		if h.Reason == "" {
			h.Reason = string(v.Status)
		}
	}
	return h
}

// Snapshot returns all servers in configuration order. daemonPort is used to
// build *.localhost alias URLs served by the daemon's proxy.
func (s *Supervisor) Snapshot(daemonPort int) []View {
	base, volatile := s.env.cached()
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]View, 0, len(s.order))
	for _, name := range s.order {
		p := s.procs[name]
		if p == nil {
			continue
		}
		c := p.cfg
		// A live process keeps the port and path it was started with until a
		// restart applies an edit (reported as Stale).
		active := c
		if p.pid != 0 {
			active = p.running
		}
		v := View{
			Name:           name,
			Group:          c.Group,
			Dir:            c.Dir,
			DirDisplay:     paths.ShortenHome(c.Dir),
			Cmd:            c.Cmd,
			Port:           active.Port,
			PortAuto:       c.PortAuto,
			Status:         p.status,
			Note:           p.note,
			PID:            p.pid,
			Listening:      p.listening,
			RestartPending: p.restartPending,
			ListenPorts:    append([]int(nil), p.listenPorts...),
			External:       p.external,
			Stale:          p.stale(s.cfg, base, volatile),
			Removed:        p.removed,
			Enabled:        c.Enabled,
			DependsOn:      c.DependsOn,
			Restart:        c.Restart,
			Restarts:       p.retries,
			CPU:            p.cpu,
			Mem:            p.mem,
			Wanted:         p.want,
			Tool:           p.runningTool,
			HasIcon:        IconPath(c.Dir, c.Icon) != "",
			Tail:           p.logs.Tail(6, 240),
			LAN:            c.LAN,
		}
		v.ForwardedFrom = s.forwardedFromLocked(p)
		if p.temp != nil {
			v.Temporary, v.Origin, v.Worktree = true, p.temp.Origin, p.temp.Worktree
		}
		if p.pid != 0 {
			v.Exposure = p.exposure
			v.NetworkWarning = NetworkWarning(p.exposure, c.LAN)
			if v.NetworkWarning {
				v.Kind = p.kind
			}
			v.CPUHistory = append([]float64(nil), p.hist.cpu...)
			v.MemHistory = append([]int64(nil), p.hist.mem...)
		}
		if active.Port > 0 {
			v.URL = "http://" + net.JoinHostPort(loopbackHost(p.listenHost), strconv.Itoa(active.Port)) + c.Open
			v.Alias = "http://" + name + ".localhost"
			if daemonPort != 80 {
				v.Alias += ":" + strconv.Itoa(daemonPort)
			}
			v.Alias += c.Open
		}
		if !p.startedAt.IsZero() && p.pid != 0 {
			t := p.startedAt
			v.StartedAt = &t
		}
		if !p.readyAt.IsZero() && p.pid != 0 {
			t := p.readyAt
			v.ReadyAt = &t
		}
		if p.hasExit && p.pid == 0 {
			t, code := p.exitedAt, p.exitCode
			v.ExitedAt, v.ExitCode = &t, &code
		}
		out = append(out, v)
	}
	return out
}

// iconCandidates are common locations of a project's favicon or logo.
var iconCandidates = []string{
	"public/favicon.svg", "public/icon.svg", "public/logo.svg",
	"public/favicon.png", "public/icon.png", "public/apple-touch-icon.png",
	"public/favicon.ico",
	"public/icons/favicon.svg", "public/icons/favicon-32.png", "public/icons/icon-192.png",
	"app/icon.svg", "app/icon.png", "app/favicon.ico",
	"src/app/icon.svg", "src/app/icon.png", "src/app/favicon.ico",
	"static/favicon.svg", "static/favicon.png", "static/favicon.ico",
	"src/assets/logo.svg", "src/assets/images/logo.png",
	"apps/web/public/favicon.svg", "apps/web/public/favicon.ico",
	"apps/web/src/app/icon.svg", "apps/web/app/favicon.ico",
	"web/public/favicon.svg", "web/public/favicon.ico",
	"frontend/public/favicon.svg", "frontend/public/favicon.ico",
}

type iconEntry struct {
	path string
	at   time.Time
}

var iconCache sync.Map // dir+"\x00"+explicit -> iconEntry

// IconPath returns the icon file for a server directory, preferring an
// explicitly configured one. Results are cached for a minute.
func IconPath(dir, explicit string) string {
	key := dir + "\x00" + explicit
	if e, ok := iconCache.Load(key); ok && time.Since(e.(iconEntry).at) < time.Minute {
		return e.(iconEntry).path
	}
	p := findIcon(dir, explicit)
	iconCache.Store(key, iconEntry{p, time.Now()})
	return p
}

func findIcon(dir, explicit string) string {
	if explicit != "" {
		if st, err := os.Stat(explicit); err == nil && !st.IsDir() {
			return explicit
		}
		return ""
	}
	for _, rel := range iconCandidates {
		p := filepath.Join(dir, rel)
		if st, err := os.Stat(p); err == nil && !st.IsDir() && st.Size() < 2<<20 {
			return p
		}
	}
	return ""
}
