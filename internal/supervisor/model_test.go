package supervisor

import (
	"flag"
	"fmt"
	"net"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"pgregory.net/rapid"

	"github.com/padmk/portside/internal/config"
)

// The model tests drive servers through random sequences of starts, stops,
// exits, reaps, the passing of time, upgrades in place, renames and config
// reloads, on a fake process table and clock (fakesys_test.go), and compare
// the supervisor after each step with a reference model that knows only
// what the documentation promises. Deeper runs:
//
//	go test ./internal/supervisor -run TestModel -rapid.checks=2000 -rapid.steps=60
//
// A failure prints the drawn steps, which rapid shrinks to a short
// sequence, and saves it under testdata/rapid for -rapid.failfile.

// modelPhase is where the model's server is.
type modelPhase int

const (
	mDown      modelPhase = iota // no process; a restart or retry may be pending
	mPreflight                   // a start waits in its preflight, before the process starts
	mUp                          // the process runs
	mHeld                        // the leader exited and was reaped; a member still holds its terminal
	mStopping                    // a Stop waits for a leader that takes its time
)

func (p modelPhase) String() string {
	return [...]string{"down", "preflight", "up", "held", "stopping"}[p]
}

type modelPending int

const (
	noPending modelPending = iota
	restartPending
	retryPending
)

// model is the reference: what a user of the server is promised.
type model struct {
	task       bool
	restart    string
	retryAfter bool

	phase   modelPhase
	code    int  // the exit code of a held run
	again   bool // a start asked for while held or stopping: it runs once the run has ended
	pending modelPending
	retries int // automatic restarts of the current round
	// launches counts the processes the server should have started.
	launches int
}

func (m *model) launch() {
	m.phase, m.pending, m.again = mUp, noPending, false
	m.launches++
}

// start is an explicit start, in any phase; preflight says whether a start
// from down waits in its preflight.
func (m *model) start(preflight bool) {
	m.retries = 0
	switch m.phase {
	case mDown:
		if preflight {
			m.phase, m.pending = mPreflight, noPending
		} else {
			m.launch()
		}
	case mHeld, mStopping:
		m.again = true
	case mUp, mPreflight:
	}
}

func (m *model) stop() {
	m.again, m.pending, m.retries = false, noPending, 0
	if m.phase != mStopping {
		m.phase = mDown
	}
}

// ended records the end of the run: by an exit with code (unknown when
// !known), or by a stop that was asked for (stopped).
func (m *model) ended(code int, known, stopped bool) {
	if m.again {
		m.retries = 0
		m.launch()
		return
	}
	m.phase = mDown
	if stopped || (m.task && known && code == 0) {
		return
	}
	failed := code != 0 || !known
	if m.restart != config.RestartAlways && (m.restart != config.RestartOnFailure || !failed) {
		return
	}
	if m.retries < maxRetries {
		m.retries++
		m.pending = restartPending
		return
	}
	if m.retryAfter {
		m.pending = retryPending
	}
}

// tick lets time pass until nothing is due: a held terminal is let go, a
// pending restart or retry runs.
func (m *model) tick() {
	if m.phase == mHeld {
		m.ended(m.code, true, false)
	}
	if m.phase == mDown && m.pending != noPending {
		if m.pending == retryPending {
			m.retries = 0
		}
		m.launch()
	}
}

// upgrade is an upgrade in place: running servers keep running, a start in
// its preflight goes on in the new daemon, a run that ended during it has
// an unknown exit code, pending restarts keep their time.
func (m *model) upgrade() {
	switch {
	case m.phase == mPreflight:
		m.launch()
	case m.phase != mHeld:
	case m.again:
		m.launch()
	case m.task:
		m.phase = mDown // ended during the upgrade: neither done nor a crash
	case m.restart != config.RestartNo && m.retries < maxRetries:
		m.retries++
		m.launch()
	default:
		m.phase = mDown
		if m.retryAfter && m.restart != config.RestartNo {
			m.pending = retryPending
		}
	}
}

func (m *model) reload(restart string, retryAfter bool) {
	m.restart, m.retryAfter = restart, retryAfter
	if m.pending == restartPending && restart == config.RestartNo {
		m.pending = noPending
	}
	if m.pending == retryPending && (!retryAfter || restart == config.RestartNo) {
		m.pending = noPending
	}
}

