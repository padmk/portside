package supervisor

import (
	"syscall"
	"time"
)

func waitGroupGone(pgid int, d time.Duration) {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) && syscall.Kill(-pgid, 0) == nil {
		time.Sleep(50 * time.Millisecond)
	}
}

// Terminate signals a process group and escalates until the leader and
// every other member have exited, so a restart never finds the port taken
// by a child that was still shutting down.
func Terminate(pgid int, done <-chan struct{}, grace time.Duration) {
	if grace <= 0 {
		grace = 10 * time.Second
	}
	_ = syscall.Kill(-pgid, syscall.SIGINT)
	if waitGroup(pgid, done, grace) {
		return
	}
	_ = syscall.Kill(-pgid, syscall.SIGTERM)
	if waitGroup(pgid, done, 3*time.Second) {
		return
	}
	_ = syscall.Kill(-pgid, syscall.SIGKILL)
	<-done
	waitGroupGone(pgid, 2*time.Second)
}

// waitGroup waits up to d for the leader to be reaped and the group to empty.
func waitGroup(pgid int, done <-chan struct{}, d time.Duration) bool {
	deadline := time.After(d)
	select {
	case <-done:
	case <-deadline:
		return false
	}
	for syscall.Kill(-pgid, 0) == nil {
		select {
		case <-deadline:
			return false
		case <-time.After(50 * time.Millisecond):
		}
	}
	return true
}

func reapGroup(pgid int) {
	if syscall.Kill(-pgid, 0) != nil {
		return
	}
	_ = syscall.Kill(-pgid, syscall.SIGTERM)
	time.Sleep(2 * time.Second)
	if syscall.Kill(-pgid, 0) == nil {
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
	}
}
