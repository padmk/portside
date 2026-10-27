package supervisor

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/padmk/portside/internal/app"
	"github.com/padmk/portside/internal/config"
)

// crashHookSupervisor runs script as server web with an on_crash command
// that appends what it was told to a file, returned as the second value.
func crashHookSupervisor(t *testing.T, script, restart string) (*Supervisor, string) {
	t.Helper()
	s := lifecycleSupervisor(t, script, restart)
	out := filepath.Join(t.TempDir(), "calls")
	s.procs["web"].cfg.OnCrash = fmt.Sprintf(`printf '%%s|%%s|%%s|%%s\n' "$%s" "$%s" "$%s" "$%s" >> %s; printf '%%s' "$%s" > %s.tail`,
		app.Env("SERVER"), app.Env("EXIT_CODE"), app.Env("RESTARTS"), app.Env("NOTE"), out, app.Env("LOG_TAIL"), out)
	return s, out
}

// hookCalls waits up to d for at least one call and then a little longer for
// any further ones, and returns them.
func hookCalls(t *testing.T, out string, d time.Duration) []string {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(out); err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	time.Sleep(500 * time.Millisecond)
	data, err := os.ReadFile(out)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSpace(string(data)), "\n")
}

func TestCrashHookRunsOnceRestartsGiveUp(t *testing.T) {
	old := restartDelay
	restartDelay = 10 * time.Millisecond
	t.Cleanup(func() { restartDelay = old })
	s, out := crashHookSupervisor(t, "echo boom; exit 3", config.RestartOnFailure)
	if err := s.Start("web", StartOptions{}); err != nil {
		t.Fatal(err)
	}
	calls := hookCalls(t, out, 15*time.Second)
	want := fmt.Sprintf("web|3|%d|exited with code 3; gave up after %d restarts", maxRetries, maxRetries)
	if len(calls) != 1 || calls[0] != want {
		t.Fatalf("calls = %q, want one %q", calls, want)
	}
	tail, err := os.ReadFile(out + ".tail")
	if err != nil || !strings.Contains(string(tail), "boom") {
		t.Fatalf("log tail = %q, %v", tail, err)
	}
}

func TestCrashHookOnlyForCrashes(t *testing.T) {
	s, out := crashHookSupervisor(t, "sleep 60", config.RestartNo)
	if err := s.Start("web", StartOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := s.Stop("web"); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	s.procs["web"].cfg.Cmd = "exit 0"
	s.mu.Unlock()
	if err := s.Start("web", StartOptions{}); err != nil {
		t.Fatal(err)
	}
	if calls := hookCalls(t, out, 2*time.Second); calls != nil {
		t.Fatalf("a stop or a clean exit ran on_crash: %q", calls)
	}
	s.mu.Lock()
	s.procs["web"].cfg.Cmd = "exit 7"
	s.mu.Unlock()
	if err := s.Start("web", StartOptions{}); err != nil {
		t.Fatal(err)
	}
	if calls := hookCalls(t, out, 5*time.Second); len(calls) != 1 || calls[0] != "web|7|0|exited with code 7" {
		t.Fatalf("calls = %q", calls)
	}
}

func TestCrashHookForFailedStart(t *testing.T) {
	s, out := crashHookSupervisor(t, "sleep 60", config.RestartOnFailure)
	s.procs["web"].cfg.Dir = filepath.Join(t.TempDir(), "missing")
	if err := s.Start("web", StartOptions{}); err == nil {
		t.Fatal("start in a missing directory succeeded")
	}
	calls := hookCalls(t, out, 5*time.Second)
	if len(calls) != 1 || !strings.HasPrefix(calls[0], "web||0|directory not found") {
		t.Fatalf("calls = %q", calls)
	}
}

func TestCrashHookWhenAlwaysGivesUpOnCleanExits(t *testing.T) {
	old := restartDelay
	restartDelay = 10 * time.Millisecond
	t.Cleanup(func() { restartDelay = old })
	s, out := crashHookSupervisor(t, "exit 0", config.RestartAlways)
	if err := s.Start("web", StartOptions{}); err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprintf("web|0|%d|exited on its own; gave up after %d restarts", maxRetries, maxRetries)
	if calls := hookCalls(t, out, 15*time.Second); len(calls) != 1 || calls[0] != want {
		t.Fatalf("calls = %q, want one %q", calls, want)
	}
}

func TestCrashHookWhenThePortIsTaken(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	s, out := crashHookSupervisor(t, "sleep 60", config.RestartNo)
	s.procs["web"].cfg.Port = ln.Addr().(*net.TCPAddr).Port
	if err := s.Start("web", StartOptions{}); err == nil {
		t.Fatal("start on a taken port succeeded")
	}
	if calls := hookCalls(t, out, 5*time.Second); len(calls) != 1 || !strings.HasPrefix(calls[0], "web||0|port ") {
		t.Fatalf("calls = %q", calls)
	}
}

// A crash command that leaves a process in another process group holding
// its output still finishes, also when it runs past its time limit, so it
// never keeps a slot.
func TestCrashHookBackgroundProcessHoldsOutput(t *testing.T) {
	if _, err := exec.LookPath("perl"); err != nil {
		t.Skip("perl not found")
	}
	old := hookTimeout
	hookTimeout = 500 * time.Millisecond
	t.Cleanup(func() { hookTimeout = old })
	for _, tail := range []string{"exit 0", "sleep 10"} {
		pidFile := filepath.Join(t.TempDir(), "pid")
		// The background process leaves the command's process group and
		// keeps its standard output open.
		command := fmt.Sprintf(`perl -e 'setpgrp(0, 0); sleep 20' & echo $! > %s; %s`, pidFile, tail)
		t.Cleanup(func() {
			if data, err := os.ReadFile(pidFile); err == nil {
				if pid, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil && pid > 1 {
					_ = syscall.Kill(-pid, syscall.SIGKILL)
				}
			}
		})
		finished := make(chan struct{})
		go func() {
			runHook("on_crash", command, os.Environ(), "web")
			close(finished)
		}()
		select {
		case <-finished:
		case <-time.After(10 * time.Second):
			t.Fatalf("%s: the crash command never finished", tail)
		}
	}
}

// eventHook sets command key of server web to append the event, trigger,
// streak and what it recovered from to the returned file.
func eventHook(t *testing.T, s *Supervisor, set func(p *proc, command string)) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), "events")
	command := fmt.Sprintf(`printf '%%s|%%s|%%s|%%s\n' "$%s" "$%s" "$%s" "$%s" >> %s`,
		app.Env("EVENT"), app.Env("TRIGGER"), app.Env("STREAK"), app.Env("RECOVERED_FROM"), out)
	s.mu.Lock()
	set(s.procs["web"], command)
	s.mu.Unlock()
	return out
}

