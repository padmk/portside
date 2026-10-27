package supervisor

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/padmk/portside/internal/app"
	"github.com/padmk/portside/internal/config"
)

// fakeClock is a wall clock the test moves.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) set(t time.Time) {
	c.mu.Lock()
	c.t = t
	c.mu.Unlock()
}

// scheduleStart is when the clock of the schedule tests starts.
var scheduleStart = time.Date(2026, 1, 1, 10, 0, 30, 0, time.Local)

func every(t *testing.T, expr string) *config.Schedule {
	t.Helper()
	s, err := config.ParseSchedule(expr)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// scheduleConfig runs job, a task that appends a line to $RUNS, every
// minute; $RUNS is the second value.
func scheduleConfig(t *testing.T) (*config.Config, string) {
	t.Helper()
	runs := filepath.Join(t.TempDir(), "runs")
	cfg := &config.Config{Shell: "sh", Port: 19000, Env: map[string]string{"RUNS": runs}, Servers: []config.Server{
		{Name: "job", Cmd: `echo run >> "$RUNS"`, Dir: t.TempDir(), Enabled: true, Restart: config.RestartNo, Stop: time.Second, Oneshot: true, Schedule: every(t, "* * * * *")},
	}}
	return cfg, runs
}

func scheduleSupervisor(t *testing.T, cfg *config.Config, state string, clock *fakeClock) *Supervisor {
	t.Helper()
	s := newWithClock(cfg, state, clock.now)
	s.env.env, s.env.fetched = os.Environ(), time.Now()
	t.Cleanup(s.Shutdown)
	return s
}

func runCount(t *testing.T, runs string) int {
	t.Helper()
	data, err := os.ReadFile(runs)
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Count(string(data), "run\n")
}

func scheduleTimes(s *Supervisor, name string) (next, last time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.procs[name]
	return p.nextRun, p.lastRun
}

// A due run starts the server with the trigger schedule, which on_crash
// reports for a scheduled task that failed.
func TestScheduledRunStarts(t *testing.T) {
	cfg, runs := scheduleConfig(t)
	out := filepath.Join(t.TempDir(), "calls")
	cfg.Servers[0].Cmd = `echo run >> "$RUNS"; exit 3`
	cfg.Servers[0].OnCrash = fmt.Sprintf(`echo "$%s" >> %s`, app.Env("TRIGGER"), out)
	clock := &fakeClock{t: scheduleStart}
	s := scheduleSupervisor(t, cfg, t.TempDir(), clock)

	s.checkSchedules()
	if n := runCount(t, runs); n != 0 {
		t.Fatalf("a new schedule ran at once (%d runs)", n)
	}
	due := time.Date(2026, 1, 1, 10, 1, 4, 0, time.Local)
	clock.set(due)
	s.checkSchedules()
	waitStatus(t, s, "job", Crashed)
	if n := runCount(t, runs); n != 1 {
		t.Fatalf("%d runs, want 1", n)
	}
	if calls := hookCalls(t, out, 5*time.Second); len(calls) != 1 || calls[0] != triggerSchedule {
		t.Errorf("on_crash calls = %q", calls)
	}
	next, last := scheduleTimes(s, "job")
	if !last.Equal(due) || !next.Equal(time.Date(2026, 1, 1, 10, 2, 0, 0, time.Local)) {
		t.Errorf("last %s, next %s", last, next)
	}
	v := viewOf(s, "job")
	if v.Schedule != "* * * * *" || v.NextRun == nil || !v.NextRun.Equal(next) || v.LastRun == nil || !v.LastRun.Equal(due) {
		t.Errorf("view = %+v", v)
	}
}

// A run due while the server runs is skipped and noted in its log.
func TestScheduledRunSkippedWhileRunning(t *testing.T) {
	cfg, _ := scheduleConfig(t)
	cfg.Servers[0].Oneshot = false
	cfg.Servers[0].Cmd = "sleep 60"
	clock := &fakeClock{t: scheduleStart}
	s := scheduleSupervisor(t, cfg, t.TempDir(), clock)
	if err := s.Start("job", StartOptions{}); err != nil {
		t.Fatal(err)
	}
	waitStatus(t, s, "job", Running)
	s.mu.Lock()
	seq := s.procs["job"].startSeq
	s.mu.Unlock()

	clock.set(time.Date(2026, 1, 1, 10, 1, 4, 0, time.Local))
	s.checkSchedules()
	waitLog(t, s, "job", "scheduled run skipped: running")
	s.mu.Lock()
	again := s.procs["job"].startSeq != seq
	s.mu.Unlock()
	if again {
		t.Error("the skipped run started the server again")
	}
	if next, last := scheduleTimes(s, "job"); !last.IsZero() || !next.Equal(time.Date(2026, 1, 1, 10, 2, 0, 0, time.Local)) {
		t.Errorf("last %s, next %s", last, next)
	}
}

// Runs missed during a gap (sleep, or a daemon that was not running)
// happen once, and the next run is computed from then.
func TestScheduleCatchesUpOnce(t *testing.T) {
	cfg, runs := scheduleConfig(t)
	clock := &fakeClock{t: scheduleStart}
	s := scheduleSupervisor(t, cfg, t.TempDir(), clock)
	s.checkSchedules()

	woke := time.Date(2026, 1, 1, 13, 30, 10, 0, time.Local)
	clock.set(woke)
	s.checkSchedules()
	waitStatus(t, s, "job", Exited)
	s.checkSchedules()
	if n := runCount(t, runs); n != 1 {
		t.Fatalf("%d runs after the gap, want 1", n)
	}
	if next, _ := scheduleTimes(s, "job"); !next.Equal(time.Date(2026, 1, 1, 13, 31, 0, 0, time.Local)) {
		t.Errorf("next %s", next)
	}
}

// The next and latest run survive a daemon restart: a run is not repeated,
// and one due while the daemon was down happens once.
func TestScheduleSurvivesRestart(t *testing.T) {
	cfg, runs := scheduleConfig(t)
	state := t.TempDir()
	due := time.Date(2026, 1, 1, 10, 1, 4, 0, time.Local)
	clock := &fakeClock{t: scheduleStart}
	first := scheduleSupervisor(t, cfg, state, clock)
	first.checkSchedules()
	clock.set(due)
	first.checkSchedules()
	waitStatus(t, first, "job", Exited)
	next, last := scheduleTimes(first, "job")
	first.Shutdown()

	second := scheduleSupervisor(t, cfg, state, clock)
	second.Recover(true)
	if n, l := scheduleTimes(second, "job"); !n.Equal(next) || !l.Equal(last) {
		t.Fatalf("restored next %s, last %s; want %s, %s", n, l, next, last)
	}
	second.checkSchedules()
	if n := runCount(t, runs); n != 1 {
		t.Fatalf("%d runs after the restart, want 1", n)
	}
	second.Shutdown()

	clock.set(time.Date(2026, 1, 1, 10, 5, 2, 0, time.Local))
	third := scheduleSupervisor(t, cfg, state, clock)
	third.Recover(true)
	third.checkSchedules()
	waitStatus(t, third, "job", Exited)
	third.checkSchedules()
	if n := runCount(t, runs); n != 2 {
		t.Fatalf("%d runs after a run was due while the daemon was down, want 2", n)
	}
}

// A schedule added to the config, or changed, counts from then on.
func TestNewScheduleDoesNotRunAtOnce(t *testing.T) {
	cfg, runs := scheduleConfig(t)
	cfg.Servers[0].Schedule = nil
	clock := &fakeClock{t: scheduleStart}
	s := scheduleSupervisor(t, cfg, t.TempDir(), clock)

	clock.set(time.Date(2026, 1, 1, 10, 0, 59, 0, time.Local))
	added, _ := scheduleConfig(t)
	added.Env = cfg.Env
	added.Servers[0].Schedule = every(t, "0 12 * * *")
	s.Apply(added)
	s.checkSchedules()
	if next, _ := scheduleTimes(s, "job"); !next.Equal(time.Date(2026, 1, 1, 12, 0, 0, 0, time.Local)) {
		t.Fatalf("next %s", next)
	}

	clock.set(time.Date(2026, 1, 1, 12, 30, 0, 0, time.Local))
	changed, _ := scheduleConfig(t)
	changed.Env = cfg.Env
	changed.Servers[0].Schedule = every(t, "15 * * * *")
	s.Apply(changed)
	s.checkSchedules()
	if n := runCount(t, runs); n != 0 {
		t.Fatalf("%d runs, want none", n)
	}
	if next, _ := scheduleTimes(s, "job"); !next.Equal(time.Date(2026, 1, 1, 13, 15, 0, 0, time.Local)) {
		t.Errorf("next after the change %s", next)
	}
}

// enabled = false pauses the schedule.
func TestDisabledScheduleDoesNotRun(t *testing.T) {
	cfg, runs := scheduleConfig(t)
	cfg.Servers[0].Enabled = false
	clock := &fakeClock{t: scheduleStart}
	s := scheduleSupervisor(t, cfg, t.TempDir(), clock)
	clock.set(time.Date(2026, 1, 1, 10, 1, 4, 0, time.Local))
	s.checkSchedules()
	time.Sleep(300 * time.Millisecond)
	if n := runCount(t, runs); n != 0 {
		t.Fatalf("%d runs of a disabled server", n)
	}
	if v := viewOf(s, "job"); v.Status != Stopped || v.NextRun != nil || v.LastRun != nil {
		t.Errorf("view = %+v", v)
	}
}

// A temporary copy of a scheduled server never starts from the schedule.
func TestTemporaryCopyIgnoresSchedule(t *testing.T) {
	cfg, runs := scheduleConfig(t)
	clock := &fakeClock{t: scheduleStart}
	s := scheduleSupervisor(t, cfg, t.TempDir(), clock)
	s.mu.Lock()
	s.addTemporaryLocked(cfg.Servers[0], &temporary{Name: "job--wt", Origin: "job", Worktree: t.TempDir()})
	s.reorderLocked()
	s.mu.Unlock()
	s.checkSchedules()

	clock.set(time.Date(2026, 1, 1, 10, 1, 4, 0, time.Local))
	s.checkSchedules()
	waitStatus(t, s, "job", Exited)
	if n := runCount(t, runs); n != 1 {
		t.Fatalf("%d runs, want 1 (the configured server's)", n)
	}
	s.mu.Lock()
	temp := s.procs["job--wt"]
	status, want, next := temp.status, temp.want, temp.nextRun
	s.mu.Unlock()
	if status != Stopped || want || !next.IsZero() {
		t.Errorf("temporary copy: %s, want %v, next %s", status, want, next)
	}
	if v := viewOf(s, "job--wt"); v.Schedule != "" || v.NextRun != nil {
		t.Errorf("temporary view = %+v", v)
	}
}

// A scheduled run that was due but had not begun when the daemon ended
// (it crashed between saving the reservation and starting the run) starts
// in the next daemon, also with restore = false, and keeps its trigger.
func TestReservedScheduledRunStartsAfterACrash(t *testing.T) {
	cfg, runs := scheduleConfig(t)
	cfg.Restore = false
	state := t.TempDir()
	clock := &fakeClock{t: scheduleStart}
	err := writeState(statePath(state), persisted{Running: []string{"job"}, Servers: map[string]exitState{"job": {
		Status: Exited, Trigger: triggerSchedule, Schedule: "* * * * *",
		NextRun: scheduleStart.Add(time.Minute), LastRun: scheduleStart, ScheduleReserved: true,
	}}})
	if err != nil {
		t.Fatal(err)
	}
	s := scheduleSupervisor(t, cfg, state, clock)
	s.Recover(true)
	waitStatus(t, s, "job", Exited)
	if n := runCount(t, runs); n != 1 {
		t.Fatalf("%d runs, want 1", n)
	}
	s.mu.Lock()
	trigger, reserved := s.procs["job"].trigger, s.procs["job"].scheduleReserved
	s.mu.Unlock()
	if trigger != triggerSchedule || reserved {
		t.Errorf("trigger %q, reserved %v", trigger, reserved)
	}
}
