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
	retryAt        time.Time // when the pending restart is a retry after retry_after, its time
	restartAt      time.Time // when the pending restart is an automatic one after a crash, its time
	// scheduleReserved marks a scheduled run reserved but not begun yet.
	scheduleReserved bool
	listenPorts      []int
	listenHost       string
	missingSince     time.Time
	externalStart    string
	running          config.Server // configuration the live process was started with
	status           Status
	note             string // short human explanation of the status
	process          *os.Process
	pty              *os.File
	pid              int
	pidStart         string
	startedAt        time.Time
	readyAt          time.Time
	logReady         atomic.Bool   // a line of this generation's output matched ready_log
	readyMatch       *readyMatcher // matches ready_log while waiting for it; nil otherwise
	exitedAt         time.Time
	// settledAt is when the latest run or start attempt ended: an exit, a
	// failed start or a stop of a server waiting to start. A task's waiters
	// compare it with when they asked (see taskStateLocked).
	settledAt time.Time
	exitCode  int
	hasExit   bool
	stopping  bool
	// overMemory is the note of a stop under way because a usage sample
	// was over memory_limit, "" otherwise; the exit then counts as a crash.
	// overMemorySeq is startSeq when that stop began.
	overMemory    string
	overMemorySeq int
	// ending is closed once the latest Stop or Shutdown has ended the whole
	// process group, which can outlive the leader; nil before the first
	// (see Supervisor.endings).
	ending   chan struct{}
	want     bool // the user wants this server running
	startSeq int  // bumped by every Start and Stop; a launch proceeds only with the latest
	retries  int
	trigger  string // what began the latest round of runs and automatic restarts (triggerStart and others)
	// streak counts crash reports in a row without the server ready in
	// between; incident is the event of a problem a hook reported that has
	// not ended (see readyLocked), and incidentSince when that began.
	streak        int
	incident      string
	incidentSince time.Time
	gen           int // increments on every start; stale goroutines compare it
	done          chan struct{}
	copied        chan struct{} // closed when all PTY output has been read
	reader        *outputReader // copies the terminal into logs
	logs          *LogBuffer
	hist          history
	cpu           float64
	mem           int64
	listening     bool
	exposure      ports.Exposure // where the live listeners can be reached from; "" until read
	kind          string         // the tool the live command starts, read with a network exposure (discover.CommandKind)
	external      *ports.Listener
	cols          uint16
	rows          uint16
	removed       bool
	temp          *temporary // set for a temporary copy of a configured server
	// The schedule's next run, the expression it was computed for, and
	// when the latest scheduled run began (see schedule.go).
	nextRun      time.Time
	scheduleExpr string
	lastRun      time.Time
	// watch is what the scans of the watch patterns found (see watch.go);
	// nil without patterns.
	watch *watchState
}

// Supervisor owns all managed processes.
type Supervisor struct {
	mu      sync.Mutex
	saveErr string // why the last state write failed; "" when it worked
	// endings holds the process-group terminations under way by server
	// name, also of servers removed from the config meanwhile: Shutdown
	// waits for them, an upgrade waits until there are none, and a start of
	// the same name waits for its own.
	endings map[chan struct{}]string
	tempMu  sync.Mutex // serializes creating temporary servers
	cfg     *config.Config
	order   []string
	procs   map[string]*proc
	env     shellEnv
	logDir  string
	state   string
	changes notify.Signals
	closing bool
	// handoff is set while the servers are being handed to the binary this
	// daemon re-executes; closing is set too.
	handoff  bool
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
	// handoffExited holds handed-off servers that exited during the upgrade,
	// and handoffRestarts those of them that their restart policy restarts;
	// Recover reads both.
	handoffExited   map[string]bool
	handoffRestarts []string
	// upgraded says this daemon took over from an upgrade in place, which
	// keeps pending retries whatever restore says.
	upgraded bool
	// handedOver holds the servers adopted from the daemon this one
	// replaced; Recover leaves them to their own state.
	handedOver map[string]bool
	// handoffEnd is signalled when an upgrade in place fails or turns into
	// a shutdown (see awaitHandoffLocked).
	handoffEnd *sync.Cond
	// now is the wall clock schedules follow.
	now func() time.Time
	// processes reads the process table for usage samples.
	processes func(context.Context) map[int]procSample
	// watchEvery and watchQuiet are WatchInterval and WatchQuiet, and
	// watchLimit WatchFileLimit, unless a test sets others; watchWake wakes
	// an idle runWatches after a config change.
	watchEvery time.Duration
	watchQuiet time.Duration
	watchLimit int
	watchWake  chan struct{}
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
	return newWithClock(cfg, stateDir, time.Now)
}

