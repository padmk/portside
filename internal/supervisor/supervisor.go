// Package supervisor runs dev servers in pseudo-terminals and tracks their
// lifecycle: start (with dependencies), readiness, graceful stop, crash
// handling with restart policies, resource usage and scrollback.
package supervisor

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/creack/pty"

	"github.com/padmk/portside/internal/config"
	"github.com/padmk/portside/internal/notify"
	"github.com/padmk/portside/internal/paths"
	"github.com/padmk/portside/internal/ports"
)

// Status is the lifecycle state of a server.
type Status string

const (
	Stopped  Status = "stopped"  // not running
	Starting Status = "starting" // process alive, not yet accepting connections
	Running  Status = "running"  // process alive and ready
	Stopping Status = "stopping" // stop requested, waiting for exit
	Crashed  Status = "crashed"  // exited with an error
	Exited   Status = "exited"   // exited cleanly on its own
	Blocked  Status = "blocked"  // could not start: port taken by another process
)

// ErrPortBusy means the server's port is held by a process Portside did not start.
var ErrPortBusy = errors.New("port is in use by another process")

const (
	defaultCols = 160
	defaultRows = 48
	maxBackoff  = 30 * time.Second
	maxRetries  = 5
)

// restartDelay is the first automatic restart's delay; each further one doubles it.
var restartDelay = time.Second

type proc struct {
	cfg            config.Server // latest configuration
	runningEnv     []string
	runningShell   string
	runningTool    string        // version manager the live process runs under, "" for none
	launch         *launchInputs // what runningEnv was built from; nil when unknown
	detected       *string       // tool detection for cfg as last sampled; nil before the first sample
	restartPending bool
	listenPorts    []int
	listenHost     string
	missingSince   time.Time
	externalStart  string
	running        config.Server // configuration the live process was started with
	status         Status
	note           string // short human explanation of the status
	process        *os.Process
	pty            *os.File
	pid            int
	pidStart       string
	startedAt      time.Time
	readyAt        time.Time
	logReady       atomic.Bool   // a line of this generation's output matched ready_log
	readyMatch     *readyMatcher // matches ready_log while waiting for it; nil otherwise
	exitedAt       time.Time
	exitCode       int
	hasExit        bool
	stopping       bool
	want           bool // the user wants this server running
	startSeq       int  // bumped by every Start and Stop; a launch proceeds only with the latest
	retries        int
	gen            int // increments on every start; stale goroutines compare it
	done           chan struct{}
	copied         chan struct{} // closed when all PTY output has been read
	logs           *LogBuffer
	hist           history
	cpu            float64
	mem            int64
	listening      bool
	exposure       ports.Exposure // where the live listeners can be reached from; "" until read
	kind           string         // the tool the live command starts, read with a network exposure (discover.CommandKind)
	external       *ports.Listener
	cols           uint16
	rows           uint16
	removed        bool
	temp           *temporary // set for a temporary copy of a configured server
}

// Supervisor owns all managed processes.
type Supervisor struct {
	mu       sync.Mutex
	tempMu   sync.Mutex // serializes creating temporary servers
	cfg      *config.Config
	order    []string
	procs    map[string]*proc
	env      shellEnv
	logDir   string
	state    string
	changes  notify.Signals
	closing  bool
	notifier func(title, msg string)
	selfPID  int
	// release frees a port this daemon holds for another purpose before a
	// server that needs it starts.
	release func(port int)
	// publishedPort reports whether a running container of a server's
	// compose project publishes a port, for when the listener's owner is
	// hidden (a root docker-proxy).
	publishedPort func(server string, port int) bool
	// forwarded reports a port this daemon forwards to another computer,
	// and that computer's name.
	forwarded func(port int) (string, bool)
	// desktopClients counts connected apps that show crash notifications
	// themselves; while any is connected the daemon stays quiet.
	desktopClients int
	// usageWatchers counts connected clients that show resource usage now;
	// while there are none, usage is sampled every IdleSampleInterval.
	usageWatchers int
	// sampleNow asks Run for a sample at once (buffered, holds one request).
	sampleNow chan struct{}
	// exposureWanted asks Run to read listening addresses at once;
	// exposureAt is when it last did (see checkExposure).
	exposureWanted bool
	exposureAt     time.Time
	// tailnet gives this machine's Tailscale addresses (see SetTailnet).
	tailnet ports.TailnetAddrs
	// activePorts holds the ports of servers that are starting or running,
	// updated on every change, for readers that must not take mu.
	activePorts atomic.Pointer[map[int]bool]
	// previous is the state file as the previous daemon left it, read once
	// in New so that nothing this process saves hides it from Recover.
	previous persisted
	// adoptedRemoved lists adopted servers that are no longer configured;
	// Recover stops them once it has read the previous state.
	adoptedRemoved []string
}

