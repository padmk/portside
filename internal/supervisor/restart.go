package supervisor

import (
	"strings"
	"time"
)

// Restart stops a server (if running) and starts it again.
func (s *Supervisor) Restart(name string, opt StartOptions) error {
	if err := s.Stop(name); err != nil {
		return err
	}
	// The stop settles a task after a request time taken before it, which
	// would make the start count the stop as this request's run.
	if !opt.Since.IsZero() {
		opt.Since = time.Now()
	}
	return s.Start(name, opt)
}

// scheduleRestartLocked restarts p at the time at, an automatic restart
// after a crash that continues its round of restarts. A start or stop
// meanwhile cancels it.
func (s *Supervisor) scheduleRestartLocked(p *proc, now, at time.Time) {
	s.transitionLocked(p, evPending{kind: pendingRestart, now: now, at: at})
}

// pendingNoteLocked ends p's note with when its pending restart at the time
// at happens, and returns how long that is from now.
func pendingNoteLocked(p *proc, now, at time.Time) time.Duration {
	left := max(at.Sub(now), 0)
	if left > time.Second {
		left = left.Round(time.Second)
	}
	p.note, _, _ = strings.Cut(p.note, restartingNote)
	p.note += restartingNote + left.String()
	return left
}

// afterLocked calls f under the lock after d, then runs what f returns once
// the lock is released. While an upgrade in place is under way, f waits for
// it to replace this process or fail, so a failed upgrade keeps a pending
// restart.
func (s *Supervisor) afterLocked(d time.Duration, f func() func()) {
	s.sys.afterFunc(d, func() {
		s.mu.Lock()
		if s.closing && s.handoff {
			s.mu.Unlock()
			s.afterLocked(time.Second, f)
			return
		}
		next := f()
		s.mu.Unlock()
		if next != nil {
			next()
		}
	})
}

// retryLocked starts p again after its retry_after, with a fresh round of
// automatic restarts, when it is down for good although its restart policy
// would keep it running; it reports whether a retry is due. A start or stop
// meanwhile cancels it.
func (s *Supervisor) retryLocked(p *proc) bool {
	// During an upgrade in place the retry waits for it to end (afterLocked):
	// a failed upgrade must keep it.
	if !p.cfg.RetriesAfterGivingUp() || p.removed || (s.closing && !s.handoff) {
		return false
	}
	now := s.now()
	s.scheduleRetryLocked(p, now, now.Add(p.cfg.RetryAfter))
	return true
}

// scheduleRetryLocked starts p again at the time at, with a fresh round of
// automatic restarts. A start or stop meanwhile cancels it, and so does a
// config that no longer asks for retries.
func (s *Supervisor) scheduleRetryLocked(p *proc, now, at time.Time) {
	s.transitionLocked(p, evPending{kind: pendingRetry, now: now, at: at})
}

// pendingStart is an automatic start of a server that is down: a restart
// after a crash, which continues its round of restarts, or a retry after
// retry_after, which begins a new round.
type pendingStart struct {
	kind pendingKind
	at   time.Time
}

type pendingKind int

const (
	pendingRestart pendingKind = iota + 1
	pendingRetry
)

// restartingNote starts the part of a note that announces an automatic restart.
const restartingNote = ", restarting in "
