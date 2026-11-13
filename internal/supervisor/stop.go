package supervisor

import (
	"errors"
	"fmt"
	"sync"
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
	p.retries = 0
	effs := s.transitionLocked(p, evStop{})
	// Without an end of its own to wait for, a stop waits for the one
	// under way: a second Ctrl-C would make tools such as docker compose
	// abort their own graceful shutdown. That is also one still ending the
	// group after its leader exited.
	ending := p.ending
	s.mu.Unlock()
	if len(effs) > 0 {
		s.runEffects(effs)
		return nil
	}
	if ending != nil {
		<-ending
	}
	return nil
}

// stopRequest is the end of a server's live run under way: asked for (a
// Stop, a restart for changed files or the daemon's shutdown), or because
// a usage sample was over memory_limit, whose note the exit carries as a
// crash.
type stopRequest struct {
	cause stopCause
	note  string // why memory_limit stopped it
}

type stopCause int

const (
	stopAsked stopCause = iota + 1
	stopMemory
)

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
	st := s.stateLocked()
	for name, p := range s.procs {
		// No automatic restart follows a shutdown; the server stays among
		// those to restore.
		if p.pending() != nil {
			s.transitionLocked(p, evCancelPending{})
			if e, ok := st.Servers[name]; ok {
				e.RetryAt, e.RestartAt, e.Retries = time.Time{}, time.Time{}, 0
				e.Note = p.note
				st.Servers[name] = e
			}
		}
	}
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
		switch {
		case p.request() != nil:
			s.transitionLocked(p, evStop{}) // a start under way goes no further
		case p.live() == nil || (p.ending != nil && !isClosed(p.ending)):
		default:
			effs := s.transitionLocked(p, evStop{})
			wg.Go(func() { s.runEffects(effs) })
		}
	}
	s.changedLocked()
	s.mu.Unlock()
	wg.Wait()
	s.mu.Lock()
	for name, g := range st.Groups {
		if p := s.procs[name]; (p == nil || p.live() == nil) && s.sys.signalGroup(g.PID, 0) != nil {
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
