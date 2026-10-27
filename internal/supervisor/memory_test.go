package supervisor

import (
	"context"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/padmk/portside/internal/config"
)

// fakeMemory makes usage samples report rss bytes for web's process tree
// instead of reading the process table.
func fakeMemory(s *Supervisor) *atomic.Int64 {
	var rss atomic.Int64
	s.processes = func(context.Context) map[int]procSample {
		s.mu.Lock()
		pid := s.procs["web"].pid
		s.mu.Unlock()
		if pid == 0 {
			return nil
		}
		return map[int]procSample{pid: {ppid: 1, rss: rss.Load()}}
	}
	return &rss
}

// memorySupervisor runs slowStop as web with a 2 GB memory_limit, an
// on_crash command and samples it controls.
func memorySupervisor(t *testing.T, restart string) (*Supervisor, *atomic.Int64, string, string) {
	t.Helper()
	return memoryServer(t, slowStop, restart)
}

// memoryServer is memorySupervisor running script.
func memoryServer(t *testing.T, script, restart string) (*Supervisor, *atomic.Int64, string, string) {
	t.Helper()
	s, calls := crashHookSupervisor(t, script, restart)
	ints := calls + ".ints"
	p := s.procs["web"]
	p.cfg.Env = map[string]string{"INTS": ints}
	p.cfg.MemoryLimit = config.Size{Bytes: 2e9}
	return s, fakeMemory(s), calls, ints
}

func sampleUsage(s *Supervisor) { s.check(context.Background(), map[string]int{}, true) }

const memoryNote = "used 2.3 GB, over memory_limit 2 GB"

// A sample over memory_limit ends the group the way Stop does (one Ctrl-C
// for a server that exits on it) and counts as a crash, also with exit
// code 0: on_crash gets the note and the server stays wanted only as its
// restart policy decides.
func TestMemoryLimitStopsAsACrash(t *testing.T) {
	s, rss, calls, ints := memoryServer(t, `trap 'echo int >> "$INTS"; exit 0' INT; echo up; while :; do sleep 0.1; done`, config.RestartNo)
	if err := s.Start("web", StartOptions{}); err != nil {
		t.Fatal(err)
	}
	waitLog(t, s, "web", upLine)
	rss.Store(1.9e9)
	sampleUsage(s)
	if v := viewOf(s, "web"); v.PID == 0 || v.Status == Stopping {
		t.Fatalf("stopped under the limit: %+v", v)
	}
	rss.Store(2.3e9)
	sampleUsage(s)
	if v := viewOf(s, "web"); v.Status != Stopping || v.Note != memoryNote {
		t.Fatalf("over the limit: %s %q", v.Status, v.Note)
	}
	sampleUsage(s) // a second sample during the stop sends nothing more
	waitStatus(t, s, "web", Crashed)
	if v := viewOf(s, "web"); v.Note != memoryNote || v.Wanted {
		t.Fatalf("after the stop: %+v", v)
	}
	if got := hookCalls(t, calls, 5*time.Second); len(got) != 1 || got[0] != "web|0|0|"+memoryNote {
		t.Fatalf("on_crash calls = %q", got)
	}
	if data, _ := os.ReadFile(ints); string(data) != "int\n" {
		t.Fatalf("interrupts = %q, want one", data)
	}
	waitLog(t, s, "web", "crashed: "+memoryNote)
}

// The restart policy applies to a stop for memory_limit, and a changed
// limit applies at the next sample.
func TestMemoryLimitRestartPolicy(t *testing.T) {
	old := restartDelay
	restartDelay = 10 * time.Millisecond
	t.Cleanup(func() { restartDelay = old })
	s, rss, calls, _ := memorySupervisor(t, config.RestartOnFailure)
	if err := s.Start("web", StartOptions{}); err != nil {
		t.Fatal(err)
	}
	waitLog(t, s, "web", upLine)
	first := viewOf(s, "web").PID
	rss.Store(2.3e9)
	sampleUsage(s)
	waitLog(t, s, "web", "crashed: "+memoryNote)
	deadline := time.Now().Add(5 * time.Second)
	for v := viewOf(s, "web"); v.PID == 0 || v.PID == first; v = viewOf(s, "web") {
		if time.Now().After(deadline) {
			t.Fatalf("not restarted: %+v", v)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if v := viewOf(s, "web"); !v.Wanted {
		t.Fatalf("restarted but not wanted: %+v", v)
	}
	if got := hookCalls(t, calls, time.Second); got != nil {
		t.Fatalf("on_crash ran although the server restarted: %q", got)
	}
	s.mu.Lock()
	s.procs["web"].cfg.MemoryLimit = config.Size{}
	s.mu.Unlock()
	sampleUsage(s)
	if v := viewOf(s, "web"); v.PID == 0 || v.Status == Stopping {
		t.Fatalf("stopped with the limit removed: %+v", v)
	}
}

// A Stop during a stop for memory_limit wins: no second Ctrl-C, and the
// server ends stopped, not crashed, with no on_crash run.
func TestStopWinsOverMemoryLimit(t *testing.T) {
	s, rss, calls, ints := memorySupervisor(t, config.RestartAlways)
	if err := s.Start("web", StartOptions{}); err != nil {
		t.Fatal(err)
	}
	waitLog(t, s, "web", upLine)
	rss.Store(2.3e9)
	sampleUsage(s)
	if err := s.Stop("web"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	if v := viewOf(s, "web"); v.Status != Stopped || v.Wanted || v.PID != 0 || v.Note != "" {
		t.Fatalf("after Stop: %+v", v)
	}
	if got := hookCalls(t, calls, time.Second); got != nil {
		t.Fatalf("on_crash ran after a Stop: %q", got)
	}
	if data, _ := os.ReadFile(ints); string(data) != "int\n" {
		t.Fatalf("interrupts = %q, want one", data)
	}
}

// Nothing is stopped for memory while the daemon hands its servers to a
// new binary, nor for a task when no limit is set; a task over its limit
// fails like any crash although it exits with code 0.
func TestMemoryLimitHandoffAndTask(t *testing.T) {
	s, rss, _, _ := memorySupervisor(t, config.RestartNo)
	s.procs["web"].cfg.Oneshot = true
	if err := s.Start("web", StartOptions{}); err != nil {
		t.Fatal(err)
	}
	waitLog(t, s, "web", upLine)
	rss.Store(2.3e9)
	s.mu.Lock()
	s.closing, s.handoff = true, true
	s.mu.Unlock()
	sampleUsage(s)
	if v := viewOf(s, "web"); v.Status != Starting {
		t.Fatalf("stopped during a handoff: %+v", v)
	}
	s.mu.Lock()
	s.closing, s.handoff = false, false
	s.mu.Unlock()
	sampleUsage(s)
	waitStatus(t, s, "web", Crashed)
	if v := viewOf(s, "web"); v.Note != memoryNote || strings.Contains(v.Note, "done") {
		t.Fatalf("task over its limit: %+v", v)
	}
}
