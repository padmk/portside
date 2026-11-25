package supervisor

import (
	"context"
	"errors"
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
func TestStopDuringRestartWins(t *testing.T) {
	h := fakeHarness(t, config.RestartNo, false)
	if err := h.s.Start(h.name, StartOptions{}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "running", func() bool { return h.live() != nil })
	p := h.live()
	h.w.mu.Lock()
	p.slow = true
	h.w.mu.Unlock()
	h.call(func() { _ = h.s.Restart(h.name, StartOptions{}) })
	waitFor(t, "stopping", func() bool { st, _, _ := h.state(); return st == Stopping })
	h.call(func() { _ = h.s.Stop(h.name) })
	time.Sleep(50 * time.Millisecond) // the Stop waits for the same end
	h.w.mu.Lock()
	h.w.exitLocked(p, 130, true)
	h.w.mu.Unlock()
	h.calls.Wait()
	h.quiet(50 * time.Millisecond)
	st, want, _ := h.state()
	if n := h.launches(); n != 1 || st != Stopped || want {
		t.Fatalf("after Restart then Stop: status %s, want %v, launches %d; expected stopped, 1 launch", st, want, n)
	}
}

// A Start asked for while a Restart waits for the process to exit still
// starts the server once, after it has exited.
func TestStartDuringRestartStartsOnce(t *testing.T) {
	h := fakeHarness(t, config.RestartNo, false)
	if err := h.s.Start(h.name, StartOptions{}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "running", func() bool { return h.live() != nil })
	p := h.live()
	h.w.mu.Lock()
	p.slow = true
	h.w.mu.Unlock()
	h.call(func() { _ = h.s.Restart(h.name, StartOptions{}) })
	waitFor(t, "stopping", func() bool { st, _, _ := h.state(); return st == Stopping })
	if err := h.s.Start(h.name, StartOptions{}); err != nil {
		t.Fatal(err)
	}
	h.w.mu.Lock()
	h.w.exitLocked(p, 130, true)
	h.w.mu.Unlock()
	h.calls.Wait()
	waitFor(t, "relaunch", func() bool { return h.live() != nil })
	h.quiet(50 * time.Millisecond)
	if n := h.launches(); n != 2 {
		t.Fatalf("%d launches, want 2", n)
	}
}

// An automatic restart waits for the crashed run's group to end: an orphan
// that ignores SIGTERM and writes elsewhere than the terminal keeps it
// waiting until the SIGKILL 2 s after the exit.
func TestRestartWaitsForOrphanedGroup(t *testing.T) {
	h := fakeHarness(t, config.RestartOnFailure, false)
	if err := h.s.Start(h.name, StartOptions{}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "running", func() bool { return h.live() != nil })
	old := h.live()
	h.w.mu.Lock()
	old.orphan = true
	h.w.exitLocked(old, 1, true)
	_ = old.tty.Close()
	old.tty = nil
	h.w.mu.Unlock()
	waitFor(t, "restart pending", func() bool { _, _, rp := h.state(); return rp })
	fire := func(d time.Duration) {
		until := h.w.clock().Add(d)
		for tm := h.w.nextTimer(until); tm != nil; tm = h.w.nextTimer(until) {
			go tm.f()
			h.quiet(30 * time.Millisecond)
		}
	}
	fire(1500 * time.Millisecond) // the restart after 1 s, before the SIGKILL
	if n := h.launches(); n != 1 {
		t.Fatalf("the restart launched while pid %d's group (an orphan) still ran", old.pid)
	}
	fire(time.Second)
	waitFor(t, "relaunch", func() bool { return h.launches() == 2 })
	h.w.mu.Lock()
	alive := old.orphan
	h.w.mu.Unlock()
	if alive {
		t.Fatal("the orphan outlived the restart")
	}
}

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
func TestSecondStartJoinsPreflight(t *testing.T) {
	h := fakeHarness(t, config.RestartNo, true)
	if err := h.s.Start(h.name, StartOptions{}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "running", func() bool { return h.live() != nil })
	h.exitLive(t, 1)
	waitFor(t, "crashed", func() bool { st, _, _ := h.state(); return st == Crashed })

	gate := make(chan struct{})
	arrived := make(chan struct{}, 2)
	h.s.SetPortRelease(func(int) {
		arrived <- struct{}{}
		<-gate
	})
	first := make(chan error, 1)
	go func() { first <- h.s.Start(h.name, StartOptions{}) }()
	<-arrived
	h.call(func() { _ = h.s.Start(h.name, StartOptions{}) })
	select {
	case <-arrived:
		t.Fatal("the second Start began a preflight of its own")
	case <-time.After(200 * time.Millisecond):
	}
	close(gate)
	if err := <-first; err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	err := h.s.WaitReady(ctx, h.name, time.Now(), time.Second)
	if f, ok := errors.AsType[*waitFailure](err); ok {
		t.Fatalf("the first Start returned, then WaitReady failed with the earlier run: %s", f.reason)
	}
}

// While an automatic restart is in its preflight the server is starting,
// not a crash that nothing restarts: clients notify a crash and hide Stop
// for that.
func TestAutomaticRestartPreflightIsStarting(t *testing.T) {
	h := fakeHarness(t, config.RestartOnFailure, true)
	if err := h.s.Start(h.name, StartOptions{}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "running", func() bool { return h.live() != nil })
	h.exitLive(t, 1)
	waitFor(t, "restart pending", func() bool { _, _, rp := h.state(); return rp })
	h.gate.close(h.daemon)
	go h.tick()
	h.gate.await(t)
	v := viewOf(h.s, h.name)
	if v.Status != Starting || !v.CanStop() {
		t.Fatalf("automatic restart in preflight shown as status %s, restartPending %v, canStop %v", v.Status, v.RestartPending, v.CanStop())
	}
	h.gate.open(false)
}

// A dependency renamed while a server waits for it is still waited for:
// the start goes on once it is done, under its new name.
func TestRenamedDependencyIsStillWaitedFor(t *testing.T) {
	h := newHarness(t)
	h.name = "db"
	h.m.task = true
	h.port = freeModelPort(t)
	webDir := t.TempDir()
	webPort := freeModelPort(t)
	cfgWith := func(db string) *config.Config {
		h.name = db
		c := h.config()
		c.Servers = append(c.Servers, config.Server{Name: "web", Cmd: "fake", Dir: webDir, Port: webPort, Enabled: true, Restart: config.RestartNo, Stop: time.Hour, DependsOn: []string{db}})
		return c
	}
	h.s = h.newSupervisor(cfgWith("db"))
	t.Cleanup(h.cleanup)
	h.gate.close(h.daemon)
	if err := h.s.Start("web", StartOptions{}); err != nil {
		t.Fatal(err)
	}
	h.gate.await(t) // the task's preflight
	h.s.ApplyRenamed(cfgWith("db2"), "db", "db2")
	h.gate.open(false)
	waitFor(t, "the task runs", func() bool { return h.live() != nil })
	h.quiet(50 * time.Millisecond)
	h.exitLive(t, 0)
	waitFor(t, "web launched", func() bool {
		h.w.mu.Lock()
		defer h.w.mu.Unlock()
		return h.w.launches[webDir] == 1
	})
}

// A Compose start renamed while it waits in its preflight still counts the
// port its own containers publish as its own, under the new name.
func TestRenamedComposeStartKeepsItsPublishedPort(t *testing.T) {
	h := newHarness(t)
	h.name = "db"
	h.port = freeModelPort(t)
	cfgAs := func(name string) *config.Config {
		h.name = name
		c := h.config()
		c.Servers[0].Cmd = "docker compose up"
		return c
	}
	h.s = h.newSupervisor(cfgAs("db"))
	t.Cleanup(h.cleanup)
	h.w.mu.Lock()
	h.w.busy = map[int]bool{h.port: true}
	h.w.mu.Unlock()
	h.s.SetPublishedPorts(func(server string, port int) bool { return server == "db2" && port == h.port })
	h.gate.close(h.daemon)
	h.call(func() { _ = h.s.Start("db", StartOptions{}) })
	h.gate.await(t)
	h.s.ApplyRenamed(cfgAs("db2"), "db", "db2")
	h.gate.open(false)
	h.calls.Wait()
	if v := viewOf(h.s, "db2"); v.Status == Blocked {
		t.Fatalf("the renamed start found its own published port taken: %s", v.Note)
	}
	if n := h.launches(); n != 1 {
		t.Fatalf("%d launches, want 1", n)
	}
}

// A due automatic restart is announced as it begins, and its port counts
// as wanted through the preflight.
func TestDueRestartIsAnnounced(t *testing.T) {
	h := fakeHarness(t, config.RestartOnFailure, true)
	if err := h.s.Start(h.name, StartOptions{}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "running", func() bool { return h.live() != nil })
	h.exitLive(t, 1)
	waitFor(t, "restart pending", func() bool { _, _, rp := h.state(); return rp })
	h.quiet(50 * time.Millisecond)
	ch := h.s.Subscribe()
	defer h.s.Unsubscribe(ch)
	h.gate.close(h.daemon)
	go h.tick()
	h.gate.await(t)
	select {
	case <-ch:
	default:
		t.Fatal("no change announced for the restart in its preflight")
	}
	if !h.s.WantsPort(h.port) {
		t.Fatal("the port of the restart in its preflight does not count as wanted")
	}
	h.gate.open(false)
}
