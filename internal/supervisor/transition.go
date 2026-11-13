package supervisor

import (
	"cmp"
	"fmt"
	"strings"
	"time"

	"github.com/padmk/portside/internal/config"
)

// This file holds the transitions of a server's phase (phase.go): the only
// code that assigns proc.ph, which tools/lintcheck enforces. Every change
// goes through transitionLocked under s.mu; what must happen outside the
// lock (launching a start, ending a process group) comes back as effects
// for runEffects.
//
//	from      event            to
//	idle      evRequest        starting
//	starting  evRequest        starting (the newer request)
//	running   evRequest        running, with next when the run is ending
//	idle      evStop           idle (stopped)
//	starting  evStop           idle (stopped)
//	running   evStop           running, stopping (effTerminate)
//	starting  evLaunched       running
//	starting  evLaunchFailed   idle (blocked or crashed), a retry pending
//	running   evExited         running, ended
//	running   evReady          running, ready
//	running   evOverMemory     running, stopping (effTerminate)
//	running   evRestartRun     running, stopping with next (effTerminate)
//	running   evEnded          idle, a restart or retry pending, or
//	                           starting with next (effLaunch)
//	idle      evPending        idle, a restart or retry pending
//	idle      evPendingDue     starting (effLaunch)
//	idle      evCancelPending  idle
//	idle      evSettle         idle with another status
//	any       evAdopt          running
//
// Any other pair leaves the phase as it is.

type event interface{ isEvent() }

// evRequest is a start asked for. after says the live process ended on its
// own (see startAfterRunLocked), so the start follows its run.
type evRequest struct {
	req   startRequest
	after bool
}

// evStop is a Stop, or the daemon's shutdown.
type evStop struct{}

// evLaunched is the process of the start under way, started.
type evLaunched struct{ run *run }

// evLaunchFailed is a start that failed before or as it launched: the
// port is taken (Blocked), or a dependency, the directory or the launch
// failed (Crashed), with the note that says why.
type evLaunchFailed struct {
	status Status
	note   string
}

// evExited is wait's record of the live leader's end at the time at, made
// before it reaps the leader.
type evExited struct{ at time.Time }

// evReady is the live process seen ready.
type evReady struct{}

// evEnded is wait's record of the end of the live run, with the leader's
// exit code and how long it ran.
type evEnded struct {
	code   int
	uptime time.Duration
}

// evOverMemory is a usage sample over memory_limit, with the note the
// crash carries.
type evOverMemory struct{ note string }

// evRestartRun is a restart of the live run for changed files: it stops,
// and req starts it again.
type evRestartRun struct{ req startRequest }

// evPending makes an automatic start of a down server due at at.
type evPending struct {
	kind    pendingKind
	now, at time.Time
}

// evPendingDue is the timer of the pending start pd.
type evPendingDue struct{ pd *pendingStart }

// evCancelPending drops a pending automatic start.
type evCancelPending struct{}

// evSettle gives a down server another status: one the previous daemon
// saved, one an upgrade in place found, or stopped once the port a
// blocked server waited for is free.
type evSettle struct{ status Status }

// evAdopt is a live process taken over from the daemon this one replaced,
// with the start asked for after its run, if any.
type evAdopt struct {
	run   *run
	ready bool
	next  *startRequest
}

func (evRequest) isEvent()       {}
func (evStop) isEvent()          {}
func (evLaunched) isEvent()      {}
func (evLaunchFailed) isEvent()  {}
func (evExited) isEvent()        {}
func (evReady) isEvent()         {}
func (evEnded) isEvent()         {}
func (evOverMemory) isEvent()    {}
func (evRestartRun) isEvent()    {}
func (evPending) isEvent()       {}
func (evPendingDue) isEvent()    {}
func (evCancelPending) isEvent() {}
func (evSettle) isEvent()        {}
func (evAdopt) isEvent()         {}

type effect interface{ isEffect() }

// effLaunch launches the start under way with token: its dependencies
// first, or (direct) the server alone, as an automatic restart does.
type effLaunch struct {
	p      *proc
	deps   []string
	opt    StartOptions
	token  int
	direct bool
}

// effTerminate ends a process group the way Stop does and then closes
// ending.
type effTerminate struct {
	pid    int
	done   chan struct{}
	grace  time.Duration
	ending chan struct{}
}

func (effLaunch) isEffect()    {}
func (effTerminate) isEffect() {}

