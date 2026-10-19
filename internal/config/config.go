// Package config loads and edits the TOML file that lists managed servers.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/BurntSushi/toml"

	"github.com/padmk/portside/internal/app"
	"github.com/padmk/portside/internal/paths"
)

// AutoPortBase is the first port handed out to servers without an explicit port.
// It is 3001 rather than 3000 because 3000 is the most contested default.
const AutoPortBase = 3001

// Reserved ports are skipped by automatic assignment: 5000/7000 belong to the
// macOS AirPlay Receiver, the rest are on browsers' unsafe-port lists.
var Reserved = map[int]bool{
	3659: true, 4045: true, 5000: true, 5060: true, 5061: true, 6000: true,
	6566: true, 6665: true, 6666: true, 6667: true, 6668: true, 6669: true,
	6697: true, 7000: true, 10080: true,
}

var nameRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)
var projectRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

// dashboardLabelRe matches the label of the dashboard's own host,
// <label>.localhost, which the daemon picks at random once per state
// directory. No server may be called that, so the proxy never shadows it.
var dashboardLabelRe = regexp.MustCompile(`^d-[0-9a-f]{24}$`)

// IsDashboardLabel reports whether label has the form of a dashboard host label.
func IsDashboardLabel(label string) bool { return dashboardLabelRe.MatchString(label) }

// ValidName reports whether name can be used as a server name. Names double as
// DNS labels for the <name>.localhost proxy, hence the strict alphabet.
func ValidName(name string) error {
	if name == app.ID || IsDashboardLabel(name) {
		return fmt.Errorf("name %q is reserved for the dashboard", name)
	}
	if !nameRe.MatchString(name) {
		return fmt.Errorf("invalid name %q: use lowercase letters, digits and dashes", name)
	}
	return nil
}

// Duration is a time.Duration written as a string such as "10s" in TOML.
type Duration struct{ time.Duration }

func (d *Duration) UnmarshalText(b []byte) error {
	v, err := time.ParseDuration(string(b))
	if err != nil {
		return err
	}
	d.Duration = v
	return nil
}

// Restart policies.
const (
	RestartNo        = "no"
	RestartOnFailure = "on-failure"
	RestartAlways    = "always"

	// WrapNone turns off tool environment detection for a server.
	WrapNone = "none"
)

// Server is one managed dev server with defaults applied and paths resolved.
type Server struct {
	Name      string
	Dir       string // absolute
	DirRaw    string // as written in the file
	Cmd       string
	Port      int  // 0 means the server does not listen on a port
	PortAuto  bool // port was assigned automatically
	Group     string
	DependsOn []string
	Projects  []string
	Env       map[string]string
	Restart   string
	Health    string // optional HTTP path probed for readiness, e.g. "/"
	Icon      string // optional absolute icon path
	Open      string // optional path appended to the URL when opening
	// Wrap selects how the command enters the project's tool environment:
	// "" detects direnv, devbox, mise or a Node version file, WrapNone runs
	// it as is, anything else is a command prefix.
	Wrap    string
	Stop    time.Duration
	StopRaw string // stop_timeout as written, "" when inherited
	IconRaw string // icon as written
	Enabled bool
}

// Config is the parsed configuration file.
type Config struct {
	Path string
	Root string
	Port int
	// PortAuto is true when the file sets no dashboard port, so the daemon
	// may choose and save another one when the default is taken.
	PortAuto    bool
	Shell       string
	PathPrepend []string
	Env         map[string]string
	Restore     bool
	Notify      bool
	Editor      string
	Terminal    string
	StopTimeout time.Duration
	// DockerStart is the command that starts the container engine; empty
	// means the engine behind the active Docker context is detected.
	DockerStart string
	// Share lets the owner's other devices on Tailscale connect to this daemon.
	Share   bool
	Servers []Server
	// Warnings are non-fatal problems such as duplicate ports.
	Warnings []string
}

// Server returns the server with the given name.
func (c *Config) Server(name string) (Server, bool) {
	for _, s := range c.Servers {
		if s.Name == name {
			return s, true
		}
	}
	return Server{}, false
}

// Groups returns group names in first-appearance order.
func (c *Config) Groups() []string {
	seen := map[string]bool{}
	out := []string{}
	for _, s := range c.Servers {
		if s.Group != "" && !seen[s.Group] {
			seen[s.Group] = true
			out = append(out, s.Group)
		}
	}
	return out
}

// Resolve expands names and group names into server names, keeping config order.
func (c *Config) Resolve(targets []string) ([]string, error) {
	want := map[string]bool{}
	for _, t := range targets {
		found := false
		for _, s := range c.Servers {
			if s.Name == t || s.Group == t {
				want[s.Name] = true
				found = true
			}
		}
		if !found {
			return nil, fmt.Errorf("unknown server or group %q", t)
		}
	}
	var out []string
	for _, s := range c.Servers {
		if want[s.Name] {
			out = append(out, s.Name)
		}
	}
	return out, nil
}