// newWithClock is New with now as the wall clock schedules follow.
func newWithClock(cfg *config.Config, stateDir string, now func() time.Time) *Supervisor {
	s := &Supervisor{
		procs:      map[string]*proc{},
		logDir:     paths.LogDir(stateDir),
		state:      statePath(stateDir),
		notifier:   Notify,
		selfPID:    os.Getpid(),
		sampleNow:  make(chan struct{}, 1),
		now:        now,
		processes:  sampleProcesses,
		watchEvery: WatchInterval,
		watchQuiet: WatchQuiet,
		watchLimit: WatchFileLimit,
		watchWake:  make(chan struct{}, 1),
	}
	s.handoffEnd = sync.NewCond(&s.mu)
	s.applyLocked(cfg)
	s.previous = s.load()
	s.restoreTemporaryLocked(s.previous.Temporary)
	s.restoreHistoryLocked(s.previous.Servers)
	for _, p := range s.procs {
		syncScheduleLocked(p, s.now()) // Recover saves the state
	}
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
	s.syncSchedulesLocked()
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
			// A stop still ending its group goes with it, so a start of the
			// new name waits for that group.
			if _, ok := s.endings[p.ending]; ok && p.ending != nil {
				s.endings[p.ending] = name
			}
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
	s.syncSchedulesLocked()
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
	s.env.setOwn(cfg.OwnEnv)
	keep := map[string]bool{}
	for _, sv := range cfg.Servers {
		keep[sv.Name] = true
		if p, ok := s.procs[sv.Name]; ok && p.temp == nil {
			if !p.cfg.Enabled && sv.Enabled {
				p.nextRun = time.Time{} // runs due while disabled stay skipped
			}
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
	// After temporary copies took their origin's new settings.
	cancelled := false
	for _, p := range s.procs {
		if p.restartPending && ((!p.retryAt.IsZero() && !p.cfg.RetriesAfterGivingUp()) || (!p.restartAt.IsZero() && p.cfg.Restart == config.RestartNo)) {
			cancelPendingLocked(p)
			cancelled = true
		}
	}
	if cancelled {
		s.saveLocked()
	}
	s.reorderLocked()
	s.wakeWatchesLocked()
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
	if a.Cmd != b.Cmd || a.Dir != b.Dir || a.Port != b.Port || a.Health != b.Health || a.Wrap != b.Wrap || a.Umask != b.Umask || a.Oneshot != b.Oneshot || p.runningShell != cfg.Shell {
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
	// Since is when the start was asked for, now when zero. A task that a
	// server depends on counts as done after a successful run that ended at
	// or after it, so starts asked for together (a bulk start) share one run.
	Since time.Time
	// trigger is what asks for the start; "" (an automatic restart) keeps
	// the one that began the round.
	trigger string
}

// Start launches a server and, first, every server it depends on. It returns
// once the launch has been initiated; readiness is reported through status.
func (s *Supervisor) Start(name string, opt StartOptions) error {
	if opt.trigger == "" {
		opt.trigger = triggerStart
	}
	return s.start(name, opt)
}

// start is Start with opt.trigger as given.
func (s *Supervisor) start(name string, opt StartOptions) error {
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
	if p.pid != 0 {
		// A server that is stopping starts again once it has exited (see
		// wait), unless a later Stop withdraws the request. The request is
		// recorded under the same lock as the check, so an exit in between
		// cannot miss it.
		s.reserveLocked(p)
		s.mu.Unlock()
		return nil
	}
	pending := p.want && p.status == Starting
	// A task that a run since this request finished (one a dependent of
	// it in the same bulk start ran) is done for it.
	done := p.cfg.Oneshot && !opt.Since.IsZero() && taskStateLocked(p, opt.Since) == taskDone
	s.mu.Unlock()
	if pending || done {
		return nil // a start (e.g. waiting for dependencies) is already under way
	}

	// Dependencies come from the definition, so a temporary server shares
	// its origin's dependencies.
	return s.launch(name, cfg.WithDeps(p.cfg.DependsOn), opt, s.reserve(name))
}

// launch starts deps in order and then name, while token is the latest
// start request for name.
func (s *Supervisor) launch(name string, deps []string, opt StartOptions, token int) error {
	if opt.Since.IsZero() {
		opt.Since = time.Now()
	}
	if len(deps) == 0 {
		return s.startOne(name, opt, token)
	}
	s.mu.Lock()
	if p := s.procs[name]; p != nil && p.pid == 0 && p.startSeq == token {
		p.status = Starting
		p.note = "waiting for " + strings.Join(deps, ", ")
		if opt.trigger != "" {
			p.trigger = opt.trigger
		}
		s.changedLocked()
	}
	s.mu.Unlock()
	// Dependencies start in order; each must be ready before the next starts.
	// A Stop of the target (or shutdown) cancels the rest of the chain.
	go func() {
		for _, d := range deps {
			if !s.current(name, token) {
				return
			}
			task, done := s.taskDone(d, opt.Since)
			if done {
				continue // a run since the request satisfies it
			}
			if err := s.startOne(d, StartOptions{trigger: triggerStart}, s.reserve(d)); err != nil {
				s.failStart(name, token, fmt.Sprintf("dependency %s failed: %v", d, err))
				return
			}
			if err := s.WaitReady(context.Background(), d, opt.Since, 3*time.Minute); err != nil {
				if f, ok := errors.AsType[*waitFailure](err); ok && task {
					s.failStart(name, token, fmt.Sprintf("dependency %s failed: %s", d, f.reason))
				} else {
					s.failStart(name, token, fmt.Sprintf("dependency %s did not become ready: %v", d, err))
				}
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
	return s.reserveLocked(p)
}

// reserveStartLocked records a start of p, which is down, for launch with
// trigger, as Start does: a fresh round of automatic restarts, and the
// dependencies to start first.
func (s *Supervisor) reserveStartLocked(p *proc, trigger string) (deps []string, token int) {
	p.retries = 0
	p.trigger = trigger
	return s.cfg.WithDeps(p.cfg.DependsOn), s.reserveLocked(p)
}

// reserveLocked records a start request for p and returns its token.
func (s *Supervisor) reserveLocked(p *proc) int {
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
	s.awaitHandoffLocked()
	p := s.procs[name]
	return p != nil && !s.closing && p.want && (token == 0 || p.startSeq == token)
}

// failStart reports a failed start, unless a newer Start or a Stop took over.
func (s *Supervisor) failStart(name string, token int, msg string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if p, ok := s.procs[name]; ok && p.pid == 0 && p.startSeq == token && p.want {
		if p.status == Crashed || p.status == Blocked {
			return // startOne itself failed and reported it
		}
		p.status = Crashed
		p.note = msg
		s.startFailedLocked(p)
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
	s.awaitHandoffLocked()
	if s.closing || !p.want || (token != 0 && p.startSeq != token) {
		s.mu.Unlock()
		return nil
	}
	p.restartPending = false
	if opt.trigger != "" {
		p.trigger = opt.trigger
	}
	if ending := s.endingLocked(name); ending != nil {
		// The leader has exited, but a Stop is still ending the rest of its
		// group (also one of a server of this name removed and added again),
		// which may hold the port; start once it is gone.
		p.status = Starting
		p.note = "waiting for the previous process to exit"
		s.changedLocked()
		s.mu.Unlock()
		<-ending
		if !s.current(name, token) {
			return nil
		}
		return s.startOne(name, opt, token)
	}
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
			s.awaitHandoffLocked()
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
			s.startFailedLocked(p)
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
		s.awaitHandoffLocked()
		if s.procs[name] != p || !p.want || p.startSeq != token || s.closing {
			s.mu.Unlock()
			return nil
		}
		p.status = Crashed
		p.note = msg
		s.startFailedLocked(p)
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
	argv := append(slices.Clone(tool.prefix), shellPath, "-c", umaskScript(sv.Umask)+tool.script+script)
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = sv.Dir
	cmd.Env = mergeEnv(env, tool.env)

	s.mu.Lock()
	s.awaitHandoffLocked()
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
	p.scheduleReserved = false // the scheduled run, if any, has begun
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
	installReadyLog(p, sv.ReadyLog, nil)
	f, err := pty.StartWithSize(cmd, size)
	if err != nil {
		installReadyLog(p, "", nil)
		p.status = Crashed
		p.note = "failed to start: " + err.Error()
		s.startFailedLocked(p)
		s.saveLocked()
		s.changedLocked()
		s.mu.Unlock()
		return err
	}
	f = pollableTerminal(f)
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
	p.overMemory = ""
	p.want = true
	p.external = nil
	p.listening = false
	p.hist.reset()
	p.done = make(chan struct{})
	p.copied = make(chan struct{})
	copied := p.copied
	p.reader = newOutputReader(f, p.logs)
	reader := p.reader
	s.changedLocked()
	s.saveLocked()
	s.mu.Unlock()

	go func() {
		defer close(copied)
		reader.run()
	}()
	go s.wait(name, p, gen)
	go s.watchReady(name, p, gen)
	return nil
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
	stopping := s.requestedExitLocked(p) || p.overMemory != ""
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
	p.settledAt = p.exitedAt
	p.exitCode = code
	p.hasExit = true
	p.pid = 0
	p.cpu, p.mem = 0, 0
	p.listening = false
	close(p.done)
	stamp := p.exitedAt.Format("15:04:05")
	overMemory := p.overMemory
	p.overMemory = ""

	var restartIn time.Duration
	again := false
	switch {
	case s.requestedExitLocked(p):
		p.status = Stopped
		p.note = ""
		fmt.Fprintf(p.logs, "\r\n\x1b[2m── %s  stopped ──\x1b[0m\r\n", stamp)
		// Started again while it was stopping, with no Stop since.
		if p.stopping && p.want && !p.removed && !s.closing {
			again = true
			p.status = Starting
		}
	case overMemory != "" && p.want && p.startSeq != p.overMemorySeq && !p.removed && !s.closing:
		// Started again while the memory limit stopped it.
		p.status = Starting
		p.note = ""
		fmt.Fprintf(p.logs, "\r\n\x1b[31m── %s  stopped: %s ──\x1b[0m\r\n", stamp, overMemory)
		again = true
	case overMemory != "":
		// Whatever the exit code, also a task's: the limit ended it.
		p.status = Crashed
		p.note = overMemory
		fmt.Fprintf(p.logs, "\r\n\x1b[31m── %s  crashed: %s ──\x1b[0m\r\n", stamp, overMemory)
		if p.cfg.Restart != config.RestartNo {
			restartIn = s.backoffLocked(p, uptime)
		}
	case code == 0 && p.running.Oneshot:
		s.taskDoneLocked(p, stamp)
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
	p.restartPending, p.retryAt, p.restartAt = false, time.Time{}, time.Time{}
	if p.status == Crashed || p.status == Exited {
		// Down for good: a crash nothing restarts, or restart = "always"
		// gave up on a server meant to keep running.
		down := restartIn == 0 && (p.status == Crashed || (p.cfg.Restart == config.RestartAlways && p.retries >= maxRetries))
		retrying := false
		if restartIn > 0 {
			now := time.Now()
			s.scheduleRestartLocked(p, now, now.Add(restartIn))
		} else if down {
			s.reportCrashLocked(p, &code, uptime)
			if p.status == Crashed && s.cfg.Notify && s.notifier != nil && s.desktopClients == 0 {
				msg := fmt.Sprintf("Exited with code %d after %s", code, uptime.Round(time.Second))
				if overMemory != "" {
					msg = fmt.Sprintf("Stopped after %s: %s", uptime.Round(time.Second), overMemory)
				}
				go s.notifier(name+" crashed", msg)
			}
			retrying = s.retryLocked(p)
		}
		if restartIn == 0 && !retrying {
			p.want = false // nothing to restore after a daemon restart
		}
	}
	name = p.cfg.Name
	token := p.startSeq
	removed := p.removed
	var deps []string
	if again {
		deps = s.cfg.WithDeps(p.cfg.DependsOn)
	}
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
	if again {
		_ = s.launch(name, deps, StartOptions{trigger: triggerStart}, token)
		return
	}
}

// scheduleRestartLocked restarts p at the time at, an automatic restart
// after a crash that continues its round of restarts. A start or stop
// meanwhile cancels it.
func (s *Supervisor) scheduleRestartLocked(p *proc, now, at time.Time) {
	p.restartPending, p.restartAt, p.retryAt = true, at, time.Time{}
	left := pendingNoteLocked(p, now, at)
	gen, token := p.gen, p.startSeq
	s.afterLocked(left, func() func() {
		if s.procs[p.cfg.Name] != p || p.removed || p.gen != gen || p.startSeq != token || !p.restartPending || !p.restartAt.Equal(at) || !p.want || p.alive() || s.closing {
			return nil
		}
		name := p.cfg.Name
		return func() { _ = s.startOne(name, StartOptions{}, token) }
	})
}

// pendingNoteLocked ends p's note with when its pending restart at the time
// at happens, and returns how long that is from now.
func pendingNoteLocked(p *proc, now, at time.Time) time.Duration {
	left := max(at.Sub(now), 0)
	if left > time.Second {
		left = left.Round(time.Second)
	}
	p.note, _, _ = strings.Cut(p.note, restartingNote)
	p.note += restartingNote + left.String()
	return left
}

// awaitHandoffLocked waits while an upgrade in place is under way: it
// either replaces this process, or fails and lets a start in preflight go
// on instead of giving it up.
func (s *Supervisor) awaitHandoffLocked() {
	for s.closing && s.handoff {
		s.handoffEnd.Wait()
	}
}

// afterLocked calls f under the lock after d, then runs what f returns once
// the lock is released. While an upgrade in place is under way, f waits for
// it to replace this process or fail, so a failed upgrade keeps a pending
// restart.
func (s *Supervisor) afterLocked(d time.Duration, f func() func()) {
	time.AfterFunc(d, func() {
		s.mu.Lock()
		if s.closing && s.handoff {
			s.mu.Unlock()
			s.afterLocked(time.Second, f)
			return
		}
		next := f()
		s.mu.Unlock()
		if next != nil {
			next()
		}
	})
}

// isClosed reports whether ch has been closed.
func isClosed(ch chan struct{}) bool {
	select {
	case <-ch:
		return true
	default:
		return false
	}
}

// retryLocked starts p again after its retry_after, with a fresh round of
// automatic restarts, when it is down for good although its restart policy
// would keep it running; it reports whether a retry is due. A start or stop
// meanwhile cancels it.
func (s *Supervisor) retryLocked(p *proc) bool {
	// During an upgrade in place the retry waits for it to end (afterLocked):
	// a failed upgrade must keep it.
	if !p.cfg.RetriesAfterGivingUp() || !p.want || p.removed || (s.closing && !s.handoff) {
		return false
	}
	now := time.Now()
	s.scheduleRetryLocked(p, now, now.Add(p.cfg.RetryAfter))
	return true
}

// scheduleRetryLocked starts p again at the time at, with a fresh round of
// automatic restarts. A start or stop meanwhile cancels it, and so does a
// config that no longer asks for retries.
func (s *Supervisor) scheduleRetryLocked(p *proc, now, at time.Time) {
	p.restartPending, p.retryAt, p.restartAt = true, at, time.Time{}
	left := pendingNoteLocked(p, now, at)
	token := p.startSeq
	s.afterLocked(left, func() func() {
		name := p.cfg.Name
		if s.procs[name] != p || p.startSeq != token || !p.restartPending || !p.retryAt.Equal(at) || !p.want || p.pid != 0 || p.alive() || p.removed || s.closing {
			return nil
		}
		p.retries = 0
		deps := s.cfg.WithDeps(p.cfg.DependsOn)
		return func() { _ = s.launch(name, deps, StartOptions{trigger: triggerRetry}, token) }
	})
}

// cancelPendingLocked drops p's pending restart or retry, leaving it down.
func cancelPendingLocked(p *proc) {
	p.restartPending, p.retryAt, p.restartAt = false, time.Time{}, time.Time{}
	p.note, _, _ = strings.Cut(p.note, restartingNote)
	p.want = false
}

// restartingNote starts the part of a note that announces an automatic restart.
const restartingNote = ", restarting in "

// requestedExitLocked reports whether p's process was asked to exit: by a
// Stop or by the daemon shutting down. During a handoff the daemon only
// re-executes, so an exit then is the server's own.
func (s *Supervisor) requestedExitLocked(p *proc) bool {
	return p.stopping || (s.closing && !s.handoff)
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
	return s.stop(name, nil)
}

// errSuperseded is stop's error when only refused the stop.
var errSuperseded = errors.New("superseded by a later start or stop")

// stop is Stop; when only is set, it stops the server only if only, called
// under the lock, accepts it.
func (s *Supervisor) stop(name string, only func(*proc) bool) error {
	s.mu.Lock()
	p, ok := s.procs[name]
	if !ok {
		s.mu.Unlock()
		return fmt.Errorf("unknown server %q", name)
	}
	if only != nil && !only(p) {
		s.mu.Unlock()
		return errSuperseded
	}
	p.want = false
	p.restartPending = false
	p.scheduleReserved = false
	p.retries = 0
	p.startSeq++ // cancels any start still in preflight
	if p.pid == 0 {
		if p.status == Starting || p.status == Blocked || p.status == Crashed || p.status == Exited {
			p.status = Stopped
			p.note = ""
			p.settledAt = time.Now()
		}
		s.changedLocked()
		s.saveLocked()
		ending := p.ending
		s.mu.Unlock()
		if ending != nil {
			<-ending // a stop still ending the group after its leader exited
		}
		return nil
	}
	pid := p.pid
	done := p.done
	if ending := p.ending; ending != nil && !isClosed(ending) {
		// Already stopping: a second Ctrl-C would make tools such as
		// docker compose abort their own graceful shutdown. A stop for
		// memory_limit under way becomes this one, so the server ends up
		// stopped rather than crashed.
		if !p.stopping {
			p.stopping = true
			p.note = ""
			s.changedLocked()
			s.saveLocked()
		}
		s.mu.Unlock()
		<-ending
		return nil
	}
	p.stopping = true
	p.status = Stopping
	grace := p.running.Stop
	ending := s.beginEndingLocked(p)
	s.changedLocked()
	s.saveLocked()
	s.mu.Unlock()

	Terminate(pid, done, grace)
	s.endEnding(ending)
	return nil
}

// beginEndingLocked records that p's process group is being ended and
// returns the channel endEnding closes once it is.
func (s *Supervisor) beginEndingLocked(p *proc) chan struct{} {
	ch := make(chan struct{})
	p.ending = ch
	if s.endings == nil {
		s.endings = map[chan struct{}]string{}
	}
	s.endings[ch] = p.cfg.Name
	return ch
}

func (s *Supervisor) endEnding(ch chan struct{}) {
	s.mu.Lock()
	delete(s.endings, ch)
	s.mu.Unlock()
	close(ch)
}

// endingLocked returns a termination under way for a server named name.
func (s *Supervisor) endingLocked(name string) chan struct{} {
	for ch, n := range s.endings {
		if n == name {
			return ch
		}
	}
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
		_ = setTerminalSize(p.pty, cols, rows)
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
// persisted first so that the next daemon start can restore it; once the
// servers have exited, the state is saved again without their process
// groups, whose ids an unrelated program may get before the next start.
func (s *Supervisor) Shutdown() {
	s.mu.Lock()
	for _, p := range s.procs {
		// No automatic restart follows a shutdown.
		if p.restartPending {
			p.restartPending = false
			p.note, _, _ = strings.Cut(p.note, restartingNote)
		}
	}
	st := s.stateLocked()
	s.savedLocked(writeState(s.state, st))
	s.closing = true
	s.handoff = false
	s.handoffEnd.Broadcast()
	var wg sync.WaitGroup
	// Stops still ending a group, also after its leader exited or its
	// server was removed: a second Ctrl-C would make tools such as docker
	// compose abort their own graceful shutdown.
	for ending := range s.endings {
		wg.Go(func() { <-ending })
	}
	for _, p := range s.procs {
		if p.pid == 0 || (p.ending != nil && !isClosed(p.ending)) {
			continue
		}
		p.stopping = true
		p.status = Stopping
		ending := s.beginEndingLocked(p)
		pid, done, grace := p.pid, p.done, p.running.Stop
		wg.Go(func() {
			Terminate(pid, done, grace)
			s.endEnding(ending)
		})
	}
	s.changedLocked()
	s.mu.Unlock()
	wg.Wait()
	s.mu.Lock()
	for name, g := range st.Groups {
		if p := s.procs[name]; (p == nil || p.pid == 0) && syscall.Kill(-g.PID, 0) != nil {
			delete(st.Groups, name)
		}
	}
	st.Saved = time.Now()
	s.savedLocked(writeState(s.state, st))
	for _, p := range s.procs {
		p.logs.Close()
	}
	s.mu.Unlock()
}
