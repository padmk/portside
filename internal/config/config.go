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
	"github.com/padmk/portside/internal/signing"
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
// <label>.localhost, which the daemon picks at random on a cold start. No
// server may be called that, so the proxy never shadows it.
var dashboardLabelRe = regexp.MustCompile(`^d-[0-9a-f]{24}$`)

// IsDashboardLabel reports whether label has the form of a dashboard host label.
func IsDashboardLabel(label string) bool { return dashboardLabelRe.MatchString(label) }

// NewDashboardLabel returns a random label that IsDashboardLabel accepts.
func NewDashboardLabel() string { return "d-" + signing.RandomHex(12) }

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

// Duration is a time.Duration written as a string such as "10s" in TOML;
// Text keeps that string as written.
type Duration struct {
	time.Duration
	Text string
}

func (d *Duration) UnmarshalText(b []byte) error {
	v, err := time.ParseDuration(string(b))
	if err != nil {
		return err
	}
	d.Duration, d.Text = v, string(b)
	return nil
}

// durationText is a duration setting as written, "" when it is not set.
func durationText(d *Duration) string {
	if d == nil {
		return ""
	}
	return d.Text
}

// shortDuration writes d the way people do, "1m" rather than "1m0s".
func shortDuration(d time.Duration) string {
	s := d.String()
	if strings.HasSuffix(s, "m0s") {
		s = strings.TrimSuffix(s, "0s")
	}
	if strings.HasSuffix(s, "h0m") {
		s = strings.TrimSuffix(s, "0m")
	}
	return s
}

// Restart policies.
const (
	RestartNo        = "no"
	RestartOnFailure = "on-failure"
	RestartAlways    = "always"

	// WrapAuto (the default) detects a server's tool environment and
	// WrapNone turns detection off.
	WrapAuto = "auto"
	WrapNone = "none"
)

// DefaultRestart is the restart policy of a server that sets none: a server
// comes back after a crash, while a task's failed run, such as a migration
// that may not be safe to repeat, is reported at once.
func DefaultRestart(oneshot bool) string {
	if oneshot {
		return RestartNo
	}
	return RestartOnFailure
}

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
	// "" or WrapAuto detects direnv, devbox, mise or a Node version file, WrapNone runs
	// it as is, anything else is a command prefix.
	Wrap    string
	Stop    time.Duration
	IconRaw string // icon as written
	Enabled bool
	// LAN says the server is meant to be reachable from other devices, so
	// listening on the network is not pointed out.
	LAN bool
	// OnCrash is the shell command run once the server has crashed for good:
	// the server's own on_crash, else the top-level one; "" runs nothing.
	OnCrash string
	// ReadyLog, when set, is a regular expression a line of the server's
	// output must match before it counts as running (checked at load).
	ReadyLog string
	// ReadyAfter is how long the process must have run before it counts as
	// running; 0 means the default (immediately with a port, 1.5 s without).
	ReadyAfter time.Duration
	// OnUnready runs once a running server has not answered for
	// UnreadyAfter (0 means DefaultUnreadyAfter), and OnRecover once a server that OnCrash or OnUnready
	// reported is ready again; like OnCrash, the server's own or the
	// top-level one, "" for none.
	OnUnready    string
	OnRecover    string
	UnreadyAfter time.Duration
	// RetryAfter, when positive, starts a server that its restart policy
	// gave up on (or that could not start) again after that long, with a
	// fresh round of restarts. Only with on-failure or always.
	RetryAfter time.Duration
	// Umask, when set, is the file mode creation mask the command runs
	// with, as octal digits such as "0007"; "" keeps the daemon's.
	Umask string
	// Oneshot makes the server a task: its command runs to completion,
	// code 0 is success, and servers that depend on it wait for that
	// instead of for readiness. A task has no port.
	Oneshot bool
	// Schedule, when set, starts the server at the times it matches in
	// local time; nil for none.
	Schedule *Schedule
	// MemoryLimit, when above zero, is how much memory the server's process
	// tree may use: a usage sample above it stops the server as a crash.
	// The server's own memory_limit, else the top-level one.
	MemoryLimit Size
	// Watch, when set, holds file patterns relative to Dir whose changes
	// restart the server; nil for none.
	Watch *Watch
	// spec is the entry as written in the file, which SpecOf returns for edits.
	spec Spec
}

// RetriesAfterGivingUp reports whether s is started again after its
// automatic restarts gave up or its start failed.
func (s Server) RetriesAfterGivingUp() bool {
	return s.RetryAfter > 0 && s.Restart != RestartNo
}

// DefaultUnreadyAfter is how long a running server must go without
// answering before OnUnready runs, unless unready_after says otherwise.
const DefaultUnreadyAfter = time.Minute

// Defaults of the top-level stop_timeout and shell.
const (
	DefaultStopTimeout = 10 * time.Second
	DefaultShell       = "bash"
)

var umaskRe = regexp.MustCompile(`^0?[0-7]{3}$`)