// waitCalls waits up to d for n calls in out and returns all of them.
func waitCalls(t *testing.T, out string, n int, d time.Duration) []string {
	t.Helper()
	var calls []string
	for deadline := time.Now().Add(d); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		if data, err := os.ReadFile(out); err == nil {
			if calls = strings.Split(strings.TrimSpace(string(data)), "\n"); len(calls) >= n {
				return calls
			}
		}
	}
	t.Fatalf("calls = %q, want %d", calls, n)
	return nil
}

// Failed starts in a row count up until the server is ready, which
// on_recover reports, and a crash after that starts a new streak.
func TestCrashStreakAndRecovery(t *testing.T) {
	s := lifecycleSupervisor(t, "exit 3", config.RestartNo)
	out := eventHook(t, s, func(p *proc, c string) {
		p.cfg.OnCrash, p.cfg.OnRecover, p.cfg.ReadyAfter = c, c, 100*time.Millisecond
	})
	for range 2 {
		if err := s.Start("web", StartOptions{}); err != nil {
			t.Fatal(err)
		}
		time.Sleep(700 * time.Millisecond)
	}
	s.mu.Lock()
	s.procs["web"].cfg.Cmd = "sleep 60"
	s.mu.Unlock()
	if err := s.Start("web", StartOptions{}); err != nil {
		t.Fatal(err)
	}
	waitCalls(t, out, 3, 5*time.Second)
	if err := s.Stop("web"); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	s.procs["web"].cfg.Cmd = "exit 3"
	s.mu.Unlock()
	if err := s.Start("web", StartOptions{}); err != nil {
		t.Fatal(err)
	}
	calls := waitCalls(t, out, 4, 5*time.Second)
	want := []string{"crashed|start|1|", "crashed|start|2|", "recovered|start||crashed", "crashed|start|1|"}
	if !slices.Equal(calls, want) {
		t.Fatalf("calls = %q, want %q", calls, want)
	}
}

