package supervisor

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/creack/pty"
	"github.com/padmk/portside/internal/paths"
	"github.com/padmk/portside/internal/ports"
)

// StartOptions tune Start.
type StartOptions struct {
	// Takeover stops a foreign process holding the port before starting.
	Takeover bool
	// TakeoverPID, when set, limits Takeover to that process: a port held
	// by another one (the confirmed process exited and something else took
	// the port) leaves the server blocked, as without Takeover.
	TakeoverPID int
	// Since is when the start was asked for, now when zero. A task that a
	// server depends on counts as done after a successful run that ended at
	// or after it, so starts asked for together (a bulk start) share one run.
	Since time.Time
	// trigger is what asks for the start; "" (an automatic restart) keeps
	// the one that began the round.
	trigger string
	// after, for a Restart, is its stop: the start goes ahead only while
	// nothing has started or stopped the server since.
	after *stopped
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
	p, ok := s.procs[name]
	if !ok {
		s.mu.Unlock()
		return fmt.Errorf("unknown server %q", name)
	}
	if s.closing {
		s.mu.Unlock()
		return errors.New("the daemon is shutting down")
	}
	if a := opt.after; a != nil && (a.p != p || a.seq != p.startSeq) {
		s.mu.Unlock()
		return nil
	}
	opt.after = nil
	// An explicit start gets a fresh automatic-restart budget; an automatic
	// restart (no trigger) goes on with its round's.
	if opt.trigger != "" {
		p.retries = 0
	}
	if p.live() != nil {
		// A server that is stopping, or whose process has ended and whose
		// last output is still draining, starts again once wait has
		// recorded the exit, unless a later Stop withdraws the request.
		// The request is recorded under the same lock as the check, so an
		// exit in between cannot miss it. A task's run in progress
		// satisfies a start asked for together with others (Since), as it
		// does a dependency.
		s.transitionLocked(p, evRequest{req: startRequest{trigger: opt.trigger, at: opt.Since}, after: s.startAfterRunLocked(p, opt.Since)})
		s.mu.Unlock()
		return nil
	}
	// A start under way serves this one too, unless this one may take
	// the port over: it replaces that start.
	pending := p.request() != nil && !opt.Takeover
	// A task whose run since this request ended (one a dependent of it in
	// the same bulk start ran) is not run again for it, done or failed.
	done := false
	if p.cfg.Oneshot && !opt.Since.IsZero() && !p.settledAt.Before(opt.Since) {
		state := taskStateLocked(p, opt.Since)
		done = state == taskDone || state == taskFailed
	}
	if pending || done {
		s.mu.Unlock()
		return nil
	}
	// Dependencies come from the definition, so a temporary server shares
	// its origin's dependencies. They are read under the lock that reserves
	// the start, so a config reload cannot mix two versions.
	deps := s.cfg.WithDeps(p.cfg.DependsOn)
	if opt.Since.IsZero() {
		opt.Since = time.Now()
	}
	s.transitionLocked(p, evRequest{req: startRequest{trigger: opt.trigger, at: opt.Since}})
	token := p.startSeq
	s.mu.Unlock()
	return s.launch(p, deps, opt, token)
}

// launch starts deps in order and then p, while token is p's start under
// way. The start holds p itself, so a rename meanwhile does not end it.
func (s *Supervisor) launch(p *proc, deps []string, opt StartOptions, token int) error {
	s.mu.Lock()
	if opt.Since.IsZero() {
		// The request's own time, which a task it waits for is measured by.
		opt.Since = time.Now()
		if req := p.request(); req != nil && p.startingWith(token) {
			opt.Since = req.at
		}
	}
	if len(deps) == 0 {
		s.mu.Unlock()
		return s.startOne(p, opt, token)
	}
	if p.startingWith(token) {
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
			if !s.current(p, token) {
				return
			}
			dep := s.reserveDependency(d, opt.Since)
			switch {
			case dep.p == nil || dep.done:
				continue // a run since the request satisfies it
			case dep.failed != "":
				// A run since the request failed: the same request does not
				// run it again.
				s.failStart(p, token, fmt.Sprintf("dependency %s failed: %s", d, dep.failed))
				return
			}
			task := dep.task
			if err := s.startOne(dep.p, StartOptions{trigger: triggerStart}, dep.token); err != nil {
				s.failStart(p, token, fmt.Sprintf("dependency %s failed: %v", d, err))
				return
			}
			if err := s.WaitReady(context.Background(), d, opt.Since, 3*time.Minute); err != nil {
				if f, ok := errors.AsType[*waitFailure](err); ok && task {
					s.failStart(p, token, fmt.Sprintf("dependency %s failed: %s", d, f.reason))
				} else {
					s.failStart(p, token, fmt.Sprintf("dependency %s did not become ready: %v", d, err))
				}
				return
			}
		}
		if err := s.startOne(p, opt, token); err != nil {
			s.failStart(p, token, err.Error())
		}
	}()
	return nil
}