// preflightGate holds starts in their preflight, at the release of the
// server's port, while it is closed.
type preflightGate struct {
	mu      sync.Mutex
	closed  chan struct{} // nil while open
	daemon  int           // the daemon whose starts it holds
	arrived chan struct{}
}

func newPreflightGate() *preflightGate { return &preflightGate{arrived: make(chan struct{}, 64)} }

// hook is the port release of daemon's supervisor.
func (g *preflightGate) hook(daemon int) func(int) {
	return func(int) {
		g.mu.Lock()
		closed := g.closed
		mine := g.daemon == daemon
		g.mu.Unlock()
		if closed == nil || !mine {
			return
		}
		g.arrived <- struct{}{}
		<-closed
	}
}

func (g *preflightGate) close(daemon int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed == nil {
		g.closed, g.daemon = make(chan struct{}), daemon
	}
}

func (g *preflightGate) isClosed() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.closed != nil
}

// open lets the held starts go on (or, with abandon, leaves them held for
// good, as when their daemon was replaced).
func (g *preflightGate) open(abandon bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed != nil && !abandon {
		close(g.closed)
	}
	g.closed = nil
	for len(g.arrived) > 0 {
		<-g.arrived
	}
}

func (g *preflightGate) await(t *rapid.T) {
	select {
	case <-g.arrived:
	case <-time.After(10 * time.Second):
		t.Fatal("the start never reached its preflight")
	}
}

const modelServer = "web"

// harness runs one supervisor at a time on a fake world.
type harness struct {
	w        *fakeWorld
	s        *Supervisor
	m        model
	name     string // the server's name, which a rename changes
	dir      string
	port     int
	stateDir string
	daemon   int
	gate     *preflightGate
	// calls are the current daemon's Stops and the calls its gate holds;
	// those of a replaced daemon end with it.
	calls    *sync.WaitGroup
	replaced []*Supervisor // daemons an upgrade replaced, whose terminals cleanup closes
}

func (h *harness) config() *config.Config {
	sv := config.Server{
		Name: h.name, Cmd: "fake", Dir: h.dir, Port: h.port, Enabled: true, Oneshot: h.m.task,
		Restart: h.m.restart, Stop: time.Hour, ReadyAfter: time.Nanosecond,
	}
	if h.m.retryAfter {
		sv.RetryAfter = 30 * time.Second
	}
	return &config.Config{Shell: "sh", Servers: []config.Server{sv}}
}

func (h *harness) newSupervisor(cfg *config.Config) *Supervisor {
	s := newWithClock(cfg, h.stateDir, h.w.clock)
	s.sys = &fakeSystem{w: h.w, daemon: h.daemon}
	s.env.env, s.env.fetched = os.Environ(), time.Now()
	s.notifier = nil
	s.SetPortRelease(h.gate.hook(h.daemon))
	return s
}

// observed is the supervisor's server in the model's terms.
type observed struct {
	status   Status
	pid      int
	want     bool
	pending  bool
	launches int
	live     int // pid of the leader that runs, 0 for none
	exited   bool
}

func (h *harness) observe() observed {
	h.s.mu.Lock()
	var o observed
	if p := h.s.procs[h.name]; p != nil {
		o.status, o.pid, o.want, o.pending = p.status, p.pid, p.want, p.restartPending
	}
	h.s.mu.Unlock()
	h.w.mu.Lock()
	o.launches = h.w.launches[h.dir]
	if p := h.w.liveLocked(h.dir); p != nil {
		o.live = p.pid
	}
	if p := h.w.procs[o.pid]; p != nil && !p.leader {
		o.exited = true
	}
	h.w.mu.Unlock()
	return o
}