// runEffects carries out effects; the caller does not hold s.mu.
func (s *Supervisor) runEffects(effs []effect) {
	for _, e := range effs {
		switch e := e.(type) {
		case effLaunch:
			if e.direct {
				_ = s.startOne(e.p, e.opt, e.token)
			} else {
				_ = s.launch(e.p, e.deps, e.opt, e.token)
			}
		case effTerminate:
			terminate(s.sys, e.pid, e.done, e.grace)
			s.endEnding(e.ending)
		}
	}
}

// transitionLocked applies ev to p's phase and returns what the caller
// runs once it has released s.mu.
func (s *Supervisor) transitionLocked(p *proc, ev event) []effect {
	if p.ph == nil {
		p.ph = idle{status: Stopped} // a server just configured
	}
	switch ev := ev.(type) {
	case evRequest:
		s.requestLocked(p, ev)
	case evStop:
		return s.stopLocked(p)
	case evLaunched:
		if _, ok := p.ph.(starting); ok {
			p.ph = running{run: ev.run}
		}
	case evLaunchFailed:
		s.launchFailedLocked(p, ev)
	case evExited:
		if ph, ok := p.ph.(running); ok {
			ph.ended = ev.at
			p.ph = ph
		}
	case evReady:
		if ph, ok := p.ph.(running); ok && !ph.ready {
			ph.ready = true
			p.ph = ph
		}
	case evEnded:
		return s.endedLocked(p, ev)
	case evOverMemory:
		return s.overMemoryStopLocked(p, ev)
	case evRestartRun:
		return s.restartRunLocked(p, ev)
	case evPending:
		s.pendLocked(p, ev)
	case evPendingDue:
		return s.pendingDueLocked(p, ev)
	case evCancelPending:
		if ph, ok := p.ph.(idle); ok && ph.pending != nil {
			p.ph = idle{status: ph.status}
			p.note, _, _ = strings.Cut(p.note, restartingNote)
		}
	case evSettle:
		if _, ok := p.ph.(idle); ok {
			p.ph = idle{status: ev.status, pending: p.pending()}
		}
	case evAdopt:
		p.ph = running{run: ev.run, ready: ev.ready, next: ev.next}
	}
	return nil
}

// requestLocked records a start request: without a live process it is the
// start under way, replacing an earlier one; with one, it is the start
// after the run when the run is ending (stopped, over its memory_limit or
// ended on its own). A start of a run that goes on asks for nothing more.
func (s *Supervisor) requestLocked(p *proc, ev evRequest) {
	p.startSeq++
	req := ev.req
	if req.at.IsZero() {
		req.at = time.Now()
	}
	switch ph := p.ph.(type) {
	case idle, starting:
		p.ph = starting{req: req, token: p.startSeq}
	case running:
		if ev.after && ph.next == nil {
			// The ended run is not ready for anyone waiting: the next one
			// is on its way.
			p.note = ""
		}
		if ev.after || ph.next != nil || ph.stop != nil || (s.closing && !s.handoff) {
			ph.next = &req
			p.ph = ph
		}
	}
	s.changedLocked()
	s.saveLocked()
}

// stopLocked ends what p does: a start under way is dropped, and a live
// process group is ended (effTerminate) unless an end is already under
// way, which a stop for memory_limit then becomes, so the server ends up
// stopped rather than crashed.
func (s *Supervisor) stopLocked(p *proc) []effect {
	p.startSeq++ // a start still in its preflight goes no further
	var effs []effect
	switch ph := p.ph.(type) {
	case idle:
		if ph.status != Stopped {
			p.note, p.settledAt = "", time.Now()
		}
		p.ph = idle{status: Stopped}
	case starting:
		p.note, p.settledAt = "", time.Now()
		p.ph = idle{status: Stopped}
	case running:
		ph.next = nil
		if ph.stop == nil || ph.stop.cause != stopAsked {
			if ph.stop != nil {
				p.note = ""
			}
			ph.stop = &stopRequest{cause: stopAsked}
		}
		p.ph = ph
		if p.ending == nil || isClosed(p.ending) {
			ending := s.beginEndingLocked(p)
			effs = append(effs, effTerminate{pid: ph.run.pid, done: ph.run.done, grace: p.running.Stop, ending: ending})
		}
	}
	s.changedLocked()
	s.saveLocked()
	return effs
}