type rawServer struct {
	Dir         string            `toml:"dir"`
	Cmd         string            `toml:"cmd"`
	Port        *int              `toml:"port"`
	Group       string            `toml:"group"`
	DependsOn   []string          `toml:"depends_on"`
	Projects    []string          `toml:"projects"`
	Env         map[string]string `toml:"env"`
	Restart     string            `toml:"restart"`
	Health      string            `toml:"health"`
	Icon        string            `toml:"icon"`
	Open        string            `toml:"open"`
	Wrap        string            `toml:"wrap"`
	StopTimeout *Duration         `toml:"stop_timeout"`
	Enabled     *bool             `toml:"enabled"`
}

type rawConfig struct {
	Root        string               `toml:"root"`
	Port        int                  `toml:"port"`
	Shell       string               `toml:"shell"`
	Path        []string             `toml:"path"`
	Env         map[string]string    `toml:"env"`
	Restore     *bool                `toml:"restore"`
	Notify      *bool                `toml:"notify"`
	Editor      string               `toml:"editor"`
	Terminal    string               `toml:"terminal"`
	StopTimeout *Duration            `toml:"stop_timeout"`
	DockerStart string               `toml:"docker_start"`
	Share       bool                 `toml:"share"`
	Servers     map[string]rawServer `toml:"servers"`
}

// ErrNotFound is returned when the configuration file does not exist.
var ErrNotFound = errors.New("config file not found")

// Load reads and validates the configuration at path.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("%w: %s (run `%s init`)", ErrNotFound, path, app.ID)
	}
	if err != nil {
		return nil, err
	}
	return Parse(path, data)
}

// Parse validates configuration text. path is used for messages and relative paths.
func Parse(path string, data []byte) (*Config, error) { return parse(path, data, nil) }

// parse is Parse with automatic ports that also skip the ports avoid reports.
func parse(path string, data []byte, avoid func(port int) bool) (*Config, error) {
	var raw rawConfig
	meta, err := toml.Decode(string(data), &raw)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	cfg := &Config{
		Path:        path,
		Root:        paths.ExpandHome(raw.Root),
		Port:        raw.Port,
		Shell:       raw.Shell,
		Env:         raw.Env,
		Restore:     raw.Restore == nil || *raw.Restore,
		Notify:      raw.Notify == nil || *raw.Notify,
		Editor:      strings.TrimSpace(raw.Editor),
		Terminal:    strings.TrimSpace(raw.Terminal),
		StopTimeout: 10 * time.Second,
		DockerStart: strings.TrimSpace(raw.DockerStart),
		Share:       raw.Share,
	}
	if cfg.Root == "" {
		cfg.Root = filepath.Dir(path)
	} else if !filepath.IsAbs(cfg.Root) {
		cfg.Root = filepath.Join(filepath.Dir(path), cfg.Root)
	}
	if cfg.Port == 0 {
		cfg.Port = app.DefaultPort
		cfg.PortAuto = true
	}
	if cfg.Shell == "" {
		cfg.Shell = "bash"
	}
	if raw.StopTimeout != nil {
		cfg.StopTimeout = raw.StopTimeout.Duration
	}
	for _, p := range raw.Path {
		cfg.PathPrepend = append(cfg.PathPrepend, paths.ExpandHome(p))
	}
	if undecoded := meta.Undecoded(); len(undecoded) > 0 {
		var keys []string
		for _, k := range undecoded {
			if len(k) >= 3 && k[0] == "servers" && k[2] == "env" {
				continue // env maps are free-form
			}
			keys = append(keys, k.String())
		}
		if len(keys) > 0 {
			cfg.Warnings = append(cfg.Warnings, "unknown keys ignored: "+strings.Join(keys, ", "))
		}
	}

	// Keep servers in file order: map iteration order is random.
	var order []string
	seen := map[string]bool{}
	for _, k := range meta.Keys() {
		if len(k) >= 2 && k[0] == "servers" && !seen[k[1]] {
			seen[k[1]] = true
			order = append(order, k[1])
		}
	}

	explicit := map[int]string{}
	for _, name := range order {
		if name == app.ID {
			cfg.Warnings = append(cfg.Warnings, fmt.Sprintf("%s: name is reserved for the dashboard; rename this server to use its alias", name))
		} else if err := ValidName(name); err != nil {
			return nil, fmt.Errorf("%s: [servers.%s]: %w", path, name, err)
		}
		r := raw.Servers[name]
		if strings.TrimSpace(r.Cmd) == "" {
			return nil, fmt.Errorf("%s: [servers.%s]: cmd is required", path, name)
		}
		s := Server{
			Name:      name,
			DirRaw:    r.Dir,
			Cmd:       r.Cmd,
			Group:     r.Group,
			DependsOn: r.DependsOn,
			Projects:  r.Projects,
			Env:       r.Env,
			Restart:   r.Restart,
			Health:    r.Health,
			Open:      r.Open,
			Wrap:      strings.TrimSpace(r.Wrap),
			Stop:      cfg.StopTimeout,
			Enabled:   r.Enabled == nil || *r.Enabled,
		}
		projects := map[string]bool{}
		for _, project := range s.Projects {
			if !projectRe.MatchString(project) || projects[project] {
				return nil, fmt.Errorf("%s: [servers.%s]: projects must contain unique compose project names (lowercase letters, digits, dashes and underscores)", path, name)
			}
			projects[project] = true
		}
		s.Dir = paths.ExpandHome(r.Dir)
		if s.Dir == "" {
			s.Dir = cfg.Root
		} else if !filepath.IsAbs(s.Dir) {
			s.Dir = filepath.Join(cfg.Root, s.Dir)
		}
		s.IconRaw = r.Icon
		if r.Icon != "" {
			s.Icon = paths.ExpandHome(r.Icon)
			if !filepath.IsAbs(s.Icon) {
				s.Icon = filepath.Join(s.Dir, s.Icon)
			}
		}
		if r.StopTimeout != nil {
			s.Stop = r.StopTimeout.Duration
			s.StopRaw = r.StopTimeout.Duration.String()
		}
		switch s.Restart {
		case "":
			s.Restart = RestartNo
		case RestartNo, RestartOnFailure, RestartAlways:
		default:
			return nil, fmt.Errorf("%s: [servers.%s]: restart must be no, on-failure or always", path, name)
		}
		if r.Port != nil {
			s.Port = *r.Port
			if s.Port < 0 || s.Port > 65535 {
				return nil, fmt.Errorf("%s: [servers.%s]: port %d out of range", path, name, s.Port)
			}
			if s.Port > 0 {
				if other, dup := explicit[s.Port]; dup {
					cfg.Warnings = append(cfg.Warnings, fmt.Sprintf("port %d is used by both %s and %s", s.Port, other, name))
				}
				explicit[s.Port] = name
				if Reserved[s.Port] {
					cfg.Warnings = append(cfg.Warnings, fmt.Sprintf("%s: port %d is reserved by the OS or blocked by browsers", name, s.Port))
				}
				if s.Port == cfg.Port {
					return nil, fmt.Errorf("%s: [servers.%s]: port %d is the daemon's own port", path, name, s.Port)
				}
			}
		} else {
			s.PortAuto = true
		}
		cfg.Servers = append(cfg.Servers, s)
	}

	// Automatic ports: lowest free port from AutoPortBase, in file order.
	next := AutoPortBase
	for i := range cfg.Servers {
		if !cfg.Servers[i].PortAuto {
			continue
		}
		for Reserved[next] || explicit[next] != "" || next == cfg.Port || (avoid != nil && avoid(next)) {
			next++
		}
		cfg.Servers[i].Port = next
		explicit[next] = cfg.Servers[i].Name
		next++
	}

	if err := checkDeps(cfg); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return cfg, nil
}