// dependency is how reserveDependency left a dependency: a task done or
// failed in a run since the request, or reserved with token.
type dependency struct {
	p          *proc
	task, done bool
	failed     string // why a task's run since the request failed
	token      int
}

// reserveDependency reserves a start of dependency name with a fresh round
// of automatic restarts, as Start does, unless it is a task whose run since
// the request already ended (done or failed); one lock covers both, so a
// run that ends in between is not repeated.
func (s *Supervisor) reserveDependency(name string, since time.Time) dependency {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.procs[name]
	if p == nil {
		return dependency{}
	}
	if task := p.cfg.Oneshot; task && p.live() == nil && !p.settledAt.Before(since) {
		switch taskStateLocked(p, since) {
		case taskDone:
			return dependency{p: p, task: true, done: true}
		case taskFailed:
			return dependency{p: p, task: true, failed: cmp.Or(p.note, string(p.status()))}
		case taskPending:
			// Under way or on its way: reserved below, and waited for.
		}
	}
	p.retries = 0
	s.transitionLocked(p, evRequest{req: startRequest{trigger: triggerStart, at: since}, after: s.startAfterRunLocked(p, since)})
	return dependency{p: p, task: p.cfg.Oneshot, token: p.startSeq}
}

// startAfterRunLocked reports whether a start of p asked for now comes
// after the end of its live run, which ended on its own: its process has
// ended (also before wait has recorded it), so wait runs the start once it
// has recorded the exit. A task's run that ended at or after since
// (non-zero for a dependency or a start asked for together with others)
// serves the start instead. A stopping server starts again through its
// own path in wait.
func (s *Supervisor) startAfterRunLocked(p *proc, since time.Time) bool {
	if p.live() == nil || s.requestedExitLocked(p) || p.stopping() != nil {
		return false
	}
	if p.cfg.Oneshot && !since.IsZero() {
		// Ordered by wait's record of the end, which becomes the run's
		// settledAt: a request from before it shares the run.
		at, ok := exitRecordedLocked(p)
		return ok && at.Before(since)
	}
	return processEndedLocked(p)
}

// reserveStartLocked records a start of p, which is down, with trigger, as
// Start does: a fresh round of automatic restarts, and the dependencies to
// start first. It returns them and the start's token.
func (s *Supervisor) reserveStartLocked(p *proc, trigger string) (deps []string, token int) {
	p.retries = 0
	p.trigger = trigger
	s.transitionLocked(p, evRequest{req: startRequest{trigger: trigger}})
	return s.cfg.WithDeps(p.cfg.DependsOn), p.startSeq
}

// startRequest is a start asked for: what asked for it ("" for an
// automatic restart, which keeps its round's trigger) and when.
type startRequest struct {
	trigger string
	at      time.Time
}

// current reports whether token is still p's start under way.
func (s *Supervisor) current(p *proc, token int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.awaitHandoffLocked()
	return s.currentLocked(p, token)
}

// currentLocked is current with s.mu held.
func (s *Supervisor) currentLocked(p *proc, token int) bool {
	return !s.closing && !p.removed && s.procs[p.cfg.Name] == p && p.startingWith(token)
}

// settleStart ends p's start under way with token as failed with status
// and note, when it is still current: a launch that returned without a
// process. One that a Stop, a newer start or the daemon's end replaced is
// left to them, and during an upgrade in place the next daemon goes on
// with it.
func (s *Supervisor) settleStart(p *proc, token int, status Status, note string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.currentLocked(p, token) {
		s.transitionLocked(p, evLaunchFailed{status: status, note: note})
	}
}

// failStart reports a failed start, unless a newer start or a Stop took
// over, or startOne itself failed and reported it.
func (s *Supervisor) failStart(p *proc, token int, msg string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if p.startingWith(token) {
		s.transitionLocked(p, evLaunchFailed{status: Crashed, note: msg})
	}
}

