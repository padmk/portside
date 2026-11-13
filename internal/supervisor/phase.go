package supervisor

import "time"

// phase is where a server is in its lifecycle: exactly one of idle,
// starting and running; nil, for a server just configured, is idle and
// stopped. Only transition.go assigns it, through
// transitionLocked (tools/lintcheck reports any other assignment), and a
// server's status and whether it is wanted follow from it.
type phase interface{ isPhase() }

// idle: no process runs and no start is under way.
type idle struct {
	status  Status        // Stopped, Crashed, Exited or Blocked
	pending *pendingStart // the automatic restart or retry due, nil for none
}

// starting: a start is under way and no process runs yet. It waits for
// its dependencies, for the previous process group to end, or in its
// preflight (the port, a takeover, the shell environment).
type starting struct {
	req startRequest
	// token is the request's startSeq: a newer request or a Stop changes
	// startSeq, and a start whose token is not current goes no further.
	token int
}

// running: a process runs, or its leader ended and wait has yet to record
// the end.
type running struct {
	run   *run
	ready bool         // readiness was seen: the status is Running
	stop  *stopRequest // the end of the run under way, nil for none
	// ended is when wait reaped the leader; zero before.
	ended time.Time
	// next is the start asked for once the run ends (while it stops, or
	// after it ended on its own), nil for none; wait launches it.
	next *startRequest
}

func (idle) isPhase()     {}
func (starting) isPhase() {}
func (running) isPhase()  {}

// status is p's lifecycle state as clients see it.
func (p *proc) status() Status {
	switch ph := p.ph.(type) {
	case idle:
		return ph.status
	case starting:
		return Starting
	case running:
		switch {
		case ph.stop != nil:
			return Stopping
		case ph.next != nil:
			return Starting // the run ended and the next one is on its way
		case ph.ready:
			return Running
		}
		return Starting
	}
	return Stopped
}

// wanted reports whether the user wants p running: a start is under way,
// a process runs that no stop is ending (memory_limit's ends it as a
// crash), a start follows the run, or an automatic start is due.
func (p *proc) wanted() bool {
	switch ph := p.ph.(type) {
	case idle:
		return ph.pending != nil
	case starting:
		return true
	case running:
		return ph.stop == nil || ph.stop.cause == stopMemory || ph.next != nil
	}
	return false
}

// live returns p's live process, or nil when none runs.
func (p *proc) live() *run {
	if ph, ok := p.ph.(running); ok {
		return ph.run
	}
	return nil
}

// pending returns p's due automatic restart or retry, nil for none.
func (p *proc) pending() *pendingStart {
	if ph, ok := p.ph.(idle); ok {
		return ph.pending
	}
	return nil
}

// startingWith reports whether p's start under way is the request with
// token.
func (p *proc) startingWith(token int) bool {
	ph, ok := p.ph.(starting)
	return ok && ph.token == token
}

// request returns the start under way while no process runs, nil for none.
func (p *proc) request() *startRequest {
	if ph, ok := p.ph.(starting); ok {
		return &ph.req
	}
	return nil
}

// next returns the start asked for once the live run ends, nil for none.
func (p *proc) next() *startRequest {
	if ph, ok := p.ph.(running); ok {
		return ph.next
	}
	return nil
}

// stopping returns the end of the live run under way, nil for none.
func (p *proc) stopping() *stopRequest {
	if ph, ok := p.ph.(running); ok {
		return ph.stop
	}
	return nil
}

// stopAskedLocked reports whether p's live run is ending because a stop
// was asked for.
func stopAskedLocked(p *proc) bool {
	st := p.stopping()
	return st != nil && st.cause == stopAsked
}

// reapedLocked reports whether wait has recorded the end of p's process,
// which still counts as live, and when.
func reapedLocked(p *proc) (time.Time, bool) {
	ph, ok := p.ph.(running)
	if !ok || ph.ended.IsZero() {
		return time.Time{}, false
	}
	return ph.ended, true
}

// processEndedLocked reports whether p's live process has ended: reaped,
// or exited and not reaped yet (a zombie), so a start asked for now comes
// after the run's end. The process's own state says so, not its pid,
// which another process may have by now.
func processEndedLocked(p *proc) bool {
	r := p.live()
	if r == nil {
		return false
	}
	if _, ok := reapedLocked(p); ok {
		return true
	}
	return r.process.exited()
}
