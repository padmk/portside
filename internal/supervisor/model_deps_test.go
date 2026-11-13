package supervisor

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"pgregory.net/rapid"

	"github.com/padmk/portside/internal/config"
)

// TestModelDependencies drives a server that depends on a task (a
// migration) through starts, the task's exits, starts held in their
// preflight, upgrades in place and stops, and checks what a dependency
// promises: the task runs once for every start that needs it (never again
// for a start it already served, also across an upgrade), the server
// starts once the task succeeded and not when it failed.
func TestModelDependencies(t *testing.T) {
	modelChecks()
	rapid.Check(t, func(t *rapid.T) {
		d := newDepsHarness(t)
		defer d.cleanup()
		t.Repeat(logSteps(map[string]func(*rapid.T){
			"StartWeb": func(t *rapid.T) {
				s := d.s
				d.calls.Go(func() { _ = s.Start(depsServer, StartOptions{}) })
				d.m.startWeb()
				d.settle(t)
			},
			"StopWeb": func(t *rapid.T) {
				s := d.s
				d.calls.Go(func() { _ = s.Stop(depsServer) })
				d.m.web = webDown
				d.settle(t)
			},
			// The next start of the server waits in its preflight until
			// PassPreflight.
			"HoldPreflight": func(t *rapid.T) {
				if d.gate.isClosed() || d.m.web == webPreflight {
					t.Skip("already held")
				}
				d.gate.close(d.daemon)
				d.m.held = true
				d.settle(t)
			},
			"PassPreflight": func(t *rapid.T) {
				if !d.gate.isClosed() {
					t.Skip("nothing held")
				}
				d.gate.open(false)
				d.m.held = false
				if d.m.web == webPreflight {
					d.m.webUp()
				}
				d.settle(t)
			},
			"TaskExit": func(t *rapid.T) {
				p := d.liveIn(d.taskDir)
				if p == nil {
					t.Skip("the task does not run")
				}
				code := rapid.SampledFrom([]int{0, 1}).Draw(t, "code")
				arrives := code == 0 && d.m.web == webWaiting && d.m.held
				d.w.mu.Lock()
				d.w.exitLocked(p, code, true)
				d.w.mu.Unlock()
				if arrives {
					d.gate.await(t)
				}
				d.m.taskExit(code)
				d.settle(t)
			},
			"Upgrade": func(t *rapid.T) {
				if !d.upgrade(t) {
					t.Fatal("PrepareHandoff refused with no stop under way")
				}
				d.m.upgrade()
				d.settle(t)
			},
		}))
	})
}

const (
	depsTask   = "migrate"
	depsServer = "web"
)

type webPhase int

const (
	webDown      webPhase = iota
	webWaiting            // waiting for the task's run
	webPreflight          // the task is done; the server's start waits in its preflight
	webUp
)

func (p webPhase) String() string { return [...]string{"down", "waiting", "preflight", "up"}[p] }

// depsModel is the reference for a server that depends on a task.
type depsModel struct {
	taskRunning bool
	web         webPhase
	held        bool // the gate holds the server's next start in its preflight
	taskRuns    int
	webRuns     int
}

func (m *depsModel) webUp() {
	m.web = webUp
	m.webRuns++
}

// startWeb: a start of the server needs a run of the task from after it
// was asked for, unless one is under way.
func (m *depsModel) startWeb() {
	if m.web != webDown {
		return
	}
	m.web = webWaiting
	if !m.taskRunning {
		m.taskRunning = true
		m.taskRuns++
	}
}

func (m *depsModel) taskExit(code int) {
	m.taskRunning = false
	if m.web != webWaiting {
		return
	}
	switch {
	case code != 0:
		m.web = webDown
	case m.held:
		m.web = webPreflight
	default:
		m.webUp()
	}
}

// upgrade: the task's run and a running server are adopted, and a start
// under way goes on in the new daemon, which holds no preflight; the task
// run that served it serves it still.
func (m *depsModel) upgrade() {
	m.held = false
	if m.web == webPreflight {
		m.webUp()
	}
}

type depsHarness struct {
	w               *fakeWorld
	s               *Supervisor
	m               depsModel
	root, stateDir  string
	taskDir, webDir string
	port, daemon    int
	gate            *preflightGate
	calls           *sync.WaitGroup
	replaced        []*Supervisor
	lastObserved    string
}

func newDepsHarness(t modelTB) *depsHarness {
	root, err := os.MkdirTemp("", "model-deps-*")
	if err != nil {
		t.Fatal(err)
	}
	d := &depsHarness{
		w: newFakeWorld(), root: root, stateDir: filepath.Join(root, "state"),
		taskDir: filepath.Join(root, "task"), webDir: filepath.Join(root, "web"),
		port: freeModelPort(t), gate: newPreflightGate(), calls: &sync.WaitGroup{},
	}
	for _, dir := range []string{d.taskDir, d.webDir} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	d.s = d.newSupervisor(d.config())
	return d
}

func (d *depsHarness) config() *config.Config {
	return &config.Config{Shell: "sh", Servers: []config.Server{
		{Name: depsTask, Cmd: "fake", Dir: d.taskDir, Enabled: true, Oneshot: true, Restart: config.RestartNo, Stop: time.Hour},
		{Name: depsServer, Cmd: "fake", Dir: d.webDir, Port: d.port, Enabled: true, Restart: config.RestartNo, Stop: time.Hour,
			ReadyAfter: time.Nanosecond, DependsOn: []string{depsTask}},
	}}
}

