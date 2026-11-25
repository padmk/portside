package supervisor

import (
	"fmt"
	"os"
	"syscall"
	"time"
)

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

// wait records the end of the process, reaps it and records what happens
// next (endedLocked). The process is a child of this daemon, also when it
// was adopted from the binary this one replaced, so waiting on its pid
// works either way.
func (s *Supervisor) wait(p *proc, r *run, gen int) {
	r.process.exitWait()
	pgid := r.pid
	s.mu.Lock()
	// Recorded under the lock that start checks it under, and before the
	// reap, so a request is ordered against the run's end the same way as
	// its time, and one that comes first still finds the zombie.
	exited := time.Now()
	s.transitionLocked(p, evExited{at: exited})
	stopping := s.requestedExitLocked(p) || p.stopping() != nil
	var orphans chan struct{}
	if !stopping {
		// The leader died on its own; children left in its group are
		// orphans, which are ended as a Stop's group is: a start of the
		// server (an automatic restart among them) and a Stop wait for
		// them. During a Stop, terminate owns the whole group and its grace
		// period.
		orphans = s.beginEndingLocked(p, true)
	}
	s.mu.Unlock()
	code := r.process.reap()
	if orphans != nil {
		go func() {
			defer s.endEnding(orphans)
			reapGroup(s.sys, pgid)
		}()
	}
	// Let the reader drain the last output (often the crash message). An
	// orphan holding the terminal open must not delay the exit forever.
	select {
	case <-r.copied:
	case <-s.sys.after(2 * time.Second):
	}

	s.mu.Lock()
	if p.gen != gen || p.live() != r {
		s.mu.Unlock()
		return
	}
	_ = r.pty.Close()
	uptime := time.Since(p.startedAt)
	p.exitedAt = time.Now()
	// The run ended when its process did, before its output drained: a
	// request from after that is not served by it.
	p.settledAt = exited
	p.exitCode = code
	p.hasExit = true
	p.cpu, p.mem = 0, 0
	p.listening = false
	close(r.done)
	effs := s.transitionLocked(p, evEnded{code: code, uptime: uptime})
	name := p.cfg.Name
	removed := p.removed
	s.changedLocked()
	s.saveLocked()
	if removed {
		if q, ok := s.procs[name]; ok && q == p && q.removed && p.live() == nil {
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
	s.mu.Unlock()
	s.runEffects(effs)
}

// requestedExitLocked reports whether p's process was asked to exit: by a
// Stop or by the daemon shutting down. During a handoff the daemon only
// re-executes, so an exit then is the server's own.
func (s *Supervisor) requestedExitLocked(p *proc) bool {
	return stopAskedLocked(p) || (s.closing && !s.handoff)
}

// backoffLocked returns the delay before an automatic restart, or 0 to give up.
func (s *Supervisor) backoffLocked(p *proc, uptime time.Duration) time.Duration {
	if !p.wanted() {
		return 0
	}
	if uptime > time.Minute {
		p.retries = 0
	}
	if p.retries >= maxRetries {
		p.note += fmt.Sprintf("; gave up after %d restarts", maxRetries)
		return 0
	}
	d := min(restartDelay<<p.retries, maxBackoff)
	p.retries++
	return d
}
