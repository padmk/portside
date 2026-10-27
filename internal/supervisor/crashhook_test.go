package supervisor

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
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
	old := crashHookTimeout
	crashHookTimeout = 500 * time.Millisecond
	t.Cleanup(func() { crashHookTimeout = old })
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
			runCrashHook(command, os.Environ(), "web")
			close(finished)
		}()
		select {
		case <-finished:
		case <-time.After(10 * time.Second):
			t.Fatalf("%s: the crash command never finished", tail)
		}
	}
}