func (d *depsHarness) newSupervisor(cfg *config.Config) *Supervisor {
	s := newWithClock(cfg, d.stateDir, d.w.clock)
	s.sys = &fakeSystem{w: d.w, daemon: d.daemon}
	s.env.env, s.env.fetched = os.Environ(), time.Now()
	s.notifier = nil
	s.SetPortRelease(d.gate.hook(d.daemon))
	return s
}

func (d *depsHarness) liveIn(dir string) *fakeProc {
	d.w.mu.Lock()
	defer d.w.mu.Unlock()
	return d.w.liveLocked(dir)
}

func (d *depsHarness) mismatch() string {
	d.s.mu.Lock()
	web := d.s.procs[depsServer]
	pid, want, status := web.livePID(), web.want, web.status
	d.s.mu.Unlock()
	d.w.mu.Lock()
	taskRuns, webRuns := d.w.launches[d.taskDir], d.w.launches[d.webDir]
	taskLive, webLive := d.w.liveLocked(d.taskDir) != nil, d.w.liveLocked(d.webDir) != nil
	d.w.mu.Unlock()
	d.lastObserved = fmt.Sprintf("task runs %d (live %v), server runs %d (live %v, pid %d, want %v, %s)",
		taskRuns, taskLive, webRuns, webLive, pid, want, status)
	m := d.m
	var diffs []string
	if taskRuns != m.taskRuns || webRuns != m.webRuns {
		diffs = append(diffs, fmt.Sprintf("want task runs %d, server runs %d", m.taskRuns, m.webRuns))
	}
	if taskLive != m.taskRunning {
		diffs = append(diffs, fmt.Sprintf("task running %v, want %v", taskLive, m.taskRunning))
	}
	switch m.web {
	case webUp:
		if pid == 0 || !webLive {
			diffs = append(diffs, "server not running")
		}
	case webWaiting, webPreflight:
		if pid != 0 || !want {
			diffs = append(diffs, "server not waiting to start")
		}
	case webDown:
		if pid != 0 || webLive || (status != Stopped && status != Crashed) {
			diffs = append(diffs, "server not down")
		}
	}
	return strings.Join(diffs, "; ")
}

func (d *depsHarness) settle(t modelTB) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if d.mismatch() == "" {
			quiet(d.s, 30*time.Millisecond)
			if d.mismatch() == "" {
				break
			}
			continue
		}
		if time.Now().After(deadline) {
			t.Fatalf("model: server %s, task running %v; supervisor: %s: %s", d.m.web, d.m.taskRunning, d.lastObserved, d.mismatch())
		}
		time.Sleep(2 * time.Millisecond)
	}
	d.w.mu.Lock()
	v := slices.Clone(d.w.violations)
	d.w.mu.Unlock()
	if len(v) > 0 {
		t.Fatalf("invariant broken: %s", strings.Join(v, "; "))
	}
}

func (d *depsHarness) upgrade(t modelTB) bool {
	hs, err := d.s.PrepareHandoff()
	if err != nil {
		return false
	}
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
	d.gate.open(true)
	d.daemon = d.w.handoff()
	d.replaced = append(d.replaced, d.s)
	d.calls = &sync.WaitGroup{}
	d.s = d.newSupervisor(d.s.Config())
	d.s.Adopt(hs)
	d.s.Recover(true)
	return true
}

func (d *depsHarness) cleanup() {
	d.w.mu.Lock()
	for _, p := range d.w.procs {
		p.slow, p.orphan = false, false
		d.w.exitLocked(p, 137, true)
		d.w.reapableLocked(p)
		d.w.closeIfGoneLocked(p)
	}
	d.w.mu.Unlock()
	d.gate.open(false)
	_ = d.s.Stop(depsServer)
	d.calls.Wait()
	for _, s := range append(d.replaced, d.s) {
		retireDaemon(s)
	}
	_ = os.RemoveAll(d.root)
}

// quiet waits until s has reported no change for d, at most a few seconds.
func quiet(s *Supervisor, d time.Duration) {
	ch := s.Subscribe()
	defer s.Unsubscribe(ch)
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

// An upgrade in place goes on with the start request the previous daemon
// was serving: a task whose run already served it does not run again, both
// when it succeeded and its dependent still waits in preflight, and when it
// failed and so did the dependent's start.
func TestUpgradeDoesNotRunAServedTaskAgain(t *testing.T) {
	for _, code := range []int{0, 1} {
		t.Run(fmt.Sprintf("code %d", code), func(t *testing.T) {
			d := newDepsHarness(t)
			defer d.cleanup()
			if code == 0 {
				d.gate.close(d.daemon)
			}
			if err := d.s.Start(depsServer, StartOptions{}); err != nil {
				t.Fatal(err)
			}
			waitFor(t, "the task's run", func() bool { return d.liveIn(d.taskDir) != nil })
			p := d.liveIn(d.taskDir)
			d.w.mu.Lock()
			d.w.exitLocked(p, code, true)
			d.w.mu.Unlock()
			if code == 0 {
				d.gate.await(t)
			} else {
				waitFor(t, "the failed start", func() bool {
					d.s.mu.Lock()
					defer d.s.mu.Unlock()
					return d.s.procs[depsServer].status == Crashed
				})
			}
			if !d.upgrade(t) {
				t.Fatal("PrepareHandoff refused")
			}
			wantWeb := map[int]int{0: 1, 1: 0}[code]
			waitFor(t, "the server's start in the new daemon", func() bool {
				d.w.mu.Lock()
				defer d.w.mu.Unlock()
				return d.w.launches[d.webDir] == wantWeb
			})
			quiet(d.s, 200*time.Millisecond)
			d.w.mu.Lock()
			runs := d.w.launches[d.taskDir]
			d.w.mu.Unlock()
			if runs != 1 {
				t.Errorf("the task ran %d times, want once", runs)
			}
		})
	}
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