// AddDesktopClient registers a client that notifies the user itself and
// returns a function that unregisters it.
func (s *Supervisor) AddDesktopClient() func() {
	s.mu.Lock()
	s.desktopClients++
	s.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			s.mu.Lock()
			s.desktopClients--
			s.mu.Unlock()
		})
	}
}

// New creates a supervisor for cfg. Logs and state go below stateDir.
func New(cfg *config.Config, stateDir string) *Supervisor {
	s := &Supervisor{
		procs:     map[string]*proc{},
		logDir:    paths.LogDir(stateDir),
		state:     statePath(stateDir),
		notifier:  Notify,
		selfPID:   os.Getpid(),
		sampleNow: make(chan struct{}, 1),
	}
	s.applyLocked(cfg)
	s.previous = s.load()
	s.restoreTemporaryLocked(s.previous.Temporary)
	return s
}

// SetPortRelease registers fn, called with a server's port before it starts.
func (s *Supervisor) SetPortRelease(fn func(port int)) {
	s.mu.Lock()
	s.release = fn
	s.mu.Unlock()
}

// SetPublishedPorts registers fn, which reports whether a running container
// of a server's compose project publishes a port.
func (s *Supervisor) SetPublishedPorts(fn func(server string, port int) bool) {
	s.mu.Lock()
	s.publishedPort = fn
	s.mu.Unlock()
}

// SetForwarded registers fn, which reports a port forwarded to another
// computer. Such a port is not probed for a stopped server (a connection
// would reach the other computer) and is shown as forwarded instead.
func (s *Supervisor) SetForwarded(fn func(port int) (string, bool)) {
	s.mu.Lock()
	s.forwarded = fn
	s.mu.Unlock()
}

// WantsPort reports whether a server here is starting or running on port.
// It takes no lock, so code holding its own lock while the supervisor
// calls into it (such as the port forwarding) can ask.
func (s *Supervisor) WantsPort(port int) bool {
	m := s.activePorts.Load()
	return m != nil && (*m)[port]
}

// forwardedFromLocked returns the computer a stopped server's port is
// forwarded to, or "".
func (s *Supervisor) forwardedFromLocked(p *proc) string {
	if s.forwarded == nil || p.pid != 0 || p.cfg.Port <= 0 {
		return ""
	}
	name, _ := s.forwarded(p.cfg.Port)
	return name
}

// Config returns the configuration currently applied.
func (s *Supervisor) Config() *config.Config {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cfg
}

// Subscribe returns a channel that receives a signal whenever state changes.
func (s *Supervisor) Subscribe() chan struct{} { return s.changes.Subscribe() }

// Unsubscribe stops change signals to ch.
func (s *Supervisor) Unsubscribe(ch chan struct{}) { s.changes.Unsubscribe(ch) }

func (s *Supervisor) changedLocked() {
	active := map[int]bool{}
	for _, p := range s.procs {
		if p.status != Starting && p.status != Running {
			continue
		}
		if p.pid != 0 {
			active[p.running.Port] = true
		} else {
			active[p.cfg.Port] = true
		}
	}
	delete(active, 0)
	s.activePorts.Store(&active)
	s.changes.Notify()
}

// Apply swaps in a new configuration. Running servers keep running; if their
// definition changed they are flagged as stale until restarted. Servers that
// disappeared from the file are stopped.
func (s *Supervisor) Apply(cfg *config.Config) {
	s.mu.Lock()
	removed := s.applyLocked(cfg)
	s.changedLocked()
	s.mu.Unlock()
	for _, name := range removed {
		_ = s.Stop(name)
	}
}

