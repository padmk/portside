package supervisor

import (
	"fmt"
	"log"
	"time"
)

// A task is a server with oneshot = true: its command runs to completion,
// exit code 0 is success, and servers that depend on it wait for that. A
// task is never Running: it is Starting while it runs, then Exited with
// noteDone, or Crashed after a non-zero exit like any server.

// Notes of a task that ended: with code 0, and while the daemon upgraded in
// place, when the daemon it replaced reaped it and its code is unknown.
const (
	noteDone           = "done"
	noteEndedInHandoff = "ended during the upgrade; exit code unknown"
)

// succeeded reports whether p is a task whose last run exited with code 0.
func (p *proc) succeeded() bool {
	return p.cfg.Oneshot && p.status() == Exited && p.hasExit && p.exitCode == 0
}

// taskState is how far a task got for a waiter.
type taskState int

const (
	taskPending taskState = iota // still to finish
	taskDone                     // a run exited with code 0
	taskFailed                   // failed, was stopped, or nothing will run it
)

// taskStateLocked tells a waiter that asked at since how task p stands. A
// run that ended before since (as one that was done long ago) counts only
// while a start is on its way; a run in progress at since satisfies it, so
// starts asked for together share one run.
func taskStateLocked(p *proc, since time.Time) taskState {
	fresh := !p.settledAt.Before(since)
	switch {
	case !since.IsZero() && !p.doneAt.IsZero() && !p.doneAt.Before(since):
		// A run since the request succeeded, also while a later start runs
		// it again. A wait without a request (zero since) is for the last run.
		return taskDone
	case p.status() == Starting || p.status() == Stopping || p.status() == Running:
		return taskPending
	case fresh && p.succeeded():
		return taskDone
	case fresh && p.status() == Crashed && p.pending() != nil && p.pending().kind == pendingRestart:
		return taskPending // an automatic restart follows
	case fresh && p.status() != Stopped:
		return taskFailed
	case p.wanted():
		return taskPending // a start is on its way
	}
	return taskFailed
}

// taskEndedInHandoffLocked records a task handed off by the daemon this
// one replaced that exited before it could be taken over. Its exit code is
// unknown, so it neither succeeded nor crashed: it is not reported,
// restarted or retried, and it satisfies no one waiting for it.
func (s *Supervisor) taskEndedInHandoffLocked(p *proc, h Handoff) {
	log.Printf("handoff: task %s ended during the upgrade", h.Name)
	s.transitionLocked(p, evSettle{status: Exited})
	p.note = noteEndedInHandoff
	p.hasExit = false
	p.exitedAt = time.Now()
	p.settledAt = p.exitedAt
	p.retries = h.Retries
	fmt.Fprintf(p.logs, "\r\n\x1b[2m── %s  ended during the upgrade (exit code unknown) ──\x1b[0m\r\n", p.exitedAt.Format("15:04:05"))
}
