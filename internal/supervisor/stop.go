package supervisor

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Stop ends a server gracefully: Ctrl-C to its process group, then SIGTERM,
// then SIGKILL. It returns when the process has exited.
func (s *Supervisor) Stop(name string) error {
	s.mu.Lock()
	p, ok := s.procs[name]
	if !ok {
		s.mu.Unlock()
		return fmt.Errorf("unknown server %q", name)
	}
	// The servers and the state are already handed to the new daemon,
	// which would not see this stop; like a start, it waits for the end.
	if s.closing && s.handoff {
		s.mu.Unlock()
		return errors.New("the daemon is upgrading in place; try again in a moment")
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