// ApplyRenamed preserves a live process and its logs when an edit renames it.
func (s *Supervisor) ApplyRenamed(cfg *config.Config, old, name string) {
	s.mu.Lock()
	restart := false
	if old != name {
		if p := s.procs[old]; p != nil && s.procs[name] == nil {
			delete(s.procs, old)
			s.procs[name] = p
			p.cfg.Name = name
			p.running.Name = name
			p.logs.retarget(s.logDir, name)
			if p.pid == 0 && p.status == Starting {
				p.startSeq++
				restart = p.want
				p.status = Stopped
			}
		}
		for _, q := range s.procs {
			if q.temp != nil && q.temp.Origin == old {
				q.temp.Origin = name
			}
		}
	}
	removed := s.applyLocked(cfg)
	s.saveLocked()
	s.changedLocked()
	s.mu.Unlock()
	for _, n := range removed {
		_ = s.Stop(n)
	}
	if restart {
		_ = s.Start(name, StartOptions{})
	}
}

func (s *Supervisor) applyLocked(cfg *config.Config) []string {
	s.cfg = cfg
	keep := map[string]bool{}
	for _, sv := range cfg.Servers {
		keep[sv.Name] = true
		if p, ok := s.procs[sv.Name]; ok && p.temp == nil {
			p.cfg = sv
			p.removed = false
			continue
		} else if ok {
			// A configured server now has a temporary server's name.
			p.temp = nil
			p.cfg = sv
			p.removed = false
			continue
		}
		s.procs[sv.Name] = &proc{
			cfg:    sv,
			status: Stopped,
			logs:   newLogBuffer(s.logDir, sv.Name),
			cols:   defaultCols,
			rows:   defaultRows,
		}
	}
	var removed []string
	for name, p := range s.procs {
		if keep[name] || (p.temp != nil && !p.removed && s.updateTemporaryLocked(p)) {
			continue
		}
		if p.alive() {
			p.removed = true
			removed = append(removed, name)
		} else {
			p.logs.Close()
			delete(s.procs, name)
		}
	}
	s.reorderLocked()
	return removed
}

func (p *proc) alive() bool {
	return p.status == Starting || p.status == Running || p.status == Stopping
}

func (p *proc) stale(cfg *config.Config, base []string, volatile map[string]bool) bool {
	if p.pid == 0 || p.status == Stopping {
		return false
	}
	a, b := p.running, p.cfg
	if a.Cmd != b.Cmd || a.Dir != b.Dir || a.Port != b.Port || a.Health != b.Health || a.Wrap != b.Wrap || p.runningShell != cfg.Shell {
		return true
	}
	if l := p.launch; l != nil {
		if !slices.Equal(l.Path, cfg.PathPrepend) || !maps.Equal(l.Env, cfg.Env) || !maps.Equal(a.Env, b.Env) {
			return true
		}
		if p.detected != nil && *p.detected != l.Detect {
			return true
		}
	}
	// Until the login environment has been read (as after an upgrade, which
	// keeps servers started by the previous binary), it cannot be compared.
	if base == nil {
		return false
	}
	if l := p.launch; l != nil {
		return !sameEnv(l.Base, base, volatile)
	}
	// Started by a daemon that kept only the resulting environment.
	return !sameEnv(p.runningEnv, launchEnv(base, b, cfg.PathPrepend, cfg.Env), volatile)
}

// StartOptions tune Start.
type StartOptions struct {
	// Takeover stops a foreign process holding the port before starting.
	Takeover bool
}

// Start launches a server and, first, every server it depends on. It returns
// once the launch has been initiated; readiness is reported through status.
func (s *Supervisor) Start(name string, opt StartOptions) error {
	s.mu.Lock()
	cfg := s.cfg
	p, ok := s.procs[name]
	if !ok {
		s.mu.Unlock()
		return fmt.Errorf("unknown server %q", name)
	}
	if s.closing {
		s.mu.Unlock()
		return errors.New("the daemon is shutting down")
	}
	// An explicit start gets a fresh automatic-restart budget.
	p.retries = 0
	alive := p.pid != 0
	pending := !alive && p.want && p.status == Starting
	s.mu.Unlock()
	if pending {
		return nil // a start (e.g. waiting for dependencies) is already under way
	}
	if alive {
		s.reserve(name)
		return nil
	}

	// Dependencies come from the definition, so a temporary server shares
	// its origin's dependencies.
	deps := cfg.WithDeps(p.cfg.DependsOn)
	token := s.reserve(name)
	if len(deps) == 0 {
		return s.startOne(name, opt, token)
	}
	s.mu.Lock()
	p.status = Starting
	p.note = "waiting for " + strings.Join(deps, ", ")
	s.changedLocked()
	s.mu.Unlock()
	// Dependencies start in order; each must be ready before the next starts.
	// A Stop of the target (or shutdown) cancels the rest of the chain.
	go func() {
		for _, d := range deps {
			if !s.current(name, token) {
				return
			}
			if err := s.startOne(d, StartOptions{}, s.reserve(d)); err != nil {
				s.failStart(name, token, fmt.Sprintf("dependency %s failed: %v", d, err))
				return
			}
			if err := s.WaitReady(context.Background(), d, 3*time.Minute); err != nil {
				s.failStart(name, token, fmt.Sprintf("dependency %s did not become ready: %v", d, err))
				return
			}
		}
		if err := s.startOne(name, opt, token); err != nil {
			s.failStart(name, token, err.Error())
		}
	}()
	return nil
}