// launchFailedLocked records a start under way that failed: it is
// reported, and retried after retry_after when the config asks for that.
func (s *Supervisor) launchFailedLocked(p *proc, ev evLaunchFailed) {
	if _, ok := p.ph.(starting); !ok {
		return
	}
	p.ph = idle{status: ev.status}
	p.note = ev.note
	p.settledAt = time.Now()
	s.reportCrashLocked(p, nil, 0)
	s.retryLocked(p)
	s.saveLocked()
	s.changedLocked()
}

// endedLocked decides what follows the end of p's live run, which wait
// has recorded: a start asked for meanwhile runs, a stop leaves it
// stopped, a task's code 0 is done, and an exit or crash follows the
// restart policy (an automatic restart, or reported once nothing restarts
// it, then a retry after retry_after).
func (s *Supervisor) endedLocked(p *proc, ev evEnded) []effect {
	ph, ok := p.ph.(running)
	if !ok {
		return nil
	}
	code, uptime := ev.code, ev.uptime
	stamp := p.exitedAt.Format("15:04:05")
	asked := s.requestedExitLocked(p)
	overMemory := ""
	if ph.stop != nil && ph.stop.cause == stopMemory {
		overMemory = ph.stop.note
	}
	status := Stopped
	var restartIn time.Duration
	again, done := false, false
	switch {
	case asked:
		p.note = ""
		fmt.Fprintf(p.logs, "\r\n\x1b[2m── %s  stopped ──\x1b[0m\r\n", stamp)
		// Started again while it was stopping, with no Stop since.
		again = stopAskedLocked(p) && ph.next != nil && !p.removed && !s.closing
	case overMemory != "" && ph.next != nil && !p.removed && !s.closing:
		// Started again while the memory limit stopped it.
		p.note = ""
		fmt.Fprintf(p.logs, "\r\n\x1b[31m── %s  stopped: %s ──\x1b[0m\r\n", stamp, overMemory)
		again = true
	case overMemory != "":
		// Whatever the exit code, also a task's: the limit ended it.
		status, p.note = Crashed, overMemory
		fmt.Fprintf(p.logs, "\r\n\x1b[31m── %s  crashed: %s ──\x1b[0m\r\n", stamp, overMemory)
		if p.cfg.Restart != config.RestartNo {
			restartIn = s.backoffLocked(p, uptime)
		}
	case code == 0 && p.running.Oneshot:
		// Being done ends the task's crash streak, as readiness does for
		// a server.
		status, p.note, done = Exited, noteDone, true
		fmt.Fprintf(p.logs, "\r\n\x1b[2m── %s  done (code 0) ──\x1b[0m\r\n", stamp)
		p.doneAt = p.settledAt
	case code == 0:
		status, p.note = Exited, "exited on its own"
		fmt.Fprintf(p.logs, "\r\n\x1b[2m── %s  exited (code 0) ──\x1b[0m\r\n", stamp)
		if p.cfg.Restart == config.RestartAlways {
			restartIn = s.backoffLocked(p, uptime)
		}
	default:
		status, p.note = Crashed, fmt.Sprintf("exited with code %d", code)
		fmt.Fprintf(p.logs, "\r\n\x1b[31m── %s  crashed (code %d) ──\x1b[0m\r\n", stamp, code)
		if p.cfg.Restart != config.RestartNo {
			restartIn = s.backoffLocked(p, uptime)
		}
	}
	if !again && !asked && ph.next != nil && !p.removed {
		// Started again after it ended on its own, while its last output
		// drained: run it now instead of what the exit alone would decide.
		// During an upgrade in place the launch waits for its end; the
		// next daemon starts the server, which stays wanted.
		p.note = ""
		p.retries = 0
		again = true
	}
	if again {
		// The start keeps the time it was asked for: a task it waits for
		// that ran since serves it (a bulk start shares that run).
		req := startRequest{trigger: triggerStart, at: time.Now()}
		if ph.next != nil {
			req = *ph.next
			req.trigger = cmp.Or(req.trigger, triggerStart)
			if req.at.IsZero() {
				req.at = time.Now() // handed over by a daemon that did not say when
			}
		}
		p.ph = starting{req: req, token: p.startSeq}
		return []effect{effLaunch{p: p, deps: s.cfg.WithDeps(p.cfg.DependsOn), opt: StartOptions{trigger: req.trigger, Since: req.at}, token: p.startSeq}}
	}
	p.ph = idle{status: status}
	if done {
		s.readyLocked(p, p.exitedAt)
	}
	if status != Crashed && status != Exited {
		return nil
	}
	switch {
	case restartIn > 0:
		now := s.now()
		s.pendLocked(p, evPending{kind: pendingRestart, now: now, at: now.Add(restartIn)})
	case status == Crashed || (p.cfg.Restart == config.RestartAlways && p.retries >= maxRetries):
		// Down for good: a crash nothing restarts, or restart = "always"
		// gave up on a server meant to keep running.
		s.reportCrashLocked(p, &code, uptime)
		if status == Crashed && s.cfg.Notify && s.notifier != nil && s.desktopClients == 0 {
			msg := fmt.Sprintf("Exited with code %d after %s", code, uptime.Round(time.Second))
			if overMemory != "" {
				msg = fmt.Sprintf("Stopped after %s: %s", uptime.Round(time.Second), overMemory)
			}
			name := p.cfg.Name
			go s.notifier(name+" crashed", msg)
		}
		s.retryLocked(p)
	}
	return nil
}

