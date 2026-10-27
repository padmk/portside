package supervisor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/creack/pty"

	"github.com/padmk/portside/internal/app"
	"github.com/padmk/portside/internal/config"
)

// taskSupervisor runs the task migrate and the servers api and api2, which
// depend on it. $MARK, $SEEN, $SEEN2 and $RUNS name files in a temporary
// directory, returned as the second value.
func taskSupervisor(t *testing.T, migrate string) (*Supervisor, string) {
	t.Helper()
	dir := t.TempDir()
	files := t.TempDir()
	env := map[string]string{}
	for _, k := range []string{"MARK", "SEEN", "SEEN2", "RUNS"} {
		env[k] = filepath.Join(files, strings.ToLower(k))
	}
	cfg := &config.Config{Shell: "sh", Port: 19000, Env: env, Servers: []config.Server{
		{Name: "migrate", Cmd: migrate, Dir: dir, Enabled: true, Restart: config.RestartNo, Stop: time.Second, Oneshot: true},
		// Each copies the task's mark when it starts, so the copy exists
		// only if the task had finished by then.
		{Name: "api", Cmd: `cp "$MARK" "$SEEN"; sleep 60`, Dir: dir, Enabled: true, Restart: config.RestartNo, Stop: time.Second, DependsOn: []string{"migrate"}},
		{Name: "api2", Cmd: `cp "$MARK" "$SEEN2"; sleep 60`, Dir: dir, Enabled: true, Restart: config.RestartNo, Stop: time.Second, DependsOn: []string{"migrate"}},
	}}
	s := New(cfg, t.TempDir())
	s.env.env, s.env.fetched = os.Environ(), time.Now()
	t.Cleanup(s.Shutdown)
	return s, files
}

func viewOf(s *Supervisor, name string) View {
	for _, v := range s.Snapshot(19000) {
		if v.Name == name {
			return v
		}
	}
	return View{}
}

// A server that depends on a task starts once the task exited with code 0,
// although a server without a port would count as running after 1.5 s.
func TestDependentWaitsForTheTask(t *testing.T) {
	s, files := taskSupervisor(t, `sleep 2; touch "$MARK"`)
	s.procs["migrate"].streak = 1
	since := time.Now()
	if err := s.Start("api", StartOptions{Since: since}); err != nil {
		t.Fatal(err)
	}
	if err := s.WaitReady(context.Background(), "api", since, 10*time.Second); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(files, "seen")); err != nil {
		t.Fatal("api started before the task was done")
	}
	v := viewOf(s, "migrate")
	if !v.Oneshot || !v.Done || v.Status != Exited || v.Note != "done" || v.Wanted {
		t.Errorf("migrate = %+v", v)
	}
	if h := v.Health(false); h.Expected || !h.Ready {
		t.Errorf("a done task is %+v", h)
	}
	s.mu.Lock()
	streak := s.procs["migrate"].streak
	s.mu.Unlock()
	if streak != 0 {
		t.Errorf("success left the crash streak at %d", streak)
	}
}

// A task that fails keeps a server that depends on it from starting, and
// the server's note says why.
func TestTaskFailureBlocksTheDependent(t *testing.T) {
	s, files := taskSupervisor(t, `exit 3`)
	since := time.Now()
	if err := s.Start("api", StartOptions{Since: since}); err != nil {
		t.Fatal(err)
	}
	err := s.WaitReady(context.Background(), "api", since, 10*time.Second)
	const want = "dependency migrate failed: exited with code 3"
	if f, ok := errors.AsType[*waitFailure](err); !ok || f.reason != want {
		t.Fatalf("WaitReady = %v, want %q", err, want)
	}
	if v := viewOf(s, "api"); v.Status != Crashed || v.PID != 0 {
		t.Errorf("api = %+v", v)
	}
	if _, err := os.Stat(filepath.Join(files, "seen")); err == nil {
		t.Error("api ran although the task failed")
	}
	// Waiting for the task itself fails too, with its output.
	if err := s.WaitReady(context.Background(), "migrate", since, time.Second); err == nil || !strings.Contains(err.Error(), "exited with code 3") {
		t.Errorf("WaitReady(migrate) = %v", err)
	}
}

// A task that failed in a run since the request fails the next server of
// the same request that depends on it, without running again; each server
// reports it under its own start, whatever the task's start number.
func TestFailedTaskRunIsShared(t *testing.T) {
	s, files := taskSupervisor(t, `echo run >> "$RUNS"; exit 3`)
	// Start numbers that differ from the task's.
	for range 3 {
		_ = s.Stop("api")
		_ = s.Stop("api2")
	}
	since := time.Now() // one time for all, as a bulk start passes
	const want = "dependency migrate failed: exited with code 3"
	for _, name := range []string{"api", "api2"} {
		if err := s.Start(name, StartOptions{Since: since}); err != nil {
			t.Fatal(err)
		}
		err := s.WaitReady(context.Background(), name, since, 10*time.Second)
		if f, ok := errors.AsType[*waitFailure](err); !ok || f.reason != want {
			t.Fatalf("WaitReady(%s) = %v, want %q", name, err, want)
		}
	}
	if err := s.Start("migrate", StartOptions{Since: since}); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(files, "runs"))
	if n := strings.Count(string(data), "run"); n != 1 {
		t.Fatalf("the task ran %d times in one request", n)
	}
}