// reserve marks a server as wanted and returns a token that a later launch
// must still hold; Stop invalidates it.
func (s *Supervisor) reserve(name string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.procs[name]
	if p == nil {
		return 0
	}
	p.want = true
	p.restartPending = false
	p.startSeq++
	s.saveLocked()
	return p.startSeq
}

// current reports whether token is still the latest start request for name.
func (s *Supervisor) current(name string, token int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.procs[name]
	return p != nil && !s.closing && p.want && (token == 0 || p.startSeq == token)
}

// failStart reports a failed start, unless a newer Start or a Stop took over.
func (s *Supervisor) failStart(name string, token int, msg string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if p, ok := s.procs[name]; ok && p.pid == 0 && p.startSeq == token && p.want {
		reported := p.status == Crashed || p.status == Blocked // startOne itself failed and reported it
		p.status = Crashed
		p.note = msg
		if !reported {
			s.reportCrashLocked(p, nil, 0)
		}
		s.saveLocked()
		s.changedLocked()
	}
}

// startOne launches one server while its start request token is current.
func (s *Supervisor) startOne(name string, opt StartOptions, token int) error {
	s.mu.Lock()
	p, ok := s.procs[name]
	if !ok {
		s.mu.Unlock()
		return fmt.Errorf("unknown server %q", name)
	}
	if p.pid != 0 {
		s.mu.Unlock()
		return nil
	}
	if s.closing || !p.want || (token != 0 && p.startSeq != token) {
		s.mu.Unlock()
		return nil
	}
	p.restartPending = false
	sv := p.cfg
	shell := s.cfg.Shell
	prepend := s.cfg.PathPrepend
	globalEnv := s.cfg.Env
	release := s.release
	published := s.publishedPort
	s.mu.Unlock()

	if sv.Port > 0 && release != nil {
		release(sv.Port)
	}
	if sv.Port > 0 && ports.Listening(sv.Port) {
		s.mu.Lock()
		recent := !p.exitedAt.IsZero() && time.Since(p.exitedAt) < 10*time.Second
		s.mu.Unlock()
		// A server that just exited may need a moment to release its port.
		for i := 0; recent && i < 30 && ports.Listening(sv.Port); i++ {
			time.Sleep(100 * time.Millisecond)
		}
	}
	if sv.Port > 0 && ports.Listening(sv.Port) && !composeOwnsPort(name, sv.Cmd, sv.Port, published) {
		owner, found := ports.Owner(context.Background(), sv.Port)
		if !opt.Takeover || !found {
			s.mu.Lock()
			if s.procs[name] != p || !p.want || p.startSeq != token || s.closing {
				s.mu.Unlock()
				return nil
			}
			p.status = Blocked
			if found {
				p.external = &owner
				p.note = fmt.Sprintf("port %d is used by %s (pid %d)", sv.Port, owner.Command, owner.PID)
			} else {
				p.note = fmt.Sprintf("port %d is already in use", sv.Port)
			}
			note := p.note
			s.reportCrashLocked(p, nil, 0)
			s.saveLocked()
			s.changedLocked()
			s.mu.Unlock()
			return fmt.Errorf("%w: %s", ErrPortBusy, note)
		}
		if !s.current(name, token) {
			return nil
		}
		if err := s.killForeign(owner); err != nil {
			return err
		}
	}

	if st, err := os.Stat(sv.Dir); err != nil || !st.IsDir() {
		msg := "directory not found: " + paths.ShortenHome(sv.Dir)
		s.mu.Lock()
		if s.procs[name] != p || !p.want || p.startSeq != token || s.closing {
			s.mu.Unlock()
			return nil
		}
		p.status = Crashed
		p.note = msg
		s.reportCrashLocked(p, nil, 0)
		s.saveLocked()
		s.changedLocked()
		s.mu.Unlock()
		fmt.Fprintf(p.logs, "\r\n\x1b[31m%s\x1b[0m\r\n", msg)
		return errors.New(msg)
	}

	base := s.env.get()
	env := launchEnv(base, sv, prepend, globalEnv)
	shellPath := lookPath(shell, lookupEnv(env, "PATH"))
	script := strings.ReplaceAll(sv.Cmd, "{PORT}", strconv.Itoa(sv.Port))
	tool, choice := projectTools(sv, env, shellPath, keptKeys(sv, globalEnv))
	argv := append(slices.Clone(tool.prefix), shellPath, "-c", tool.script+script)
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = sv.Dir
	cmd.Env = mergeEnv(env, tool.env)

	s.mu.Lock()
	if p.pid != 0 { // lost a race with another Start
		s.mu.Unlock()
		return nil
	}
	// Preflight can take seconds (port release, takeover, shell environment);
	// a Stop or shutdown in the meantime wins.
	if cur, ok := s.procs[name]; !ok || cur != p || p.removed || s.closing || !p.want || (token != 0 && p.startSeq != token) {
		// Stopped meanwhile: reflect it. Superseded by a newer start: leave
		// the status to that one.
		if (s.closing || !p.want) && p.status == Starting {
			p.status = Stopped
			p.note = ""
			s.changedLocked()
		}
		s.mu.Unlock()
		return nil
	}
	size := &pty.Winsize{Cols: p.cols, Rows: p.rows}
	stamp := time.Now().Format("15:04:05")
	fmt.Fprintf(p.logs, "\r\n\x1b[2m── %s  %s ──\x1b[0m\r\n\x1b[2m$ %s\x1b[0m\r\n", stamp, "start", script)
	if tool.name != "" {
		fmt.Fprintf(p.logs, "\x1b[2m(in the %s environment)\x1b[0m\r\n", tool.name)
	}
	if tool.note != "" {
		fmt.Fprintf(p.logs, "\x1b[33m%s\x1b[0m\r\n", tool.note)
	}
	// Watched before the process starts, so its first lines are seen;
	// ready_log was checked when the config loaded.
	p.logReady.Store(false)
	installReadyLog(p, sv.ReadyLog, "")
	f, err := pty.StartWithSize(cmd, size)
	if err != nil {
		installReadyLog(p, "", "")
		p.status = Crashed
		p.note = "failed to start: " + err.Error()
		s.reportCrashLocked(p, nil, 0)
		s.saveLocked()
		s.changedLocked()
		s.mu.Unlock()
		return err
	}
	p.gen++
	gen := p.gen
	p.process = cmd.Process
	p.pty = f
	p.pid = cmd.Process.Pid
	p.pidStart = processStart(p.pid)
	p.running = sv
	p.runningEnv = slices.Clone(env)
	p.runningShell = shell
	p.runningTool = tool.name
	p.launch = &launchInputs{Base: slices.Clone(base), Path: slices.Clone(prepend), Env: maps.Clone(globalEnv), Detect: choice.key()}
	p.detected = nil
	p.restartPending = false
	p.listenPorts = nil
	p.listenHost = ""
	p.missingSince = time.Time{}
	p.exposure, p.kind = "", ""
	p.status = Starting
	p.note = ""
	p.startedAt = time.Now()
	p.readyAt = time.Time{}
	p.hasExit = false
	p.stopping = false
	p.want = true
	p.external = nil
	p.listening = false
	p.hist.reset()
	p.done = make(chan struct{})
	p.copied = make(chan struct{})
	copied := p.copied
	s.changedLocked()
	s.saveLocked()
	s.mu.Unlock()

	go func() {
		defer close(copied)
		_, _ = copyTo(p.logs, f)
	}()
	go s.wait(name, p, gen)
	go s.watchReady(name, p, gen)
	return nil
}

