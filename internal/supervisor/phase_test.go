package supervisor

import (
	"testing"
	"time"

	"github.com/padmk/portside/internal/config"
)

// The tests in this file drive one server through a lifecycle on the fake
// system (fakesys_test.go) at the points where two requests or a request
// and an exit meet.

// fakeHarness is a harness for one server with restart policy restart and,
// with port, a port its start releases (which the preflight gate holds).
func fakeHarness(t *testing.T, restart string, port bool) *harness {
	h := newHarness(t)
	h.name = modelServer
	h.m.restart = restart
	if port {
		h.port = freeModelPort(t)
	}
	h.s = h.newSupervisor(h.config())
	t.Cleanup(h.cleanup)
	return h
}

// state returns the server's status, whether it is wanted and whether an
// automatic start is due.
func (h *harness) state() (Status, bool, bool) {
	h.s.mu.Lock()
	defer h.s.mu.Unlock()
	p := h.s.procs[h.name]
	if p == nil {
		return "", false, false
	}
	return p.status(), p.wanted(), p.pending() != nil
}

// exitLive ends the live leader with code, reapable at once.
func (h *harness) exitLive(t *testing.T, code int) *fakeProc {
	t.Helper()
	p := h.live()
	if p == nil {
		t.Fatal("no live process")
	}
	h.w.mu.Lock()
	h.w.exitLocked(p, code, true)
	h.w.mu.Unlock()
	return p
}

func (h *harness) launches() int {
	h.w.mu.Lock()
	defer h.w.mu.Unlock()
	return h.w.launches[h.dir]
}

// A Stop asked for while a Restart waits for a slow process to exit wins:
// the server stays stopped.
// A Start asked for while a Restart waits for the process to exit still
// starts the server once, after it has exited.
// An automatic restart waits for the crashed run's group to end: an orphan
// that ignores SIGTERM and writes elsewhere than the terminal keeps it
// waiting until the SIGKILL 2 s after the exit.
// An ordinary server's exit with code 0 is not a task's success once the
// config makes it a task.
func TestTaskConfigDoesNotMakeAnExitDone(t *testing.T) {
	h := fakeHarness(t, config.RestartNo, false)
	if err := h.s.Start(h.name, StartOptions{}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "running", func() bool { return h.live() != nil })
	h.exitLive(t, 0)
	waitFor(t, "exited", func() bool { st, _, _ := h.state(); return st == Exited })
	h.m.task = true
	h.s.Apply(h.config())
	if v := viewOf(h.s, h.name); v.Done || v.Display() == DisplayDone {
		t.Fatalf("server never ran as a task, but view says done=%v display=%s", v.Done, v.Display())
	}
	h.s.mu.Lock()
	state := taskStateLocked(h.s.procs[h.name], time.Time{})
	h.s.mu.Unlock()
	if state == taskDone {
		t.Fatal("a dependent would count the ordinary exit as the task done")
	}
}

// listed reports whether the supervisor still lists the server name.
func listed(s *Supervisor, name string) (View, bool) {
	for _, v := range s.Snapshot(19000) {
		if v.Name == name {
			return v, true
		}
	}
	return View{}, false
}

// A server removed from the config while its start is in the preflight
// leaves at once, and the start goes no further.
func TestRemovedDuringPreflightLeaves(t *testing.T) {
	h := fakeHarness(t, config.RestartNo, true)
	h.gate.close(h.daemon)
	h.call(func() { _ = h.s.Start(h.name, StartOptions{}) })
	h.gate.await(t)
	h.s.Apply(&config.Config{Shell: "sh"})
	if v, ok := listed(h.s, h.name); ok {
		t.Fatalf("removed server still listed: status %s removed %v", v.Status, v.Removed)
	}
	h.gate.open(false)
	h.calls.Wait()
	h.quiet(50 * time.Millisecond)
	if n := h.launches(); n != 0 {
		t.Fatalf("the removed server's start launched %d processes", n)
	}
}

// A server removed while it waits for a dependency leaves at once.
func TestRemovedWhileWaitingForDependencyLeaves(t *testing.T) {
	h := newHarness(t)
	h.name = "db"
	h.port = freeModelPort(t)
	webDir := t.TempDir()
	webPort := freeModelPort(t)
	cfgWith := func(web bool) *config.Config {
		c := h.config()
		if web {
			c.Servers = append(c.Servers, config.Server{Name: "web", Cmd: "fake", Dir: webDir, Port: webPort, Enabled: true, Restart: config.RestartNo, Stop: time.Hour, DependsOn: []string{"db"}})
		}
		return c
	}
	h.s = h.newSupervisor(cfgWith(true))
	t.Cleanup(h.cleanup)
	h.gate.close(h.daemon)
	if err := h.s.Start("web", StartOptions{}); err != nil {
		t.Fatal(err)
	}
	h.gate.await(t) // db's preflight
	if v := viewOf(h.s, "web"); v.Status != Starting {
		t.Fatalf("web: %s", v.Status)
	}
	h.s.Apply(cfgWith(false))
	if v, ok := listed(h.s, "web"); ok {
		t.Fatalf("removed server still listed: status %s removed %v note %q", v.Status, v.Removed, v.Note)
	}
	h.gate.open(false)
	h.quiet(100 * time.Millisecond)
	h.w.mu.Lock()
	n := h.w.launches[webDir]
	h.w.mu.Unlock()
	if n != 0 {
		t.Fatalf("the removed server launched %d processes", n)
	}
}

// A second Start during a start's preflight joins it: the first Start's
// caller then waits for the run it started, not the crash before it.
// While an automatic restart is in its preflight the server is starting,
// not a crash that nothing restarts: clients notify a crash and hide Stop
// for that.