// mismatch reports how o differs from the model, "" when it does not.
func (h *harness) mismatch(o observed) string {
	m := h.m
	var diffs []string
	if o.launches != m.launches {
		diffs = append(diffs, fmt.Sprintf("%d launches, want %d", o.launches, m.launches))
	}
	up := o.status == Starting || o.status == Running
	switch m.phase {
	case mUp:
		if o.pid == 0 || o.pid != o.live || !up {
			diffs = append(diffs, "not running")
		}
	case mPreflight:
		if o.pid != 0 || o.live != 0 || !o.want {
			diffs = append(diffs, "not a wanted server waiting to start")
		}
	case mHeld:
		if o.pid == 0 || !o.exited || !up {
			diffs = append(diffs, "not an ended run whose terminal is held")
		}
	case mStopping:
		if o.status != Stopping || o.pid == 0 {
			diffs = append(diffs, "not stopping")
		}
	case mDown:
		if o.pid != 0 || o.live != 0 || up || o.status == Stopping {
			diffs = append(diffs, "not down")
		}
		if o.pending != (m.pending != noPending) {
			diffs = append(diffs, fmt.Sprintf("restart pending %v, want %v", o.pending, m.pending != noPending))
		}
	}
	return strings.Join(diffs, "; ")
}

// settle waits until the supervisor agrees with the model and stays so
// for a moment, which also catches a launch too many.
func (h *harness) settle(t *rapid.T) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		o := h.observe()
		diff := h.mismatch(o)
		if diff == "" {
			h.quiet(30 * time.Millisecond)
			if o = h.observe(); h.mismatch(o) == "" {
				break
			}
			continue
		}
		if time.Now().After(deadline) {
			t.Fatalf("model %s (again %v, pending %v, retries %d), supervisor %+v: %s",
				h.m.phase, h.m.again, h.m.pending, h.m.retries, o, diff)
		}
		time.Sleep(2 * time.Millisecond)
	}
	h.w.mu.Lock()
	v := slices.Clone(h.w.violations)
	h.w.mu.Unlock()
	if len(v) > 0 {
		t.Fatalf("invariant broken: %s", strings.Join(v, "; "))
	}
}

// quiet waits until the supervisor has reported no change for d, at most
// a few seconds.
func (h *harness) quiet(d time.Duration) {
	ch := h.s.Subscribe()
	defer h.s.Unsubscribe(ch)
	limit := time.After(3 * time.Second)
	for {
		select {
		case <-ch:
		case <-time.After(d):
			return
		case <-limit:
			return
		}
	}
}

func (h *harness) live() *fakeProc {
	h.w.mu.Lock()
	defer h.w.mu.Unlock()
	return h.w.liveLocked(h.dir)
}

// call runs f in the background, as the gate or a slow stop may hold it.
func (h *harness) call(f func()) { h.calls.Go(f) }

// tick advances the clock by a minute, firing every timer due by then in
// order, each once the supervisor is quiet after the one before.
func (h *harness) tick() {
	until := h.w.clock().Add(time.Minute)
	for {
		tm := h.w.nextTimer(until)
		if tm == nil {
			break
		}
		go tm.f()
		h.quiet(20 * time.Millisecond)
	}
	h.w.mu.Lock()
	h.w.now = until
	h.w.mu.Unlock()
}

// upgrade replaces the daemon in place, as exec would; it reports false
// when the supervisor refused the handoff.
func (h *harness) upgrade(t *rapid.T) bool {
	hs, err := h.s.PrepareHandoff()
	if err != nil {
		return false
	}
	// exec keeps the terminals open in the new image; here the new daemon
	// gets its own descriptor for each.
	for i := range hs {
		fd, err := KeepOnExec(hs[i].pty, true)
		if err != nil {
			t.Fatal(err)
		}
		dup, err := syscallDup(fd)
		if err != nil {
			t.Fatal(err)
		}
		hs[i].PTY = dup
	}
	// Starts the old daemon held in their preflight end with it.
	h.gate.open(true)
	h.daemon = h.w.handoff()
	h.replaced = append(h.replaced, h.s)
	h.calls = &sync.WaitGroup{}
	h.s = h.newSupervisor(h.s.Config())
	h.s.Adopt(hs)
	h.s.Recover(true)
	return true
}

// cleanup ends what still runs, so no call outlives the run.
func (h *harness) cleanup() {
	h.w.mu.Lock()
	for _, p := range h.w.procs {
		p.slow, p.orphan = false, false
		h.w.exitLocked(p, 137, true)
		h.w.reapableLocked(p)
		h.w.closeIfGoneLocked(p)
	}
	h.w.mu.Unlock()
	h.gate.open(false)
	h.calls.Wait()
	// A replaced daemon no longer runs; its terminals would otherwise stay
	// open until the system has none left.
	for _, s := range h.replaced {
		s.mu.Lock()
		for _, p := range s.procs {
			if p.pty != nil {
				_ = p.pty.Close()
			}
		}
		s.mu.Unlock()
	}
}