func copyTo(dst *LogBuffer, src *os.File) (int64, error) {
	buf := make([]byte, 32*1024)
	var n int64
	for {
		k, err := src.Read(buf)
		if k > 0 {
			_, _ = dst.Write(buf[:k])
			n += int64(k)
		}
		if err != nil {
			return n, err
		}
	}
}

// exitCode is a shell-style exit code: 128 plus the signal for a process
// a signal ended, -1 when the status is unknown.
func exitCode(st *os.ProcessState, err error) int {
	if err != nil || st == nil {
		return -1
	}
	if ws, ok := st.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		return 128 + int(ws.Signal())
	}
	return st.ExitCode()
}

// wait reaps the process and decides what happens next. The process is a
// child of this daemon, also when it was adopted from the binary this one
// replaced, so waiting on its pid works either way.
func (s *Supervisor) wait(name string, p *proc, gen int) {
	s.mu.Lock()
	process := p.process
	s.mu.Unlock()
	code := exitCode(process.Wait())
	pgid := p.pid
	s.mu.Lock()
	stopping := p.stopping || s.closing
	s.mu.Unlock()
	if !stopping {
		// The leader died on its own; children left in its group are orphans.
		// During a Stop, terminate owns the whole group and its grace period.
		go reapGroup(pgid)
	}
	// Let the reader drain the last output (often the crash message). An
	// orphan holding the terminal open must not delay the exit forever.
	select {
	case <-p.copied:
	case <-time.After(2 * time.Second):
	}

	s.mu.Lock()
	if p.gen != gen {
		s.mu.Unlock()
		return
	}
	_ = p.pty.Close()
	uptime := time.Since(p.startedAt)
	p.exitedAt = time.Now()
	p.exitCode = code
	p.hasExit = true
	p.pid = 0
	p.cpu, p.mem = 0, 0
	p.listening = false
	close(p.done)
	stamp := p.exitedAt.Format("15:04:05")

	var restartIn time.Duration
	switch {
	case p.stopping || s.closing:
		p.status = Stopped
		p.note = ""
		fmt.Fprintf(p.logs, "\r\n\x1b[2m── %s  stopped ──\x1b[0m\r\n", stamp)
	case code == 0:
		p.status = Exited
		p.note = "exited on its own"
		fmt.Fprintf(p.logs, "\r\n\x1b[2m── %s  exited (code 0) ──\x1b[0m\r\n", stamp)
		if p.cfg.Restart == config.RestartAlways {
			restartIn = s.backoffLocked(p, uptime)
		}
	default:
		p.status = Crashed
		p.note = fmt.Sprintf("exited with code %d", code)
		fmt.Fprintf(p.logs, "\r\n\x1b[31m── %s  crashed (code %d) ──\x1b[0m\r\n", stamp, code)
		if p.cfg.Restart != config.RestartNo {
			restartIn = s.backoffLocked(p, uptime)
		}
	}
	if p.status == Crashed || p.status == Exited {
		if restartIn == 0 {
			p.want = false // nothing to restore after a daemon restart
		}
		if restartIn > 0 {
			p.note += fmt.Sprintf(", restarting in %s", restartIn.Round(time.Second))
		} else if p.status == Exited && p.cfg.Restart == config.RestartAlways && p.retries >= maxRetries {
			s.reportCrashLocked(p, &code, uptime) // gave up on a server meant to keep running
		} else if p.status == Crashed {
			s.reportCrashLocked(p, &code, uptime)
			if s.cfg.Notify && s.notifier != nil && s.desktopClients == 0 {
				go s.notifier(name+" crashed", fmt.Sprintf("Exited with code %d after %s", code, uptime.Round(time.Second)))
			}
		}
	}
	p.restartPending = restartIn > 0
	name = p.cfg.Name
	token := p.startSeq
	removed := p.removed
	s.changedLocked()
	s.saveLocked()
	s.mu.Unlock()

	if removed {
		s.mu.Lock()
		if q, ok := s.procs[name]; ok && q == p && q.removed {
			q.logs.Close()
			delete(s.procs, name)
			for i, n := range s.order {
				if n == name {
					s.order = append(s.order[:i], s.order[i+1:]...)
					break
				}
			}
			s.changedLocked()
		}
		s.mu.Unlock()
		return
	}
	if restartIn > 0 {
		time.AfterFunc(restartIn, func() {
			s.mu.Lock()
			ok := p.gen == gen && p.startSeq == token && p.restartPending && p.want && !p.alive() && !s.closing
			name := p.cfg.Name
			s.mu.Unlock()
			if ok {
				_ = s.startOne(name, StartOptions{}, token)
			}
		})
	}
}

