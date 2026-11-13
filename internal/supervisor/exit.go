package supervisor

import (
	"cmp"
	"fmt"
	"os"
	"syscall"
	"time"

	"github.com/padmk/portside/internal/config"
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

// wait reaps the process and decides what happens next. The process is a
// child of this daemon, also when it was adopted from the binary this one
// replaced, so waiting on its pid works either way.
func (s *Supervisor) wait(name string, p *proc, r *run, gen int) {
	code := r.process.wait()
	pgid := r.pid
	s.mu.Lock()
	// Read and recorded under the lock that start checks it under, so a
	// request is ordered against the run's end the same way as its time.
	p.reapedAt = time.Now()
	stopping := s.requestedExitLocked(p) || p.overMemory != ""
	s.mu.Unlock()
	if !stopping {
		// The leader died on its own; children left in its group are orphans.
		// During a Stop, terminate owns the whole group and its grace period.
		go reapGroup(s.sys, pgid)
	}
	// Let the reader drain the last output (often the crash message). An
	// orphan holding the terminal open must not delay the exit forever.
	select {
	case <-r.copied:
	case <-s.sys.after(2 * time.Second):
	}

	s.mu.Lock()
	if p.gen != gen {
		s.mu.Unlock()
		return
	}
	_ = r.pty.Close()
	uptime := time.Since(p.startedAt)
	p.exitedAt = time.Now()
	// The run ended when its process did, before its output drained: a
	// request from after that is not served by it.
	p.settledAt = p.reapedAt
	p.exitCode = code
	p.hasExit = true
	p.cur = nil
	p.cpu, p.mem = 0, 0
	p.listening = false
	close(r.done)
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
		if p.stopping && p.next != nil && !p.removed && !s.closing {
			again = true
			p.status = Starting
		}
	case overMemory != "" && p.next != nil && !p.removed && !s.closing:
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
	if !again && !s.requestedExitLocked(p) && p.next != nil && !p.removed {
		// Started again after it ended on its own, while its last output
		// drained: run it now instead of what the exit alone would decide.
		// During an upgrade in place the launch waits for its end; the
		// next daemon starts the server, which stays wanted.
		p.status = Starting
		p.note = ""
		p.retries = 0
		restartIn = 0
		again = true
	}
	p.pending = nil
	if p.status == Crashed || p.status == Exited {
		// Down for good: a crash nothing restarts, or restart = "always"
		// gave up on a server meant to keep running.
		down := restartIn == 0 && (p.status == Crashed || (p.cfg.Restart == config.RestartAlways && p.retries >= maxRetries))
		retrying := false
		if restartIn > 0 {
			now := s.now()
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
	trigger := triggerStart
	if p.next != nil {
		trigger = cmp.Or(p.next.trigger, triggerStart)
	}
	if again {
		deps = s.cfg.WithDeps(p.cfg.DependsOn)
		p.req = p.next
	}
	p.next = nil
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
		_ = s.launch(name, deps, StartOptions{trigger: trigger}, token)
		return
	}
}

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
	d := min(restartDelay<<p.retries, maxBackoff)
	p.retries++
	return d
}