func checkDeps(cfg *Config) error {
	byName := map[string]Server{}
	for _, s := range cfg.Servers {
		byName[s.Name] = s
	}
	for _, s := range cfg.Servers {
		for _, d := range s.DependsOn {
			if _, ok := byName[d]; !ok {
				return fmt.Errorf("[servers.%s]: depends_on refers to unknown server %q", s.Name, d)
			}
		}
	}
	// Detect cycles with a DFS.
	state := map[string]int{} // 0 new, 1 visiting, 2 done
	var visit func(string) error
	visit = func(n string) error {
		switch state[n] {
		case 1:
			return fmt.Errorf("dependency cycle through %q", n)
		case 2:
			return nil
		}
		state[n] = 1
		for _, d := range byName[n].DependsOn {
			if err := visit(d); err != nil {
				return err
			}
		}
		state[n] = 2
		return nil
	}
	names := make([]string, 0, len(byName))
	for n := range byName {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		if err := visit(n); err != nil {
			return err
		}
	}
	return nil
}

// WithDeps returns names plus all their transitive dependencies, ordered so
// that every dependency comes before its dependents.
func (c *Config) WithDeps(names []string) []string {
	byName := map[string]Server{}
	for _, s := range c.Servers {
		byName[s.Name] = s
	}
	var out []string
	done := map[string]bool{}
	var visit func(string)
	visit = func(n string) {
		if done[n] {
			return
		}
		done[n] = true
		for _, d := range byName[n].DependsOn {
			visit(d)
		}
		out = append(out, n)
	}
	for _, n := range names {
		visit(n)
	}
	return out
}

// FreePort returns the lowest unused, unreserved port at or above AutoPortBase.
func (c *Config) FreePort(inUse func(int) bool) int {
	used := map[int]bool{c.Port: true}
	for _, s := range c.Servers {
		used[s.Port] = true
	}
	for p := AutoPortBase; p < 65535; p++ {
		if used[p] || Reserved[p] || (inUse != nil && inUse(p)) {
			continue
		}
		return p
	}
	return 0
}