// backoffLocked returns the delay before an automatic restart, or 0 to give up.
func (s *Supervisor) backoffLocked(p *proc, uptime time.Duration) time.Duration {
	if !p.want {
		return 0
	}
	if uptime > time.Minute {
		p.retries = 0
	}
	if p.retries >= maxRetries {
		p.note += fmt.Sprintf("; gave up after %d restarts", maxRetries)
		return 0
	}
	d := restartDelay << p.retries
	if d > maxBackoff {
		d = maxBackoff
	}
	p.retries++
	return d
}

// Stop ends a server gracefully: Ctrl-C to its process group, then SIGTERM,
// then SIGKILL. It returns when the process has exited.
func (s *Supervisor) Stop(name string) error {
	s.mu.Lock()
	p, ok := s.procs[name]
	if !ok {
		s.mu.Unlock()
		return fmt.Errorf("unknown server %q", name)
	}
	p.want = false
	p.restartPending = false
	p.retries = 0
	p.startSeq++ // cancels any start still in preflight
	if p.pid == 0 {
		if p.status == Starting || p.status == Blocked || p.status == Crashed || p.status == Exited {
			p.status = Stopped
			p.note = ""
		}
		s.changedLocked()
		s.saveLocked()
		s.mu.Unlock()
		return nil
	}
	pid := p.pid
	done := p.done
	if p.stopping {
		// Already stopping: a second Ctrl-C would make tools such as
		// docker compose abort their own graceful shutdown.
		s.mu.Unlock()
		<-done
		waitGroupGone(pid, 20*time.Second)
		return nil
	}
	p.stopping = true
	p.status = Stopping
	grace := p.running.Stop
	s.changedLocked()
	s.saveLocked()
	s.mu.Unlock()

	Terminate(pid, done, grace)
	return nil
}

