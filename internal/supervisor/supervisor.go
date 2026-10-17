// Package supervisor runs dev servers in pseudo-terminals and tracks their
// lifecycle: start (with dependencies), readiness, graceful stop, crash
// handling with restart policies, resource usage and scrollback.
package supervisor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/creack/pty"

	"github.com/padmk/portside/internal/config"
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

type proc struct {
	cfg            config.Server // latest configuration
	runningEnv     []string
	runningShell   string
	restartPending bool
	listenPorts    []int
	listenHost     string
	missingSince   time.Time
	externalStart  string
	running        config.Server // configuration the live process was started with
	status         Status
	note           string // short human explanation of the status
	cmd            *exec.Cmd
	pty            *os.File
	pid            int
	pidStart       string
	startedAt      time.Time
	readyAt        time.Time
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
	external       *ports.Listener
	cols           uint16
	rows           uint16
	removed        bool
}

// Supervisor owns all managed processes.
type Supervisor struct {
	mu       sync.Mutex
	cfg      *config.Config
	order    []string
	procs    map[string]*proc
	env      shellEnv
	logDir   string
	state    string
	subs     map[chan struct{}]struct{}
	closing  bool
	notifier func(title, msg string)
	selfPID  int
	// release frees a port this daemon holds for another purpose before a
	// server that needs it starts.
	release func(port int)
	// forwarded reports a port this daemon forwards to another computer,
	// and that computer's name.
	forwarded func(port int) (string, bool)
	// desktopClients counts connected apps that show crash notifications
	// themselves; while any is connected the daemon stays quiet.
	desktopClients int
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
		procs:    map[string]*proc{},
		logDir:   filepath.Join(stateDir, "logs"),
		state:    filepath.Join(stateDir, "state.json"),
		subs:     map[chan struct{}]struct{}{},
		notifier: Notify,
		selfPID:  os.Getpid(),
	}
	s.applyLocked(cfg)
	return s
}