// Servers started together share one run of the task they depend on, and
// an explicit start of the task runs it again.
func TestStartsShareOneTaskRun(t *testing.T) {
	s, files := taskSupervisor(t, `echo run >> "$RUNS"; touch "$MARK"`)
	since := time.Now() // one time for all, as a bulk start passes
	var wg sync.WaitGroup
	for _, name := range []string{"migrate", "api", "api2"} {
		wg.Go(func() {
			if err := s.Start(name, StartOptions{Since: since}); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	for _, name := range []string{"api", "api2", "migrate"} {
		if err := s.WaitReady(context.Background(), name, since, 10*time.Second); err != nil {
			t.Fatal(err)
		}
	}
	runs := func() int {
		data, _ := os.ReadFile(filepath.Join(files, "runs"))
		return strings.Count(string(data), "run")
	}
	if n := runs(); n != 1 {
		t.Fatalf("the task ran %d times", n)
	}
	since = time.Now()
	if err := s.Start("migrate", StartOptions{Since: since}); err != nil {
		t.Fatal(err)
	}
	if err := s.WaitReady(context.Background(), "migrate", since, 10*time.Second); err != nil {
		t.Fatal(err)
	}
	if n := runs(); n != 2 {
		t.Fatalf("an explicit start ran the task %d times in all, want 2", n)
	}
}

// A restart of a done task runs it again: the stop before the start is not
// a run of the request.
func TestRestartRunsTheTaskAgain(t *testing.T) {
	s, files := taskSupervisor(t, `echo run >> "$RUNS"`)
	runs := func() int {
		data, _ := os.ReadFile(filepath.Join(files, "runs"))
		return strings.Count(string(data), "run")
	}
	for want := 1; want <= 2; want++ {
		since := time.Now() // as the restart route passes
		var err error
		if want == 1 {
			err = s.Start("migrate", StartOptions{Since: since})
		} else {
			err = s.Restart("migrate", StartOptions{Since: since})
		}
		if err != nil {
			t.Fatal(err)
		}
		if err := s.WaitReady(context.Background(), "migrate", since, 10*time.Second); err != nil {
			t.Fatal(err)
		}
		if n := runs(); n != want {
			t.Fatalf("the task ran %d times, want %d", n, want)
		}
	}
}

// A stopped run does not satisfy a server waiting for the task.
func TestStoppedTaskFailsItsWaiters(t *testing.T) {
	s, _ := taskSupervisor(t, `sleep 60`)
	since := time.Now()
	if err := s.Start("api", StartOptions{Since: since}); err != nil {
		t.Fatal(err)
	}
	waitStatus(t, s, "migrate", Starting)
	time.Sleep(300 * time.Millisecond)
	if err := s.Stop("migrate"); err != nil {
		t.Fatal(err)
	}
	err := s.WaitReady(context.Background(), "api", since, 10*time.Second)
	if f, ok := errors.AsType[*waitFailure](err); !ok || f.reason != "dependency migrate failed: stopped" {
		t.Fatalf("WaitReady = %v", err)
	}
}

// A task whose process exits while the daemon upgrades in place has an
// unknown exit code: it is neither done nor crashed, so it is not reported,
// restarted or retried, also when restore brings servers back, and a server
// that was waiting for it fails without running it again.
func TestTaskEndedDuringHandoff(t *testing.T) {
	s, files := taskSupervisor(t, `echo run >> "$RUNS"; sleep 60`)
	out := filepath.Join(t.TempDir(), "calls")
	p := s.procs["migrate"]
	p.cfg.Restart, p.cfg.RetryAfter = config.RestartOnFailure, time.Second
	p.cfg.OnCrash = fmt.Sprintf(`printf '%%s\n' "$%s" >> %s`, app.Env("NOTE"), out)
	s.cfg.Restore = true
	s.previous = persisted{Running: []string{"migrate", "api"}, Saved: time.Now()}
	master, tty, err := pty.Open()
	if err != nil {
		t.Fatal(err)
	}
	tty.Close()
	fd, err := syscall.Dup(int(master.Fd()))
	if err != nil {
		t.Fatal(err)
	}
	master.Close()
	// A process that has exited: its pid has no such start time.
	h := Handoff{Name: "migrate", PID: os.Getpid(), PIDStart: "Mon Jan 2 15:04:05 2006", PTY: fd,
		Config: p.cfg, Status: Starting, Want: true, StartedAt: time.Now().Add(-time.Minute)}
	if got := s.Adopt([]Handoff{h}); len(got) != 0 {
		t.Fatalf("adopted %v", got)
	}
	s.Recover(true)
	time.Sleep(1500 * time.Millisecond) // longer than retry_after
	v := viewOf(s, "migrate")
	if v.Status != Exited || v.Note != noteEndedInHandoff || v.Done || v.Wanted || v.PID != 0 || v.RestartPending {
		t.Errorf("migrate = %+v", v)
	}
	if calls := hookCalls(t, out, time.Second); len(calls) != 0 {
		t.Errorf("on_crash calls: %q", calls)
	}
	if err := s.WaitReady(context.Background(), "migrate", time.Time{}, time.Second); err == nil {
		t.Error("a task that ended during the upgrade counted as done")
	}
	if v := viewOf(s, "api"); v.Status != Crashed || v.Note != "dependency migrate failed: "+noteEndedInHandoff {
		t.Errorf("api = %+v", v)
	}
	if _, err := os.Stat(filepath.Join(files, "runs")); err == nil {
		t.Error("the task ran again after the upgrade")
	}
}