// overMemoryStopLocked ends p's live run for memory_limit, unless it is
// already ending.
func (s *Supervisor) overMemoryStopLocked(p *proc, ev evOverMemory) []effect {
	ph, ok := p.ph.(running)
	if !ok || ph.stop != nil || (p.ending != nil && !isClosed(p.ending)) {
		return nil
	}
	ph.stop = &stopRequest{cause: stopMemory, note: ev.note}
	p.ph = ph
	p.note = ev.note
	ending := s.beginEndingLocked(p)
	return []effect{effTerminate{pid: ph.run.pid, done: ph.run.done, grace: p.running.Stop, ending: ending}}
}

// restartRunLocked stops p's live run with req as the start after it, so
// the server stays wanted throughout and nothing waiting for it sees it
// stopped; a stop asked for before the run ends wins.
func (s *Supervisor) restartRunLocked(p *proc, ev evRestartRun) []effect {
	ph, ok := p.ph.(running)
	if !ok || ph.stop != nil || (p.ending != nil && !isClosed(p.ending)) {
		return nil
	}
	p.startSeq++
	req := ev.req
	if req.at.IsZero() {
		req.at = time.Now()
	}
	ph.stop = &stopRequest{cause: stopAsked}
	ph.next = &req
	p.ph = ph
	ending := s.beginEndingLocked(p)
	s.saveLocked()
	s.changedLocked()
	return []effect{effTerminate{pid: ph.run.pid, done: ph.run.done, grace: p.running.Stop, ending: ending}}
}

// pendLocked makes an automatic start of p, which is down, due at ev.at
// and sets its timer; a start or stop meanwhile cancels it, and so does a
// config that no longer asks for it.
func (s *Supervisor) pendLocked(p *proc, ev evPending) {
	ph, ok := p.ph.(idle)
	if !ok {
		return
	}
	pd := &pendingStart{kind: ev.kind, at: ev.at}
	p.ph = idle{status: ph.status, pending: pd}
	left := pendingNoteLocked(p, ev.now, ev.at)
	s.afterLocked(left, func() func() {
		effs := s.transitionLocked(p, evPendingDue{pd: pd})
		if len(effs) == 0 {
			return nil
		}
		return func() { s.runEffects(effs) }
	})
}

// pendingDueLocked starts p for its pending start pd, unless anything
// replaced it since: an automatic restart goes on with its round and
// starts the server alone; a retry begins a new round, dependencies first.
func (s *Supervisor) pendingDueLocked(p *proc, ev evPendingDue) []effect {
	ph, ok := p.ph.(idle)
	if !ok || ph.pending != ev.pd || s.procs[p.cfg.Name] != p || p.removed || s.closing {
		return nil
	}
	token := p.startSeq
	if ev.pd.kind == pendingRetry {
		p.retries = 0
		req := startRequest{trigger: triggerRetry, at: time.Now()}
		p.ph = starting{req: req, token: token}
		return []effect{effLaunch{p: p, deps: s.cfg.WithDeps(p.cfg.DependsOn), opt: StartOptions{trigger: triggerRetry, Since: req.at}, token: token}}
	}
	p.ph = starting{req: startRequest{at: time.Now()}, token: token}
	return []effect{effLaunch{p: p, token: token, direct: true}}
}