// SetPortRelease registers fn, called with a server's port before it starts.
func (s *Supervisor) SetPortRelease(fn func(port int)) {
	s.mu.Lock()
	s.release = fn
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
func (s *Supervisor) Subscribe() chan struct{} {
	ch := make(chan struct{}, 1)
	s.mu.Lock()
	s.subs[ch] = struct{}{}
	s.mu.Unlock()
	return ch
}

// Unsubscribe stops change signals to ch.
func (s *Supervisor) Unsubscribe(ch chan struct{}) {
	s.mu.Lock()
	delete(s.subs, ch)
	s.mu.Unlock()
}

func (s *Supervisor) changedLocked() {
	for ch := range s.subs {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
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
	s.order = s.order[:0]
	for _, sv := range cfg.Servers {
		keep[sv.Name] = true
		s.order = append(s.order, sv.Name)
		if p, ok := s.procs[sv.Name]; ok {
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
		if keep[name] {
			continue
		}
		if p.alive() {
			p.removed = true
			s.order = append(s.order, name)
			removed = append(removed, name)
		} else {
			p.logs.Close()
			delete(s.procs, name)
		}
	}
	return removed
}

func (p *proc) alive() bool {
	return p.status == Starting || p.status == Running || p.status == Stopping
}

func (p *proc) stale(cfg *config.Config, base []string) bool {
	if p.pid == 0 || p.status == Stopping {
		return false
	}
	a, b := p.running, p.cfg
	if a.Cmd != b.Cmd || a.Dir != b.Dir || a.Port != b.Port || a.Health != b.Health || p.runningShell != cfg.Shell {
		return true
	}
	return !sameEnv(p.runningEnv, launchEnv(base, b, cfg.PathPrepend, cfg.Env))
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

	chain := cfg.WithDeps([]string{name})
	deps := chain[:len(chain)-1]
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
		p.status = Crashed
		p.note = msg
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
	if sv.Port > 0 && ports.Listening(sv.Port) && !composeOwnsPort(sv.Cmd, sv.Port) {
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
		s.saveLocked()
		s.changedLocked()
		s.mu.Unlock()
		fmt.Fprintf(p.logs, "\r\n\x1b[31m%s\x1b[0m\r\n", msg)
		return errors.New(msg)
	}

	env := s.buildEnv(sv, prepend, globalEnv)
	shellPath := lookPath(shell, lookupEnv(env, "PATH"))
	script := strings.ReplaceAll(sv.Cmd, "{PORT}", strconv.Itoa(sv.Port))
	cmd := exec.Command(shellPath, "-c", script)
	cmd.Dir = sv.Dir
	cmd.Env = env

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
	f, err := pty.StartWithSize(cmd, size)
	if err != nil {
		p.status = Crashed
		p.note = "failed to start: " + err.Error()
		s.saveLocked()
		s.changedLocked()
		s.mu.Unlock()
		return err
	}
	p.gen++
	gen := p.gen
	p.cmd = cmd
	p.pty = f
	p.pid = cmd.Process.Pid
	p.pidStart = processStart(p.pid)
	p.running = sv
	p.runningEnv = slices.Clone(env)
	p.runningShell = shell
	p.restartPending = false
	p.listenPorts = nil
	p.listenHost = ""
	p.missingSince = time.Time{}
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

func lookPath(name, pathEnv string) string {
	if p := findInPath(name, pathEnv); p != "" {
		return p
	}
	return "/bin/sh"
}

func findInPath(name, pathEnv string) string {
	if filepath.IsAbs(name) {
		return name
	}
	for _, dir := range filepath.SplitList(pathEnv) {
		cand := filepath.Join(dir, name)
		if st, err := os.Stat(cand); err == nil && !st.IsDir() && st.Mode()&0o111 != 0 {
			return cand
		}
	}
	if p, err := exec.LookPath(name); err == nil {
		return p
	}
	return ""
}

// LookPath finds an executable on the user's login-shell PATH plus the
// configured path entries. It returns "" when nothing matches.
func (s *Supervisor) LookPath(name string) string {
	s.mu.Lock()
	prepend := s.cfg.PathPrepend
	s.mu.Unlock()
	pathEnv := lookupEnv(s.env.get(), "PATH")
	if len(prepend) > 0 {
		pathEnv = strings.Join(prepend, string(os.PathListSeparator)) + string(os.PathListSeparator) + pathEnv
	}
	return findInPath(name, pathEnv)
}

// Environ is the login-shell environment with the configured path entries,
// for helper commands the daemon runs on the user's behalf.
func (s *Supervisor) Environ() []string {
	s.mu.Lock()
	prepend := s.cfg.PathPrepend
	s.mu.Unlock()
	base := slices.Clone(s.env.get())
	if len(prepend) == 0 {
		return base
	}
	path := strings.Join(prepend, string(os.PathListSeparator)) + string(os.PathListSeparator) + lookupEnv(base, "PATH")
	return mergeEnv(base, map[string]string{"PATH": path})
}

func (s *Supervisor) buildEnv(sv config.Server, prepend []string, global map[string]string) []string {
	return launchEnv(s.env.get(), sv, prepend, global)
}

func launchEnv(base []string, sv config.Server, prepend []string, global map[string]string) []string {
	over := map[string]string{
		"FORCE_COLOR": "1",
		"TERM":        "xterm-256color",
		"COLORTERM":   "truecolor",
	}
	if sv.Port > 0 {
		over["PORT"] = strconv.Itoa(sv.Port)
	}
	if len(prepend) > 0 {
		over["PATH"] = strings.Join(prepend, string(os.PathListSeparator)) + string(os.PathListSeparator) + lookupEnv(base, "PATH")
	}
	expand := func(v string) string { return strings.ReplaceAll(v, "{PORT}", strconv.Itoa(sv.Port)) }
	for k, v := range global {
		over[k] = expand(v)
	}
	for k, v := range sv.Env {
		over[k] = expand(v)
	}
	if IsComposeCommand(sv.Cmd) {
		over["COMPOSE_MENU"] = "false"
	}
	return mergeEnv(base, over)
}

// wait reaps the process and decides what happens next.
func (s *Supervisor) wait(name string, p *proc, gen int) {
	err := p.cmd.Wait()
	code := 0
	if err != nil {
		code = -1
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			if ws, ok := ee.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
				code = 128 + int(ws.Signal())
			} else {
				code = ee.ExitCode()
			}
		}
	}
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
		} else if p.status == Crashed && s.cfg.Notify && s.notifier != nil && s.desktopClients == 0 {
			go s.notifier(name+" crashed", fmt.Sprintf("Exited with code %d after %s", code, uptime.Round(time.Second)))
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
	d := time.Second << p.retries
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

func waitGroupGone(pgid int, d time.Duration) {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) && syscall.Kill(-pgid, 0) == nil {
		time.Sleep(50 * time.Millisecond)
	}
}

// Terminate signals a process group and escalates until the leader and
// every other member have exited, so a restart never finds the port taken
// by a child that was still shutting down.
func Terminate(pgid int, done <-chan struct{}, grace time.Duration) {
	if grace <= 0 {
		grace = 10 * time.Second
	}
	_ = syscall.Kill(-pgid, syscall.SIGINT)
	if waitGroup(pgid, done, grace) {
		return
	}
	_ = syscall.Kill(-pgid, syscall.SIGTERM)
	if waitGroup(pgid, done, 3*time.Second) {
		return
	}
	_ = syscall.Kill(-pgid, syscall.SIGKILL)
	<-done
	waitGroupGone(pgid, 2*time.Second)
}

// waitGroup waits up to d for the leader to be reaped and the group to empty.
func waitGroup(pgid int, done <-chan struct{}, d time.Duration) bool {
	deadline := time.After(d)
	select {
	case <-done:
	case <-deadline:
		return false
	}
	for syscall.Kill(-pgid, 0) == nil {
		select {
		case <-deadline:
			return false
		case <-time.After(50 * time.Millisecond):
		}
	}
	return true
}

func reapGroup(pgid int) {
	if syscall.Kill(-pgid, 0) != nil {
		return
	}
	_ = syscall.Kill(-pgid, syscall.SIGTERM)
	time.Sleep(2 * time.Second)
	if syscall.Kill(-pgid, 0) == nil {
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
	}
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

// IsManagedPID reports whether pid belongs to a process tree Portside runs.
func (s *Supervisor) IsManagedPID(pid int) (string, bool) {
	s.mu.Lock()
	roots := map[int]string{}
	for name, p := range s.procs {
		if p.pid != 0 {
			roots[p.pid] = name
		}
	}
	s.mu.Unlock()
	if len(roots) == 0 {
		return "", false
	}
	table := sampleProcesses(context.Background())
	for cur, hops := pid, 0; cur > 1 && hops < 64; hops++ {
		if name, ok := roots[cur]; ok {
			return name, true
		}
		ps, ok := table[cur]
		if !ok {
			break
		}
		cur = ps.ppid
	}
	return "", false
}

// composeOwnsPort reports whether a container runtime holds the port for a
// server that is itself a compose stack. Its containers may still be up from
// an earlier run; "docker compose up" attaches to them instead of failing.
func composeOwnsPort(cmd string, port int) bool {
	if !IsComposeCommand(cmd) {
		return false
	}
	owner, ok := ports.Owner(context.Background(), port)
	return ok && IsContainerRuntime(owner.Command)
}

// IsComposeCommand reports whether a server's command runs a compose stack.
func IsComposeCommand(cmd string) bool {
	c := strings.ToLower(cmd)
	return strings.Contains(c, "docker compose") || strings.Contains(c, "docker-compose") || strings.Contains(c, "podman compose")
}

// IsContainerRuntime reports whether a process name belongs to Docker,
// OrbStack, Colima or Podman, which publish container ports on the host.
func IsContainerRuntime(command string) bool {
	lc := strings.ToLower(command)
	for _, rt := range []string{"docker", "com.docke", "orbstack", "vpnkit", "colima", "lima", "podman"} {
		if strings.Contains(lc, rt) {
			return true
		}
	}
	return false
}

// protected process names are never killed by takeover: stopping them would
// take down Docker, AirPlay or the session rather than a stray dev server.
var protected = []string{"docker", "com.docke", "orbstack", "vpnkit", "launchd", "controlce", "rapportd", "systemd", "colima", "lima", "podman"}

// Killable reports why a foreign listener may not be killed, or "" if it may.
func (s *Supervisor) Killable(l ports.Listener) string {
	if l.PID <= 1 || l.PID == s.selfPID {
		return "refusing to kill this process"
	}
	lc := strings.ToLower(l.Command)
	for _, p := range protected {
		if strings.Contains(lc, p) {
			return fmt.Sprintf("%s looks like a system or container service; stop it there", l.Command)
		}
	}
	return ""
}

// KillListener ends a foreign process that holds a port.
func (s *Supervisor) KillListener(l ports.Listener) error {
	return s.killForeign(l)
}

func (s *Supervisor) killForeign(l ports.Listener) error {
	if why := s.Killable(l); why != "" {
		return errors.New(why)
	}
	if name, ok := s.IsManagedPID(l.PID); ok {
		return s.Stop(name)
	}
	// Remember who we are signalling: once that process is gone its pid may be
	// reused, and nothing else must receive the escalation.
	identity := processStart(l.PID)
	if identity == "" {
		return nil // already gone
	}
	same := func() bool { return processStart(l.PID) == identity }
	if err := syscall.Kill(l.PID, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
		return fmt.Errorf("kill %d: %w", l.PID, err)
	}
	for i := 0; i < 50 && same(); i++ {
		time.Sleep(100 * time.Millisecond)
	}
	if same() {
		_ = syscall.Kill(l.PID, syscall.SIGKILL)
	}
	for i := 0; i < 30 && l.Port > 0 && ports.Listening(l.Port); i++ {
		time.Sleep(100 * time.Millisecond)
	}
	if l.Port > 0 && ports.Listening(l.Port) {
		return fmt.Errorf("port %d is still in use", l.Port)
	}
	return nil
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

// Run performs periodic bookkeeping until ctx ends: resource sampling,
// listening checks and detection of foreign processes on configured ports.
func (s *Supervisor) Run(ctx context.Context) {
	tick := time.NewTicker(SampleInterval)
	defer tick.Stop()
	knownExternal := map[string]int{}
	for {
		s.sample(ctx, knownExternal)
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

func (s *Supervisor) sample(ctx context.Context, knownExternal map[string]int) {
	table := sampleProcesses(ctx)
	children := childIndex(table)

	type check struct {
		name  string
		port  int
		alive bool
		pid   int
		start string
	}
	var checks []check
	hasLive := false
	forwarded := map[string]bool{} // stopped servers whose port forwards to another computer
	s.mu.Lock()
	for name, p := range s.procs {
		if p.pid != 0 {
			hasLive = true
			cpu, mem, ok := treeUsage(table, children, p.pid)
			if ok {
				p.cpu, p.mem = cpu, mem
				p.hist.add(cpu, mem)
			}
		}
		if p.cfg.Port > 0 && p.status != Starting && p.status != Stopping {
			port := p.cfg.Port
			if p.pid != 0 {
				port = p.running.Port
			}
			pid := 0
			if p.external != nil {
				pid = p.external.PID
			}
			checks = append(checks, check{name, port, p.pid != 0, pid, p.externalStart})
			forwarded[name] = s.forwardedFromLocked(p) != ""
		}
	}
	s.mu.Unlock()

	if hasLive {
		_ = s.env.get()
	}
	for _, c := range checks {
		// A forwarded port answers from the other computer; nothing here holds it.
		listening := !forwarded[c.name] && ports.Listening(c.port)
		var owner *ports.Listener
		if !c.alive && listening {
			if knownExternal[c.name] != c.port || c.pid == 0 || c.start == "" || processStart(c.pid) != c.start {
				if l, ok := ports.Owner(ctx, c.port); ok {
					owner = &l
				} else {
					owner = &ports.Listener{Port: c.port}
				}
			}
		}
		identity := ""
		if owner != nil {
			identity = processStart(owner.PID)
		}
		s.mu.Lock()
		p, ok := s.procs[c.name]
		if ok && c.alive == (p.pid != 0) {
			if c.alive {
				p.listening = listening
			} else if !listening {
				if p.external != nil {
					p.external = nil
					if p.status == Blocked {
						p.status = Stopped
						p.note = ""
					}
				}
				delete(knownExternal, c.name)
			} else if owner != nil {
				p.external = owner
				p.externalStart = identity
				knownExternal[c.name] = c.port
			}
		}
		s.mu.Unlock()
	}
	s.mu.Lock()
	s.changedLocked()
	s.mu.Unlock()
}

func processStart(pid int) string {
	out, err := exec.Command("ps", "-o", "lstart=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
