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
	p, ok := s.procs[name]
	if !ok {
		s.mu.Unlock()
		return fmt.Errorf("unknown server %q", name)
	}
	if s.closing {
		s.mu.Unlock()
		return errors.New("the daemon is shutting down")
	}
	// An explicit start gets a fresh automatic-restart budget; an automatic
	// restart (no trigger) goes on with its round's.
	if opt.trigger != "" {
		p.retries = 0
	}
	if p.pid != 0 {
		// A server that is stopping starts again once it has exited (see
		// wait), unless a later Stop withdraws the request. The request is
		// recorded under the same lock as the check, so an exit in between
		// cannot miss it.
		p.againTrigger = opt.trigger
		s.reserveLocked(p)
		s.mu.Unlock()
		return nil
	}
	pending := p.want && p.status == Starting
	// A task whose run since this request ended (one a dependent of it in
	// the same bulk start ran) is not run again for it, done or failed.
	done := false
	if p.cfg.Oneshot && !opt.Since.IsZero() && p.pid == 0 && !p.settledAt.Before(opt.Since) {
		state := taskStateLocked(p, opt.Since)
		done = state == taskDone || state == taskFailed
	}
	if pending || done {
		s.mu.Unlock()
		return nil // a start (e.g. waiting for dependencies) is already under way
	}
	// Dependencies come from the definition, so a temporary server shares
	// its origin's dependencies. They are read under the lock that reserves
	// the start, so a config reload cannot mix two versions.
	deps := s.cfg.WithDeps(p.cfg.DependsOn)
	token := s.reserveLocked(p)
	s.mu.Unlock()
	return s.launch(name, deps, opt, token)
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
			dep := s.reserveDependency(d, opt.Since)
			switch {
			case dep.done:
				continue // a run since the request satisfies it
			case dep.failed != "":
				// A run since the request failed: the same request does not
				// run it again.
				s.failStart(name, token, fmt.Sprintf("dependency %s failed: %s", d, dep.failed))
				return
			}
			task := dep.task
			if err := s.startOne(d, StartOptions{trigger: triggerStart}, dep.token); err != nil {
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

// dependency is how reserveDependency left a dependency: a task done or
// failed in a run since the request, or reserved with token.
type dependency struct {
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
	if task := p.cfg.Oneshot; task && p.pid == 0 && !p.settledAt.Before(since) {
		switch taskStateLocked(p, since) {
		case taskDone:
			return dependency{task: true, done: true}
		case taskFailed:
			return dependency{task: true, failed: cmp.Or(p.note, string(p.status))}
		}
	}
	p.retries = 0
	return dependency{task: p.cfg.Oneshot, token: s.reserveLocked(p)}
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