// IsComposeCommand reports whether a server's command runs a compose stack.
func IsComposeCommand(cmd string) bool {
	c := strings.ToLower(cmd)
	for _, s := range []string{"docker compose", "docker-compose", "podman compose", "podman-compose"} {
		if strings.Contains(c, s) {
			return true
		}
	}
	return false
}

// ComposeStop is the stop timeout given to compose stacks found by discovery
// or imported, so containers can shut down cleanly.
const ComposeStop = "30s"

// Config is the parsed configuration file.
type Config struct {
	Path string
	Root string
	Port int
	// PortAuto is true when the file sets no dashboard port, so the daemon
	// may choose another one when the default is taken (kept in the state
	// directory, see daemon.SavedPort).
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
	Share bool
	// OwnEnv gives servers the daemon's own environment instead of the
	// account's login-shell environment (login_env = false).
	OwnEnv  bool
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
	LAN         bool              `toml:"lan"`
	OnCrash     *string           `toml:"on_crash"`
	ReadyLog    string            `toml:"ready_log"`
	ReadyAfter  *Duration         `toml:"ready_after"`
	OnUnready   *string           `toml:"on_unready"`
	OnRecover   *string           `toml:"on_recover"`
	UnreadyAft  *Duration         `toml:"unready_after"`
	RetryAfter  *Duration         `toml:"retry_after"`
	Umask       *string           `toml:"umask"`
	Oneshot     bool              `toml:"oneshot"`
	Schedule    string            `toml:"schedule"`
	MemoryLimit *string           `toml:"memory_limit"`
	Watch       []string          `toml:"watch"`
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
	OnCrash     string               `toml:"on_crash"`
	OnUnready   string               `toml:"on_unready"`
	OnRecover   string               `toml:"on_recover"`
	UnreadyAft  *Duration            `toml:"unready_after"`
	RetryAfter  *Duration            `toml:"retry_after"`
	Umask       string               `toml:"umask"`
	LoginEnv    *bool                `toml:"login_env"`
	MemoryLimit *string              `toml:"memory_limit"`
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

// dashboardPort returns the dashboard port for a configured value: the default when unset,
// an error when outside 1-65535.
func dashboardPort(port int) (int, error) {
	if port == 0 {
		return app.DefaultPort, nil
	}
	if port < 1 || port > 65535 {
		return 0, fmt.Errorf("dashboard port %d out of range", port)
	}
	return port, nil
}

// caseDuplicates refuses two settings whose keys differ only in case, such
// as cmd and Cmd in one server's table: the decoder sets the same field from
// both, so which one wins is not defined. Keys of maps such as env keep
// their case and may differ in it.
func caseDuplicates(meta toml.MetaData) error {
	seen := map[string]string{}
	for _, k := range meta.Keys() {
		if len(k) != 1 && (len(k) != 3 || k[0] != "servers") {
			continue
		}
		id := strings.Join(k[:len(k)-1], "\x00") + "\x00" + strings.ToLower(k[len(k)-1])
		if prev, ok := seen[id]; ok {
			return fmt.Errorf("%s and %s are the same setting; keep one", prev, k)
		}
		seen[id] = k.String()
	}
	return nil
}

// parse is Parse with automatic ports that also skip the ports avoid reports.
func parse(path string, data []byte, avoid func(port int) bool) (*Config, error) {
	var raw rawConfig
	meta, err := toml.Decode(string(data), &raw)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if err := caseDuplicates(meta); err != nil {
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
		StopTimeout: DefaultStopTimeout,
		DockerStart: strings.TrimSpace(raw.DockerStart),
		Share:       raw.Share,
		OwnEnv:      raw.LoginEnv != nil && !*raw.LoginEnv,
	}
	if cfg.Root == "" {
		cfg.Root = filepath.Dir(path)
	} else if !filepath.IsAbs(cfg.Root) {
		cfg.Root = filepath.Join(filepath.Dir(path), cfg.Root)
	}
	port, err := dashboardPort(raw.Port)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	cfg.Port, cfg.PortAuto = port, raw.Port == 0
	if cfg.Shell == "" {
		cfg.Shell = DefaultShell
	}
	if raw.StopTimeout != nil {
		cfg.StopTimeout = raw.StopTimeout.Duration
	}
	for _, p := range raw.Path {
		cfg.PathPrepend = append(cfg.PathPrepend, paths.ExpandHome(p))
	}
	unreadyAfter, err := positive("unready_after", raw.UnreadyAft, DefaultUnreadyAfter)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	retryAfter, err := nonNegative("retry_after", raw.RetryAfter, 0)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	umask, err := checkUmask(raw.Umask)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	memLimit, err := memoryLimit(raw.MemoryLimit, Size{})
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
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
			LAN:       r.LAN,
			OnCrash:   orGlobal(r.OnCrash, raw.OnCrash),
			OnUnready: orGlobal(r.OnUnready, raw.OnUnready),
			OnRecover: orGlobal(r.OnRecover, raw.OnRecover),
			Oneshot:   r.Oneshot,
		}
		if s.UnreadyAfter, err = positive("unready_after", r.UnreadyAft, unreadyAfter); err != nil {
			return nil, fmt.Errorf("%s: [servers.%s]: %w", path, name, err)
		}
		if s.RetryAfter, err = nonNegative("retry_after", r.RetryAfter, retryAfter); err != nil {
			return nil, fmt.Errorf("%s: [servers.%s]: %w", path, name, err)
		}
		if s.MemoryLimit, err = memoryLimit(r.MemoryLimit, memLimit); err != nil {
			return nil, fmt.Errorf("%s: [servers.%s]: %w", path, name, err)
		}
		if r.MemoryLimit != nil && s.MemoryLimit.Bytes > 0 && IsComposeCommand(s.Cmd) {
			cfg.Warnings = append(cfg.Warnings, fmt.Sprintf("%s: memory_limit covers only the compose command's own processes, not its containers; set memory limits in the compose file", name))
		}
		s.Umask = umask
		if r.Umask != nil {
			if s.Umask, err = checkUmask(*r.Umask); err != nil {
				return nil, fmt.Errorf("%s: [servers.%s]: %w", path, name, err)
			}
		}
		if strings.TrimSpace(r.Schedule) != "" {
			if s.Schedule, err = ParseSchedule(r.Schedule); err != nil {
				return nil, fmt.Errorf("%s: [servers.%s]: schedule %q: %w", path, name, r.Schedule, err)
			}
		}
		if s.Watch, err = ParseWatch(r.Watch); err != nil {
			return nil, fmt.Errorf("%s: [servers.%s]: %w", path, name, err)
		}
		if r.ReadyLog != "" {
			if _, err := regexp.Compile(r.ReadyLog); err != nil {
				return nil, fmt.Errorf("%s: [servers.%s]: ready_log: %w", path, name, err)
			}
			s.ReadyLog = r.ReadyLog
		}
		if r.ReadyAfter != nil {
			if r.ReadyAfter.Duration < 0 {
				return nil, fmt.Errorf("%s: [servers.%s]: ready_after must not be negative", path, name)
			}
			s.ReadyAfter = r.ReadyAfter.Duration
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
		}
		switch s.Restart {
		case "":
			s.Restart = DefaultRestart(r.Oneshot)
		case RestartNo, RestartOnFailure, RestartAlways:
		default:
			return nil, fmt.Errorf("%s: [servers.%s]: restart must be no, on-failure or always", path, name)
		}
		if err := checkOneshot(r, s); err != nil {
			return nil, fmt.Errorf("%s: [servers.%s]: %w", path, name, err)
		}
		if s.Restart == RestartNo && r.RetryAfter != nil && s.RetryAfter > 0 {
			cfg.Warnings = append(cfg.Warnings, fmt.Sprintf("%s: retry_after has no effect with restart = %q", name, RestartNo))
		}
		s.spec = writtenSpec(name, r, s.Restart)
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
			s.PortAuto = !s.Oneshot
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

// checkOneshot refuses settings a task (oneshot = true) cannot use: it has
// no port, and it is done when it exits with code 0, not when it answers.
func checkOneshot(r rawServer, s Server) error {
	if !s.Oneshot {
		return nil
	}
	switch {
	case r.Port != nil && *r.Port != 0:
		return errors.New("a oneshot task has no port; remove port")
	case r.Health != "":
		return errors.New("health does not apply to oneshot = true: a task is done when it exits with code 0")
	case r.ReadyLog != "":
		return errors.New("ready_log does not apply to oneshot = true: a task is done when it exits with code 0")
	case r.ReadyAfter != nil:
		return errors.New("ready_after does not apply to oneshot = true: a task is done when it exits with code 0")
	case s.Restart == RestartAlways:
		return errors.New(`restart = "always" does not apply to oneshot = true; use "on-failure"`)
	}
	return nil
}

// orGlobal is a server's hook command: its own when set, even to "", else
// the top-level one.
func orGlobal(own *string, global string) string {
	if own != nil {
		return strings.TrimSpace(*own)
	}
	return strings.TrimSpace(global)
}

// nonNegative returns a duration setting, def when it is not set.
func nonNegative(key string, d *Duration, def time.Duration) (time.Duration, error) {
	if d == nil {
		return def, nil
	}
	if d.Duration < 0 {
		return 0, fmt.Errorf("%s must not be negative", key)
	}
	return d.Duration, nil
}

// positive returns a duration setting that must be above zero, def when it
// is not set.
func positive(key string, d *Duration, def time.Duration) (time.Duration, error) {
	if d != nil && d.Duration <= 0 {
		return 0, fmt.Errorf("%s must be positive", key)
	}
	return nonNegative(key, d, def)
}

// checkUmask accepts "" or three or four octal digits, such as "0007".
func checkUmask(v string) (string, error) {
	v = strings.TrimSpace(v)
	if v != "" && !umaskRe.MatchString(v) {
		return "", fmt.Errorf("umask must be octal digits such as \"0007\", not %q", v)
	}
	return v, nil
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