// A running server that stops answering is reported once after
// unready_after, and its recovery once.
func TestUnreadyAndRecoveryReportedOnce(t *testing.T) {
	s := lifecycleSupervisor(t, "sleep 60", config.RestartNo)
	if err := s.Start("web", StartOptions{}); err != nil {
		t.Fatal(err)
	}
	out := eventHook(t, s, func(p *proc, c string) {
		p.cfg.OnUnready, p.cfg.OnRecover, p.cfg.UnreadyAfter = c, c, 30*time.Second
	})
	s.mu.Lock()
	p := s.procs["web"]
	p.status = Running
	lost := time.Now()
	for _, at := range []time.Duration{0, 29 * time.Second, 30 * time.Second, time.Minute} {
		s.updateReadinessLocked(p, false, "", "health /healthz answers 503", nil, false, lost.Add(at))
	}
	s.mu.Unlock()
	waitCalls(t, out, 1, 5*time.Second)
	s.mu.Lock()
	s.updateReadinessLocked(p, true, "127.0.0.1", "", nil, false, lost.Add(2*time.Minute))
	s.updateReadinessLocked(p, true, "127.0.0.1", "", nil, false, lost.Add(3*time.Minute))
	s.mu.Unlock()
	calls := hookCalls(t, out, 5*time.Second)
	if want := []string{"unready|start||", "recovered|start||unready"}; !slices.Equal(calls, want) {
		t.Fatalf("calls = %q, want %q", calls, want)
	}
}

// retry_after starts a server again once its restarts gave up, with a
// fresh round of restarts, until a stop.
func TestRetryAfterGivingUp(t *testing.T) {
	old := restartDelay
	restartDelay = 10 * time.Millisecond
	t.Cleanup(func() { restartDelay = old })
	s := lifecycleSupervisor(t, "exit 3", config.RestartOnFailure)
	out := eventHook(t, s, func(p *proc, c string) { p.cfg.OnCrash, p.cfg.RetryAfter = c, 300*time.Millisecond })
	if err := s.Start("web", StartOptions{}); err != nil {
		t.Fatal(err)
	}
	calls := waitCalls(t, out, 2, 20*time.Second)
	if want := []string{"crashed|start|1|", "crashed|retry|2|"}; !slices.Equal(calls[:2], want) {
		t.Fatalf("calls = %q, want %q first", calls, want)
	}
	if v := s.Snapshot(19000)[0]; !v.RestartPending || !strings.Contains(v.Note, "gave up after 5 restarts, restarting in 300ms") {
		t.Fatalf("view after giving up: pending %v, note %q", v.RestartPending, v.Note)
	}
	if err := s.Stop("web"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	before, _ := os.ReadFile(out)
	time.Sleep(time.Second)
	if after, _ := os.ReadFile(out); len(after) != len(before) {
		t.Fatalf("a stopped server was retried: %q", after)
	}
}

// umask sets the mode of files the server creates; without it the server
// keeps the daemon's.
func TestServerUmask(t *testing.T) {
	s := lifecycleSupervisor(t, "touch made; sleep 60", config.RestartNo)
	for _, tc := range []struct {
		umask string
		mode  os.FileMode
	}{{"0077", 0o600}, {"027", 0o640}} {
		s.mu.Lock()
		p := s.procs["web"]
		p.cfg.Umask = tc.umask
		file := filepath.Join(p.cfg.Dir, "made")
		s.mu.Unlock()
		_ = os.Remove(file)
		if err := s.Start("web", StartOptions{}); err != nil {
			t.Fatal(err)
		}
		var st os.FileInfo
		var err error
		for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
			if st, err = os.Stat(file); err == nil {
				break
			}
		}
		if err != nil {
			t.Fatal(err)
		}
		if st.Mode().Perm() != tc.mode {
			t.Fatalf("umask %s: mode %v, want %v", tc.umask, st.Mode().Perm(), tc.mode)
		}
		if err := s.Stop("web"); err != nil {
			t.Fatal(err)
		}
	}
}

// login_env = false gives servers the daemon's own environment, not the
// login shell's.
func TestOwnEnvironment(t *testing.T) {
	// Written in one rename, so the test never reads a file the shell has
	// created but printf not yet written.
	s := lifecycleSupervisor(t, `printf '%s' "$ONLY_IN_LOGIN_SHELL" > seen.tmp && mv seen.tmp seen; sleep 60`, config.RestartNo)
	s.env.mu.Lock()
	s.env.env = append(os.Environ(), "ONLY_IN_LOGIN_SHELL=yes")
	s.env.mu.Unlock()
	file := filepath.Join(s.procs["web"].cfg.Dir, "seen")
	for _, own := range []bool{false, true} {
		cfg := *s.cfg
		cfg.OwnEnv = own
		s.Apply(&cfg)
		_ = os.Remove(file)
		if err := s.Start("web", StartOptions{}); err != nil {
			t.Fatal(err)
		}
		var data []byte
		var err error
		for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
			if data, err = os.ReadFile(file); err == nil {
				break
			}
		}
		if want := map[bool]string{false: "yes", true: ""}[own]; err != nil || string(data) != want {
			t.Fatalf("own %v: server saw %q (%v), want %q", own, data, err, want)
		}
		if err := s.Stop("web"); err != nil {
			t.Fatal(err)
		}
	}
}
