package supervisor

import (
	"syscall"
	"time"
)

func waitGroupGone(sys system, pgid int, d time.Duration) {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) && sys.signalGroup(pgid, 0) == nil {
		time.Sleep(50 * time.Millisecond)
	}
}

// Terminate signals a process group and escalates until the leader and
// every other member have exited, so a restart never finds the port taken
// by a child that was still shutting down.
func Terminate(pgid int, done <-chan struct{}, grace time.Duration) {
	terminate(hostSystem{}, pgid, done, grace)
}

func terminate(sys system, pgid int, done <-chan struct{}, grace time.Duration) {
	if pgid <= 1 {
		// -0 is this daemon's own group and -1 every process it may signal.
		return
	}
	if grace <= 0 {
		grace = 10 * time.Second
	}
	_ = sys.signalGroup(pgid, syscall.SIGINT)
	if waitGroup(sys, pgid, done, grace) {
		return
	}
	_ = sys.signalGroup(pgid, syscall.SIGTERM)
	if waitGroup(sys, pgid, done, 3*time.Second) {
		return
	}
	_ = sys.signalGroup(pgid, syscall.SIGKILL)
	<-done
	waitGroupGone(sys, pgid, 2*time.Second)
}

// waitGroup waits up to d for the leader to be reaped and the group to empty.
func waitGroup(sys system, pgid int, done <-chan struct{}, d time.Duration) bool {
	deadline := time.After(d)
	select {
	case <-done:
	case <-deadline:
		return false
	}
	for sys.signalGroup(pgid, 0) == nil {
		select {
		case <-deadline:
			return false
		case <-time.After(50 * time.Millisecond):
		}
	}
	return true
}

// reapGroup ends the members a leader that exited on its own left in its
// group: SIGTERM, then SIGKILL to those still there after 2 s. It returns
// once the group is gone, or 2 s after the SIGKILL.
func reapGroup(sys system, pgid int) {
	if pgid <= 1 || sys.signalGroup(pgid, 0) != nil {
		return
	}
	_ = sys.signalGroup(pgid, syscall.SIGTERM)
	if groupGone(sys, pgid, sys.after(2*time.Second)) {
		return
	}
	_ = sys.signalGroup(pgid, syscall.SIGKILL)
	groupGone(sys, pgid, sys.after(2*time.Second))
}

// groupGone reports whether group pgid empties before deadline fires.
func groupGone(sys system, pgid int, deadline <-chan time.Time) bool {
	for sys.signalGroup(pgid, 0) == nil {
		select {
		case <-deadline:
			return false
		case <-time.After(50 * time.Millisecond):
		}
	}
	return true
}