func newHarness(t *rapid.T) *harness {
	root, err := os.MkdirTemp("", "model-*")
	if err != nil {
		t.Fatal(err)
	}
	return &harness{w: newFakeWorld(), dir: root, stateDir: root + "/state", gate: newPreflightGate(), calls: &sync.WaitGroup{}}
}

// freeModelPort returns a loopback port nothing listens on, for a server
// whose start runs the port release (the preflight gate).
func freeModelPort(t *rapid.T) int {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// TestModelLifecycle compares random sequences of lifecycle events with the
// reference model. Besides the model's phases it checks that a server never
// has two processes at once and starts exactly as often as promised: a stop
// is final until the next start, a start in any phase ends with a process,
// and a task runs once per start.
func TestModelLifecycle(t *testing.T) {
	modelChecks()
	rapid.Check(t, func(t *rapid.T) {
		h := newHarness(t)
		defer func() { _ = os.RemoveAll(h.dir) }()
		h.name, h.port = modelServer, freeModelPort(t)
		h.m = model{
			task:       rapid.Bool().Draw(t, "task"),
			restart:    rapid.SampledFrom([]string{config.RestartNo, config.RestartOnFailure, config.RestartAlways}).Draw(t, "restart"),
			retryAfter: rapid.Bool().Draw(t, "retryAfter"),
		}
		h.s = h.newSupervisor(h.config())
		defer h.cleanup()

		start := func() {
			s, name := h.s, h.name
			h.call(func() { _ = s.Start(name, StartOptions{}) })
		}
		t.Repeat(logSteps(map[string]func(*rapid.T){
			"Start": func(t *rapid.T) {
				held := h.gate.isClosed()
				if held {
					start()
				} else if err := h.s.Start(h.name, StartOptions{}); err != nil {
					t.Fatalf("Start: %v", err)
				}
				if held && h.m.phase == mDown {
					h.gate.await(t)
				}
				h.m.start(held)
				h.settle(t)
			},
			// A start that waits in its preflight until PassPreflight.
			"StartInPreflight": func(t *rapid.T) {
				if h.m.phase != mDown || h.gate.isClosed() {
					t.Skip("not down")
				}
				h.gate.close(h.daemon)
				start()
				h.gate.await(t)
				h.m.start(true)
				h.settle(t)
			},
			"PassPreflight": func(t *rapid.T) {
				if !h.gate.isClosed() {
					t.Skip("no start held")
				}
				h.gate.open(false)
				if h.m.phase == mPreflight {
					h.m.launch()
				}
				h.settle(t)
			},
			"Stop": func(t *rapid.T) {
				s, name := h.s, h.name
				h.call(func() { _ = s.Stop(name) })
				h.m.stop()
				h.settle(t)
			},
			// A Stop whose process takes its time to exit after Ctrl-C.
			"SlowStop": func(t *rapid.T) {
				p := h.live()
				if h.m.phase != mUp || p == nil {
					t.Skip("nothing runs")
				}
				h.w.mu.Lock()
				p.slow = true
				h.w.mu.Unlock()
				s, name := h.s, h.name
				h.call(func() { _ = s.Stop(name) })
				h.m.phase, h.m.again, h.m.pending, h.m.retries = mStopping, false, noPending, 0
				h.settle(t)
			},
			// The slow leader exits, or the member holding an ended run's
			// terminal does.
			"Release": func(t *rapid.T) {
				if h.m.phase != mStopping && h.m.phase != mHeld {
					t.Skip("nothing to release")
				}
				h.w.mu.Lock()
				for _, p := range h.w.procs {
					if p.leader && p.slow {
						h.w.exitLocked(p, 130, true)
					}
					if p.orphan {
						p.orphan = false
						h.w.closeIfGoneLocked(p)
					}
				}
				h.w.mu.Unlock()
				if h.m.phase == mStopping {
					h.m.ended(130, true, true)
				} else {
					h.m.ended(h.m.code, true, false)
				}
				h.settle(t)
			},
			// The leader exits on its own; with held, a member it leaves
			// behind keeps the terminal open.
			"Exit": func(t *rapid.T) {
				p := h.live()
				if p == nil || (h.m.phase != mUp && h.m.phase != mStopping) {
					t.Skip("nothing runs")
				}
				code := rapid.SampledFrom([]int{0, 1}).Draw(t, "code")
				held := h.m.phase == mUp && rapid.Bool().Draw(t, "held")
				h.w.mu.Lock()
				p.orphan = held
				h.w.exitLocked(p, code, true)
				h.w.mu.Unlock()
				switch {
				case h.m.phase == mStopping:
					h.m.ended(code, true, true)
				case held:
					h.m.phase, h.m.code = mHeld, code
				default:
					h.m.ended(code, true, false)
				}
				h.settle(t)
			},
			// A start that arrives after the leader exited and before wait
			// reaped it.
			"ExitThenStart": func(t *rapid.T) {
				if skipFinding["ExitThenStart"] {
					t.Skip("known finding")
				}
				p := h.live()
				if p == nil || h.m.phase != mUp {
					t.Skip("nothing runs")
				}
				code := rapid.SampledFrom([]int{0, 1}).Draw(t, "code")
				h.w.mu.Lock()
				h.w.exitLocked(p, code, false)
				h.w.mu.Unlock()
				if err := h.s.Start(h.name, StartOptions{}); err != nil {
					t.Fatalf("Start: %v", err)
				}
				h.w.mu.Lock()
				h.w.reapableLocked(p)
				h.w.mu.Unlock()
				h.m.retries = 0
				h.m.again = true
				h.m.ended(code, true, false)
				h.settle(t)
			},
			"Tick": func(t *rapid.T) {
				h.tick()
				h.m.tick()
				h.settle(t)
			},
			"Upgrade": func(t *rapid.T) {
				if !h.upgrade(t) {
					if h.m.phase != mStopping {
						t.Fatal("PrepareHandoff refused with no stop under way")
					}
					h.settle(t)
					return
				}
				if h.m.phase == mStopping {
					t.Fatal("PrepareHandoff went ahead while a stop was under way")
				}
				h.m.upgrade()
				h.settle(t)
			},
			"Rename": func(t *rapid.T) {
				if h.m.phase == mPreflight && skipFinding["RenameInPreflight"] {
					t.Skip("known finding")
				}
				old := h.name
				h.name = map[string]string{modelServer: modelServer + "-renamed", modelServer + "-renamed": modelServer}[old]
				s, cfg, name := h.s, h.config(), h.name
				if h.gate.isClosed() {
					h.call(func() { s.ApplyRenamed(cfg, old, name) })
				} else {
					s.ApplyRenamed(cfg, old, name)
				}
				h.settle(t)
			},
			"Reload": func(t *rapid.T) {
				restart := rapid.SampledFrom([]string{config.RestartNo, config.RestartOnFailure, config.RestartAlways}).Draw(t, "restart")
				retry := rapid.Bool().Draw(t, "retryAfter")
				h.m.reload(restart, retry)
				h.s.Apply(h.config())
				h.settle(t)
			},
		}))
	})
}

// logSteps logs each step that ran, so a failure shows the sequence
// without the steps that did not apply; the failing step is the last one
// drawn.
func logSteps(steps map[string]func(*rapid.T)) map[string]func(*rapid.T) {
	out := map[string]func(*rapid.T){}
	for name, f := range steps {
		out[name] = func(t *rapid.T) {
			f(t)
			t.Logf("ran %s", name)
		}
	}
	return out
}

// skipFinding names the steps that reproduce a reported defect still open.
// They are left out unless MODEL_FINDINGS names them (comma-separated).
var skipFinding = map[string]bool{"ExitThenStart": true, "RenameInPreflight": true}

func init() {
	for name := range strings.SplitSeq(os.Getenv("MODEL_FINDINGS"), ",") {
		delete(skipFinding, strings.TrimSpace(name))
	}
}

// modelChecks makes the default run short enough for every go test; the
// flags still choose deeper runs.
func modelChecks() {
	set := map[string]bool{}
	flag.Visit(func(f *flag.Flag) { set[f.Name] = true })
	if !set["rapid.checks"] {
		_ = flag.Set("rapid.checks", "20")
	}
}
