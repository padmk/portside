package supervisor

import (
	"context"
	"fmt"
	"log"
	"time"
)

// ScheduleInterval is how often the wall clock is compared with the next
// scheduled runs. A short check instead of a timer per run, because timers
// follow the monotonic clock, which on macOS does not advance during sleep.
const ScheduleInterval = 15 * time.Second

// A server with a schedule starts at the times it matches. nextRun is the
// next of those, computed from when the schedule was first seen (or its
// expression changed), then from each run, so a run missed during sleep or
// while the daemon was down happens once at the next check. lastRun is when
// the latest scheduled run began. Both are kept in the state file.

// runSchedules checks the schedules every ScheduleInterval until ctx ends.
func (s *Supervisor) runSchedules(ctx context.Context) {
	tick := time.NewTicker(ScheduleInterval)
	defer tick.Stop()
	for {
		s.checkSchedules()
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// scheduledLocked reports whether p starts from its schedule: a configured
// server with a schedule, not a temporary copy of one.
func scheduledLocked(p *proc) bool {
	return p.cfg.Schedule != nil && p.temp == nil && !p.removed
}

// syncScheduleLocked computes p's next run when its schedule is new or its
// expression changed, and forgets it without a schedule. It reports whether
// anything changed.
func syncScheduleLocked(p *proc, now time.Time) bool {
	if !scheduledLocked(p) {
		if p.scheduleExpr == "" && p.nextRun.IsZero() {
			return false
		}
		p.scheduleExpr, p.nextRun = "", time.Time{}
		return true
	}
	expr := p.cfg.Schedule.String()
	if p.scheduleExpr == expr && !p.nextRun.IsZero() {
		return false
	}
	p.scheduleExpr, p.nextRun = expr, p.cfg.Schedule.Next(now)
	return true
}

// syncSchedulesLocked runs syncScheduleLocked for every server and saves
// the state when a next run changed, so a run due before the next save is
// not lost to a daemon restart.
func (s *Supervisor) syncSchedulesLocked() {
	now := s.now()
	changed := false
	for _, p := range s.procs {
		if syncScheduleLocked(p, now) {
			changed = true
		}
	}
	if changed {
		s.saveLocked()
	}
}

// checkSchedules starts the servers whose scheduled run is due. A server
// that is starting, running or stopping skips the run, noted in its log; a
// disabled one skips it silently. Nothing starts while the daemon shuts
// down or upgrades in place; a run due meanwhile happens at the first check
// after a failed upgrade, or in the daemon that took over.
func (s *Supervisor) checkSchedules() {
	s.mu.Lock()
	if s.closing {
		s.mu.Unlock()
		return
	}
	now := s.now()
	type run struct {
		name  string
		deps  []string
		token int
	}
	var due []run
	changed := false
	for _, name := range s.order {
		p := s.procs[name]
		if p == nil {
			continue
		}
		if syncScheduleLocked(p, now) {
			changed = true
		}
		if p.nextRun.IsZero() || now.Before(p.nextRun) {
			continue
		}
		changed = true
		p.nextRun = p.cfg.Schedule.Next(now)
		switch {
		case !p.cfg.Enabled:
		case p.alive():
			fmt.Fprintf(p.logs, "\r\n\x1b[2m── %s  scheduled run skipped: %s ──\x1b[0m\r\n", now.Format("15:04:05"), p.status)
		default:
			// Reserved together with moving nextRun on, so an upgrade in
			// place starting meanwhile keeps the run: the new daemon starts
			// the wanted server, and a failed upgrade lets it go on.
			// Saved with the reservation: a daemon that ends before the
			// run starts leaves it to the next one (see Recover).
			p.lastRun = now
			p.scheduleReserved = true
			deps, token := s.reserveStartLocked(p, triggerSchedule)
			due = append(due, run{name, deps, token})
		}
	}
	if changed {
		s.saveLocked()
		s.changedLocked()
	}
	s.mu.Unlock()
	for _, r := range due {
		if err := s.launch(r.name, r.deps, StartOptions{trigger: triggerSchedule}, r.token); err != nil {
			log.Printf("schedule: %s: %v", r.name, err)
		}
	}
}