// startOne launches p while token is its start under way. It holds p
// itself, so a rename during the preflight does not end the start.
func (s *Supervisor) startOne(p *proc, opt StartOptions, token int) (err error) {
	// Every way out that leaves this start under way without a process,
	// an error or a panic among them, ends it as failed, so the server is
	// neither left starting nor wanted for a start that will not come.
	defer func() {
		if v := recover(); v != nil {
			// The lock may be this goroutine's: settle once it is free.
			go s.settleStart(p, token, Crashed, fmt.Sprintf("failed to start: %v", v))
			panic(v)
		}
		note := "the start ended before its process did"
		if err != nil {
			note = err.Error()
		}
		s.settleStart(p, token, Crashed, note)
	}()
	s.mu.Lock()
	if p.live() != nil {
		s.mu.Unlock()
		return nil
	}
	s.awaitHandoffLocked()
	if !s.currentLocked(p, token) {
		s.mu.Unlock()
		return nil
	}
	name := p.cfg.Name
	if opt.trigger != "" {
		p.trigger = opt.trigger
	}
	if ending := s.endingLocked(name); ending != nil {
		// The leader has exited, but a Stop is still ending the rest of its
		// group (also one of a server of this name removed and added again),
		// which may hold the port; start once it is gone.
		p.note = "waiting for the previous process to exit"
		s.changedLocked()
		s.mu.Unlock()
		<-ending
		if !s.current(p, token) {
			return nil
		}
		return s.startOne(p, opt, token)
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
	if sv.Port > 0 && s.sys.portInUse(sv.Port) {
		s.mu.Lock()
		recent := !p.exitedAt.IsZero() && time.Since(p.exitedAt) < 10*time.Second
		s.mu.Unlock()
		// A server that just exited may need a moment to release its port.
		for i := 0; recent && i < 30 && s.sys.portInUse(sv.Port); i++ {
			time.Sleep(100 * time.Millisecond)
		}
	}
	if sv.Port > 0 && s.sys.portInUse(sv.Port) && !composeOwnsPort(name, sv.Cmd, sv.Port, published) {
		owner, found := ports.Owner(context.Background(), sv.Port)
		if found && opt.TakeoverPID != 0 && owner.PID != opt.TakeoverPID {
			// Several processes can share a listening socket (a prefork
			// server and its workers): the confirmed one may be among them.
			for _, l := range ports.List(context.Background()) {
				if l.Port == sv.Port && l.PID == opt.TakeoverPID {
					owner = l
				}
			}
		}
		takeover := opt.Takeover && (opt.TakeoverPID == 0 || owner.PID == opt.TakeoverPID)
		if !takeover || !found {
			s.mu.Lock()
			s.awaitHandoffLocked()
			if !s.currentLocked(p, token) {
				s.mu.Unlock()
				return nil
			}
			note := fmt.Sprintf("port %d is already in use", sv.Port)
			if found {
				p.external = &owner
				note = fmt.Sprintf("port %d is used by %s (pid %d)", sv.Port, owner.Command, owner.PID)
			}
			s.transitionLocked(p, evLaunchFailed{status: Blocked, note: note})
			s.mu.Unlock()
			return fmt.Errorf("%w: %s", ErrPortBusy, note)
		}
		if !s.current(p, token) {
			return nil
		}
		if err := s.killForeign(owner); err != nil {
			s.mu.Lock()
			s.awaitHandoffLocked()
			// The port stays taken: blocked, as without a takeover.
			if s.currentLocked(p, token) {
				p.external = &owner
				s.transitionLocked(p, evLaunchFailed{status: Blocked, note: fmt.Sprintf("port %d: taking it over from %s (pid %d) failed: %v", sv.Port, owner.Command, owner.PID, err)})
			}
			s.mu.Unlock()
			return err
		}
	}

	if st, err := os.Stat(sv.Dir); err != nil || !st.IsDir() {
		msg := "directory not found: " + paths.ShortenHome(sv.Dir)
		s.mu.Lock()
		s.awaitHandoffLocked()
		if !s.currentLocked(p, token) {
			s.mu.Unlock()
			return nil
		}
		s.transitionLocked(p, evLaunchFailed{status: Crashed, note: msg})
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
	// Preflight can take seconds (port release, takeover, shell environment);
	// a Stop, a newer start or the shutdown in the meantime wins, and the
	// phase already says so.
	if !s.currentLocked(p, token) {
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
	installReadyLog(p, sv.ReadyLog, nil)
	f, leader, err := s.sys.startInTerminal(cmd, size)
	if err != nil {
		installReadyLog(p, "", nil)
		s.transitionLocked(p, evLaunchFailed{status: Crashed, note: "failed to start: " + err.Error()})
		s.mu.Unlock()
		return err
	}
	f = pollableTerminal(f)
	p.gen++
	gen := p.gen
	r := &run{pid: leader.pid(), process: leader, pty: f, done: make(chan struct{}), copied: make(chan struct{})}
	r.pidStart = s.sys.processStart(r.pid)
	r.reader = newOutputReader(f, p.logs)
	s.transitionLocked(p, evLaunched{run: r})
	p.running = sv
	p.runningEnv = slices.Clone(env)
	p.runningShell = shell
	p.runningTool = tool.name
	p.launch = &launchInputs{Base: slices.Clone(base), Path: slices.Clone(prepend), Env: maps.Clone(globalEnv), Detect: choice.key()}
	p.detected = nil
	p.listenPorts = nil
	p.listenHost = ""
	p.missingSince = time.Time{}
	p.exposure, p.kind = "", ""
	p.note = ""
	p.startedAt = time.Now()
	p.readyAt = time.Time{}
	p.hasExit = false
	p.external = nil
	p.listening = false
	p.hist.reset()
	s.changedLocked()
	s.saveLocked()
	s.mu.Unlock()

	go func() {
		defer close(r.copied)
		r.reader.run()
	}()
	go s.wait(p, r, gen)
	go s.watchReady(name, p, gen)
	return nil
}
