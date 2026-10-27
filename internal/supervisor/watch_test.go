package supervisor

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/padmk/portside/internal/config"
)

// watchSupervisor runs web in a directory with src/app.py, watching
// src/**/*.py. Each start appends a line to $RUNS; the command exits 1
// while src/app.py contains "bad". It returns the directory and $RUNS.
func watchSupervisor(t *testing.T, cmd string) (*Supervisor, string, string) {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "src", "app.py"), "ok")
	runs := filepath.Join(t.TempDir(), "runs")
	w, err := config.ParseWatch([]string{"src/**/*.py"})
	if err != nil {
		t.Fatal(err)
	}
	if cmd == "" {
		cmd = `echo run >> "$RUNS"; if grep -q bad src/app.py; then exit 1; fi; exec sleep 60`
	}
	cfg := &config.Config{Shell: "sh", Port: 19000, Env: map[string]string{"RUNS": runs}, Servers: []config.Server{
		{Name: "web", Cmd: cmd, Dir: dir, Enabled: true, Restart: config.RestartNo, Stop: time.Second, Watch: w},
	}}
	s := New(cfg, t.TempDir())
	s.env.env, s.env.fetched = os.Environ(), time.Now()
	s.watchQuiet = time.Second
	t.Cleanup(s.Shutdown)
	return s, dir, runs
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// waitRuns waits until $RUNS holds want runs, and fails on more.
func waitRuns(t *testing.T, runs string, want int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for runCount(t, runs) < want && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if n := runCount(t, runs); n != want {
		t.Fatalf("%d runs, want %d", n, want)
	}
}

// waitWatchIdle waits until no restart a change began is under way.
func waitWatchIdle(t *testing.T, s *Supervisor) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		s.mu.Lock()
		busy := s.procs["web"].watch != nil && s.procs["web"].watch.restarting
		s.mu.Unlock()
		if !busy {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the restart did not finish")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func logText(s *Supervisor) string {
	lb, _ := s.Logs("web")
	return string(lb.Raw())
}

func triggerOf(s *Supervisor) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.procs["web"].trigger
}

// The first scan only records the files; changes in a burst restart a
// running server once they have gone quiet, with a log line naming them
// and the trigger watch. Files outside the patterns change nothing.
func TestWatchRestartsOnceAfterQuiet(t *testing.T) {
	s, dir, runs := watchSupervisor(t, "")
	if err := s.Start("web", StartOptions{}); err != nil {
		t.Fatal(err)
	}
	waitRuns(t, runs, 1)
	t0 := time.Now()
	s.checkWatches(t0)
	s.checkWatches(t0.Add(time.Second))
	writeFile(t, filepath.Join(dir, "README.md"), "docs")
	writeFile(t, filepath.Join(dir, "src", "node_modules", "x.py"), "dependency")
	s.checkWatches(t0.Add(2 * time.Second))
	s.checkWatches(t0.Add(4 * time.Second))
	time.Sleep(300 * time.Millisecond)
	waitRuns(t, runs, 1)

	writeFile(t, filepath.Join(dir, "src", "app.py"), "changed")
	s.checkWatches(t0.Add(5 * time.Second))
	writeFile(t, filepath.Join(dir, "src", "lib", "b.py"), "new")
	s.checkWatches(t0.Add(5500 * time.Millisecond))
	s.checkWatches(t0.Add(6 * time.Second)) // quiet for only 0.5 s
	time.Sleep(300 * time.Millisecond)
	waitRuns(t, runs, 1)
	s.checkWatches(t0.Add(7 * time.Second))
	waitRuns(t, runs, 2)
	waitWatchIdle(t, s)
	waitStatus(t, s, "web", Starting)
	if log := logText(s); !strings.Contains(log, "restarting: changed src/app.py (+1 more)") || !strings.Contains(log, "stopped") {
		t.Errorf("log: %q", log)
	}
	if tr := triggerOf(s); tr != triggerWatch {
		t.Errorf("trigger %q", tr)
	}
	for i := 8; i < 11; i++ {
		s.checkWatches(t0.Add(time.Duration(i) * time.Second))
	}
	time.Sleep(300 * time.Millisecond)
	waitRuns(t, runs, 2)
}

// A server that crashed starts again once the file is fixed, with a fresh
// round of automatic restarts.
func TestWatchStartsCrashedServer(t *testing.T) {
	s, dir, runs := watchSupervisor(t, "")
	writeFile(t, filepath.Join(dir, "src", "app.py"), "bad")
	if err := s.Start("web", StartOptions{}); err != nil {
		t.Fatal(err)
	}
	waitStatus(t, s, "web", Crashed)
	s.mu.Lock()
	s.procs["web"].retries = 3
	s.mu.Unlock()
	t0 := time.Now()
	s.checkWatches(t0)
	writeFile(t, filepath.Join(dir, "src", "app.py"), "fixed")
	s.checkWatches(t0.Add(time.Second))
	s.checkWatches(t0.Add(2 * time.Second))
	waitRuns(t, runs, 2)
	waitStatus(t, s, "web", Running)
	if log := logText(s); !strings.Contains(log, "starting: changed src/app.py ──") {
		t.Errorf("log: %q", log)
	}
	if v := viewOf(s, "web"); v.Restarts != 0 || triggerOf(s) != triggerWatch || len(v.Watch) != 1 || v.Watch[0] != "src/**/*.py" {
		t.Errorf("view %+v, trigger %q", v, triggerOf(s))
	}
}