// Restart stops a server (if running) and starts it again.
func (s *Supervisor) Restart(name string, opt StartOptions) error {
	if err := s.Stop(name); err != nil {
		return err
	}
	return s.Start(name, opt)
}

// Input writes keystrokes to a server's terminal.
func (s *Supervisor) Input(name string, data []byte) error {
	s.mu.Lock()
	p, ok := s.procs[name]
	var f *os.File
	if ok && p.pid != 0 {
		f = p.pty
	}
	s.mu.Unlock()
	if f == nil {
		return fmt.Errorf("%s is not running", name)
	}
	_, err := f.Write(data)
	return err
}

// Resize changes a server's terminal size; new processes inherit it too.
func (s *Supervisor) Resize(name string, cols, rows uint16) {
	if cols < 20 || rows < 5 || cols > 500 || rows > 300 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.procs[name]
	if !ok {
		return
	}
	p.cols, p.rows = cols, rows
	if p.pid != 0 && p.pty != nil {
		_ = pty.Setsize(p.pty, &pty.Winsize{Cols: cols, Rows: rows})
	}
}

// ActivePort is the port a server's live process uses, which can differ from
// the configured one until a restart applies an edit.
func (s *Supervisor) ActivePort(name string) (int, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.procs[name]
	if !ok {
		return 0, false
	}
	if p.pid != 0 {
		return p.running.Port, true
	}
	return p.cfg.Port, true
}

// Logs returns the server's log buffer.
func (s *Supervisor) Logs(name string) (*LogBuffer, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.procs[name]
	if !ok {
		return nil, false
	}
	return p.logs, true
}

// ClearLogs empties a server's scrollback.
func (s *Supervisor) ClearLogs(name string) {
	if lb, ok := s.Logs(name); ok {
		lb.Clear()
		s.mu.Lock()
		s.changedLocked()
		s.mu.Unlock()
	}
}

// Shutdown stops every server. The set of servers the user wanted running is
// persisted first so that the next daemon start can restore it.
func (s *Supervisor) Shutdown() {
	s.mu.Lock()
	s.saveLocked()
	s.closing = true
	var wg sync.WaitGroup
	for _, p := range s.procs {
		if p.pid == 0 {
			continue
		}
		p.stopping = true
		p.status = Stopping
		wg.Add(1)
		go func(pid int, done chan struct{}, grace time.Duration) {
			defer wg.Done()
			Terminate(pid, done, grace)
		}(p.pid, p.done, p.running.Stop)
	}
	s.changedLocked()
	s.mu.Unlock()
	wg.Wait()
	s.mu.Lock()
	for _, p := range s.procs {
		p.logs.Close()
	}
	s.mu.Unlock()
}
