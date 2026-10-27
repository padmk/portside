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
	"slices"
	"sync"
	"sync/atomic"
	"time"

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
	// againTrigger is the trigger of a start asked for while the server
	// was still running or stopping, which wait launches once it exits.
	againTrigger  string
	listenPorts   []int
	listenHost    string
	missingSince  time.Time
	externalStart string
	running       config.Server // configuration the live process was started with
	status        Status
	note          string // short human explanation of the status
	process       *os.Process
	pty           *os.File
	pid           int
	pidStart      string
	startedAt     time.Time
	readyAt       time.Time
	logReady      atomic.Bool   // a line of this generation's output matched ready_log
	readyMatch    *readyMatcher // matches ready_log while waiting for it; nil otherwise
	exitedAt      time.Time
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

// isClosed reports whether ch has been closed.
func isClosed(ch chan struct{}) bool {
	select {
	case <-ch:
		return true
	default:
		return false
	}
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