// A server the user stopped ignores changes and is not even scanned.
func TestWatchIgnoresStoppedServer(t *testing.T) {
	s, dir, runs := watchSupervisor(t, "")
	if err := s.Start("web", StartOptions{}); err != nil {
		t.Fatal(err)
	}
	waitRuns(t, runs, 1)
	t0 := time.Now()
	s.checkWatches(t0)
	if err := s.Stop("web"); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "src", "app.py"), "changed")
	for i := 1; i < 4; i++ {
		s.checkWatches(t0.Add(time.Duration(i) * time.Second))
	}
	time.Sleep(300 * time.Millisecond)
	waitRuns(t, runs, 1)
	s.mu.Lock()
	st, files := s.procs["web"].status, s.procs["web"].watch.files
	s.mu.Unlock()
	if st != Stopped || files != nil {
		t.Errorf("status %s, scanned %v", st, files)
	}
}

// Changed patterns start over with a baseline instead of restarting.
func TestWatchBaselineAfterPatternChange(t *testing.T) {
	s, dir, runs := watchSupervisor(t, "")
	if err := s.Start("web", StartOptions{}); err != nil {
		t.Fatal(err)
	}
	waitRuns(t, runs, 1)
	t0 := time.Now()
	s.checkWatches(t0)
	writeFile(t, filepath.Join(dir, "pyproject.toml"), "[project]")
	cfg := *s.Config()
	cfg.Servers = []config.Server{cfg.Servers[0]}
	cfg.Servers[0].Watch, _ = config.ParseWatch([]string{"src/**/*.py", "pyproject.toml"})
	s.Apply(&cfg)
	for i := 1; i < 4; i++ {
		s.checkWatches(t0.Add(time.Duration(i) * time.Second))
	}
	time.Sleep(300 * time.Millisecond)
	waitRuns(t, runs, 1)
	if v := viewOf(s, "web"); v.Stale {
		t.Error("changing watch made the server stale")
	}
}

// A change while the restart it began is still stopping the server adds
// no second restart.
func TestWatchRestartsDoNotStack(t *testing.T) {
	s, dir, runs := watchSupervisor(t, `echo run >> "$RUNS"; trap 'sleep 1; exit 0' INT; while :; do sleep 0.1; done`)
	if err := s.Start("web", StartOptions{}); err != nil {
		t.Fatal(err)
	}
	waitRuns(t, runs, 1)
	time.Sleep(200 * time.Millisecond) // the trap is set
	t0 := time.Now()
	s.checkWatches(t0)
	writeFile(t, filepath.Join(dir, "src", "app.py"), "one")
	s.checkWatches(t0.Add(time.Second))
	s.checkWatches(t0.Add(2 * time.Second))
	waitStatus(t, s, "web", Stopping)
	writeFile(t, filepath.Join(dir, "src", "app.py"), "two!")
	s.checkWatches(t0.Add(3 * time.Second))
	waitRuns(t, runs, 2)
	waitWatchIdle(t, s)
	for i := 4; i < 7; i++ {
		s.checkWatches(t0.Add(time.Duration(i) * time.Second))
	}
	time.Sleep(500 * time.Millisecond)
	waitRuns(t, runs, 2)
}

// A task restarts on a change while it runs, and not once it is done.
func TestWatchTask(t *testing.T) {
	s, dir, runs := watchSupervisor(t, `echo run >> "$RUNS"; [ -e "$RUNS.go" ] || exec sleep 60`)
	s.mu.Lock()
	s.procs["web"].cfg.Oneshot = true
	s.mu.Unlock()
	since := time.Now()
	if err := s.Start("web", StartOptions{Since: since}); err != nil {
		t.Fatal(err)
	}
	waitRuns(t, runs, 1)
	// Something waiting for the task (a dependent, ensure) is never told
	// it stopped: the restart keeps it wanted and the next run satisfies it.
	waited := make(chan error, 1)
	go func() { waited <- s.WaitReady(context.Background(), "web", since, 10*time.Second) }()
	t0 := time.Now()
	s.checkWatches(t0)
	writeFile(t, runs+".go", "") // the next run ends at once
	writeFile(t, filepath.Join(dir, "src", "app.py"), "changed")
	s.checkWatches(t0.Add(time.Second))
	s.checkWatches(t0.Add(2 * time.Second))
	waitRuns(t, runs, 2)
	waitStatus(t, s, "web", Exited)
	if err := <-waited; err != nil {
		t.Fatalf("a waiter failed across the watch restart: %v", err)
	}
	if tr := triggerOf(s); tr != triggerWatch {
		t.Errorf("trigger %q", tr)
	}
	waitWatchIdle(t, s)
	writeFile(t, filepath.Join(dir, "src", "app.py"), "again")
	for i := 3; i < 6; i++ {
		s.checkWatches(t0.Add(time.Duration(i) * time.Second))
	}
	time.Sleep(300 * time.Millisecond)
	waitRuns(t, runs, 2)
}

// Patterns matching more files than the limit watch the first ones and
// say so in the note.
func TestWatchFileLimit(t *testing.T) {
	s, dir, runs := watchSupervisor(t, "")
	s.watchLimit = 2
	for _, f := range []string{"b.py", "c.py"} {
		writeFile(t, filepath.Join(dir, "src", f), f)
	}
	if err := s.Start("web", StartOptions{}); err != nil {
		t.Fatal(err)
	}
	waitRuns(t, runs, 1)
	s.checkWatches(time.Now())
	if v := viewOf(s, "web"); !strings.Contains(v.Note, watchNote(2)) {
		t.Errorf("note %q", v.Note)
	}
	s.mu.Lock()
	n := len(s.procs["web"].watch.files)
	s.mu.Unlock()
	if n != 2 {
		t.Errorf("%d files watched, want 2", n)
	}
}
