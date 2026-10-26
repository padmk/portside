package supervisor

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/creack/pty"
	"golang.org/x/sys/unix"

	"github.com/padmk/portside/internal/config"
	"github.com/padmk/portside/internal/ports"
)

func TestBuildEnvDisablesComposeMenu(t *testing.T) {
	s := &Supervisor{env: shellEnv{env: []string{"PATH=/bin", "COMPOSE_MENU=true"}, fetched: time.Now()}}
	for _, cmd := range []string{"docker compose up", "docker-compose up", "podman compose up"} {
		env := s.buildEnv(config.Server{Cmd: cmd, Port: 3010, Env: map[string]string{"COMPOSE_MENU": "true"}}, nil, nil)
		if got := lookupEnv(env, "COMPOSE_MENU"); got != "false" {
			t.Errorf("%s: COMPOSE_MENU=%s", cmd, got)
		}
		if got := lookupEnv(env, "PORT"); got != "3010" {
			t.Errorf("PORT=%s", got)
		}
	}
	env := s.buildEnv(config.Server{Cmd: "pnpm dev"}, nil, nil)
	if got := lookupEnv(env, "COMPOSE_MENU"); got != "true" {
		t.Errorf("non-compose COMPOSE_MENU=%s", got)
	}
}

// startGroup runs script in its own process group and returns the group id
// and a channel that closes once the leader has been reaped. The script
// prints "ready" once its traps and children are in place.
func startGroup(t *testing.T, script string) (int, <-chan struct{}) {
	t.Helper()
	cmd := exec.Command("sh", "-c", script)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	// An os.Pipe rather than StdoutPipe: Wait closes the latter once the
	// leader exits, which can come before "ready" is read.
	out, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stdout = w
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	w.Close()
	t.Cleanup(func() { out.Close() })
	pgid := cmd.Process.Pid
	ready := make(chan struct{})
	go func() {
		if line, _ := bufio.NewReader(out).ReadString('\n'); line == "ready\n" {
			close(ready)
		}
		_, _ = io.Copy(io.Discard, out)
	}()
	done := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(done)
	}()
	t.Cleanup(func() {
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
		<-done
	})
	select {
	case <-ready:
	case <-time.After(5 * time.Second):
		t.Fatal("the script did not report ready")
	}
	return pgid, done
}

func requireGroupRunning(t *testing.T, pgid int) {
	t.Helper()
	if syscall.Kill(-pgid, 0) != nil {
		t.Fatal("process group is not running")
	}
}

// requireGroupGone accepts EPERM too: macOS reports it for a group whose
// only members are zombies waiting for launchd to reap them.
func requireGroupGone(t *testing.T, pgid int) {
	t.Helper()
	if err := syscall.Kill(-pgid, 0); err != syscall.ESRCH && err != syscall.EPERM {
		t.Errorf("process group %d still exists after Terminate (kill -0: %v)", pgid, err)
	}
}

func TestTerminateIgnoredInterrupt(t *testing.T) {
	// The leader and its children ignore SIGINT, so SIGTERM is needed.
	pgid, done := startGroup(t, "trap '' INT; sleep 60 & sleep 60 & echo ready; wait")
	requireGroupRunning(t, pgid)
	start := time.Now()
	Terminate(pgid, done, 200*time.Millisecond)
	requireGroupGone(t, pgid)
	select {
	case <-done:
	default:
		t.Error("leader was not reaped")
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("Terminate took %v", d)
	}
}

func TestTerminateChildOutlivesLeader(t *testing.T) {
	// The leader dies on SIGINT; its background child ignores it.
	pgid, done := startGroup(t, `sh -c "trap '' INT; echo ready; exec sleep 60" & wait`)
	requireGroupRunning(t, pgid)
	start := time.Now()
	Terminate(pgid, done, 200*time.Millisecond)
	requireGroupGone(t, pgid)
	if d := time.Since(start); d < 150*time.Millisecond {
		t.Errorf("Terminate returned after %v, before the leftover child was signalled", d)
	}
}

func TestTerminateGracefulExit(t *testing.T) {
	// SIGINT ends the whole group, so nothing waits for the grace period.
	pgid, done := startGroup(t, "echo ready; exec sleep 60")
	requireGroupRunning(t, pgid)
	start := time.Now()
	Terminate(pgid, done, 5*time.Second)
	requireGroupGone(t, pgid)
	if d := time.Since(start); d > time.Second {
		t.Errorf("Terminate took %v for a group that exits on SIGINT", d)
	}
}

func TestTerminateAlreadyExited(t *testing.T) {
	pgid, done := startGroup(t, "echo ready; exit 0")
	<-done
	start := time.Now()
	Terminate(pgid, done, 200*time.Millisecond)
	if d := time.Since(start); d > time.Second {
		t.Errorf("Terminate took %v for a finished group", d)
	}
	requireGroupGone(t, pgid)
}

func TestTerminateIgnoredTermNeedsKill(t *testing.T) {
	if testing.Short() {
		t.Skip("waits out the fixed SIGTERM grace period")
	}
	pgid, done := startGroup(t, "trap '' INT TERM; sleep 60 & sleep 60 & echo ready; wait")
	requireGroupRunning(t, pgid)
	Terminate(pgid, done, 200*time.Millisecond)
	requireGroupGone(t, pgid)
	select {
	case <-done:
	default:
		t.Error("leader was not reaped")
	}
}

func lifecycleSupervisor(t *testing.T, script, restart string) *Supervisor {
	t.Helper()
	cfg := &config.Config{Shell: "sh", Port: 19000, Servers: []config.Server{{Name: "web", Cmd: script, Dir: t.TempDir(), Enabled: true, Restart: restart, Stop: time.Second}}}
	s := New(cfg, t.TempDir())
	s.env.env, s.env.fetched = os.Environ(), time.Now()
	t.Cleanup(s.Shutdown)
	return s
}

func TestReadinessDiagnosticsAndReloadGrace(t *testing.T) {
	s := lifecycleSupervisor(t, "sleep 60", config.RestartNo)
	p := s.procs["web"]
	p.running.Port = 4802
	p.status, p.startedAt = Starting, t0
	s.updateReadinessLocked(p, false, "", "waiting for port 4802", []int{4803}, true, t0.Add(11*time.Second))
	if !strings.Contains(p.note, "listening on 4803, not 4802") {
		t.Fatalf("wrong-port note: %s", p.note)
	}
	s.updateReadinessLocked(p, true, "::1", "", nil, false, t0.Add(12*time.Second))
	if p.status != Running || p.note != "" || len(p.listenPorts) != 0 {
		t.Fatalf("ready state: %+v", p)
	}
	lost := t0.Add(13 * time.Second)
	s.updateReadinessLocked(p, false, "", "waiting for port 4802", nil, false, lost)
	if p.note != "" {
		t.Fatal("hot reload gap immediately degraded")
	}
	s.updateReadinessLocked(p, false, "", "waiting for port 4802", nil, false, lost.Add(listenerLossDelay))
	if p.note != "not listening on :4802" {
		t.Fatal(p.note)
	}
	s.updateReadinessLocked(p, true, "::1", "", nil, false, lost.Add(5*time.Second))
	if p.note != "" {
		t.Fatal("recovery did not clear degraded note")
	}
	p.pid = 1
	t.Cleanup(func() { p.pid = 0 })
	if got := s.Snapshot(19000)[0].URL; got != "http://[::1]:4802" {
		t.Fatal(got)
	}
	if addr, _ := s.ActiveAddress("web"); addr != "[::1]:4802" {
		t.Fatal(addr)
	}
}

func TestWaitReadyRechecksListenerAndHealth(t *testing.T) {
	s := lifecycleSupervisor(t, "sleep 60", config.RestartNo)
	h := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) }))
	addr := h.Listener.Addr().(*net.TCPAddr)
	p := s.procs["web"]
	p.running.Port, p.running.Health, p.status = addr.Port, "/health", Running
	if err := s.WaitReady(context.Background(), "web", time.Second); err == nil || !strings.Contains(err.Error(), "health /health answers 500") {
		t.Fatalf("health error: %v", err)
	}
	h.Close()
	p.running.Health = ""
	if err := s.WaitReady(context.Background(), "web", time.Second); err == nil || !strings.Contains(err.Error(), "not listening on :") {
		t.Fatalf("missing listener: %v", err)
	}
}

func TestIPv6Readiness(t *testing.T) {
	ln, err := net.Listen("tcp6", "[::1]:0")
	if err != nil {
		t.Skip(err)
	}
	h := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "ok") }))
	h.Listener.Close()
	h.Listener = ln
	h.Start()
	defer h.Close()
	port := ln.Addr().(*net.TCPAddr).Port
	if host, reason := probeReady(port, "/health"); host != "::1" {
		t.Fatalf("host %q, reason %q", host, reason)
	}
}

func TestStopCancelsAutomaticRestart(t *testing.T) {
	s := lifecycleSupervisor(t, "printf 'first error\nsecond error\nthird error\n'; exit 7", config.RestartOnFailure)
	if err := s.Start("web", StartOptions{}); err != nil {
		t.Fatal(err)
	}
	err := s.WaitReady(context.Background(), "web", 5*time.Second)
	if err == nil || !strings.Contains(err.Error(), "third error") {
		t.Fatalf("crash detail: %v", err)
	}
	if !s.Snapshot(19000)[0].RestartPending {
		t.Fatal("restart not reported pending")
	}
	if err := s.Stop("web"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1200 * time.Millisecond)
	v := s.Snapshot(19000)[0]
	if v.RestartPending || v.PID != 0 || v.Wanted || v.Status != Stopped {
		t.Fatalf("stop failed: %+v", v)
	}
}

func TestWantsPortWhileStartingOrRunning(t *testing.T) {
	s := lifecycleSupervisor(t, "sleep 60", config.RestartNo)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	s.procs["web"].cfg.Port = port
	if s.WantsPort(port) {
		t.Fatal("stopped server wants its port")
	}
	if err := s.Start("web", StartOptions{}); err != nil {
		t.Fatal(err)
	}
	if !s.WantsPort(port) {
		t.Fatal("starting server does not want its port")
	}
	if err := s.Stop("web"); err != nil {
		t.Fatal(err)
	}
	if s.WantsPort(port) {
		t.Fatal("stopped server still wants its port")
	}
}

func TestRecoverCrashStateAndLogs(t *testing.T) {
	s := lifecycleSupervisor(t, "printf 'persistent failure\n'; exit 9", config.RestartNo)
	if err := s.Start("web", StartOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := s.WaitReady(context.Background(), "web", 5*time.Second); err == nil {
		t.Fatal("expected crash")
	}
	s.Shutdown()
	recovered := New(s.cfg, filepath.Dir(s.state))
	recovered.env.env, recovered.env.fetched = os.Environ(), time.Now()
	t.Cleanup(recovered.Shutdown)
	recovered.Recover(true)
	v := recovered.Snapshot(19000)[0]
	if v.Status != Crashed || v.ExitCode == nil || *v.ExitCode != 9 || v.ExitedAt == nil {
		t.Fatalf("lost crash: %+v", v)
	}
	logs, _ := recovered.Logs("web")
	if raw := string(logs.Raw()); !strings.Contains(raw, "persistent failure") || !strings.Contains(raw, "daemon restarted") {
		t.Fatal(raw)
	}
}

func TestRenamePreservesLiveProcess(t *testing.T) {
	s := lifecycleSupervisor(t, "sleep 60", config.RestartNo)
	if err := s.Start("web", StartOptions{}); err != nil {
		t.Fatal(err)
	}
	before := s.Snapshot(19000)[0]
	cfg := *s.cfg
	cfg.Servers = append([]config.Server(nil), cfg.Servers...)
	cfg.Servers[0].Name = "renamed"
	logs, _ := s.Logs("web")
	s.ApplyRenamed(&cfg, "web", "renamed")
	after := s.Snapshot(19000)[0]
	if after.Name != "renamed" || after.PID != before.PID || !after.Wanted {
		t.Fatalf("lost process: %+v", after)
	}
	if next, _ := s.Logs("renamed"); next != logs {
		t.Fatal("log buffer replaced")
	}
	if err := s.Stop("renamed"); err != nil {
		t.Fatal(err)
	}
	if s.Snapshot(19000)[0].Status != Stopped {
		t.Fatal("renamed exit not tracked")
	}
}

func TestEffectiveLaunchSettingsStale(t *testing.T) {
	s := lifecycleSupervisor(t, "sleep 60", config.RestartNo)
	if err := s.Start("web", StartOptions{}); err != nil {
		t.Fatal(err)
	}
	if s.Snapshot(19000)[0].Stale {
		t.Fatal("fresh launch stale")
	}
	// An adopted server is not stale before the login environment is read.
	s.env.mu.Lock()
	base := s.env.env
	s.env.env = nil
	s.env.mu.Unlock()
	if s.Snapshot(19000)[0].Stale {
		t.Fatal("stale against an unknown login environment")
	}
	s.env.mu.Lock()
	s.env.env = base
	s.env.mu.Unlock()
	cfg := *s.cfg
	cfg.Env = map[string]string{"GLOBAL_TEST_VALUE": "changed"}
	s.Apply(&cfg)
	if !s.Snapshot(19000)[0].Stale {
		t.Fatal("global env change ignored")
	}
	cfg.Env = nil
	cfg.PathPrepend = []string{"/new/bin"}
	s.Apply(&cfg)
	if !s.Snapshot(19000)[0].Stale {
		t.Fatal("PATH change ignored")
	}
	cfg.PathPrepend = nil
	cfg.Shell = "bash"
	s.Apply(&cfg)
	if !s.Snapshot(19000)[0].Stale {
		t.Fatal("shell change ignored")
	}
	cfg.Shell = "sh"
	s.Apply(&cfg)
	s.env.mu.Lock()
	s.env.env = append(s.env.env, "SHELL_REFRESH_TEST=value")
	s.env.mu.Unlock()
	if !s.Snapshot(19000)[0].Stale {
		t.Fatal("login environment refresh ignored")
	}
}

func TestStopWinsDuringPreflight(t *testing.T) {
	s := lifecycleSupervisor(t, "sleep 60", config.RestartNo)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	p := s.procs["web"]
	p.cfg.Port, p.cfg.Dir = port, filepath.Join(t.TempDir(), "missing")
	entered, release := make(chan struct{}), make(chan struct{})
	s.SetPortRelease(func(int) { close(entered); <-release })
	done := make(chan error, 1)
	go func() { done <- s.Start("web", StartOptions{}) }()
	<-entered
	if err := s.Stop("web"); err != nil {
		t.Fatal(err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if v := s.Snapshot(19000)[0]; v.Status != Stopped || v.Wanted || v.PID != 0 {
		t.Fatalf("preflight undid stop: %+v", v)
	}
}

func TestSampleRefreshesStaleExternalOwner(t *testing.T) {
	if _, err := exec.LookPath("lsof"); err != nil {
		if _, err := exec.LookPath("ss"); err != nil {
			t.Skip("no listener owner tool")
		}
	}
	s := lifecycleSupervisor(t, "sleep 60", config.RestartNo)
	h := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer h.Close()
	port := h.Listener.Addr().(*net.TCPAddr).Port
	p := s.procs["web"]
	p.cfg.Port = port
	p.external = &ports.Listener{Port: port, PID: os.Getpid(), Command: "cached"}
	p.externalStart = "stale process identity"
	s.sample(context.Background(), map[string]int{"web": port})
	if p.external == nil || p.external.Command == "cached" || p.external.PID != os.Getpid() || p.externalStart == "stale process identity" {
		t.Fatalf("owner not refreshed: %+v", p.external)
	}
}

// waitLog waits until a server's scrollback contains want.
func waitLog(t *testing.T, s *Supervisor, name, want string) {
	t.Helper()
	lb, _ := s.Logs(name)
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(string(lb.Raw()), want) {
		if time.Now().After(deadline) {
			t.Fatalf("%s output lacks %q: %q", name, want, lb.Raw())
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// A server handed off by the daemon this process replaced keeps running:
// its output flows into the new supervisor, Recover leaves it alone, and
// Stop ends it through the adopted process.
func TestAdoptHandedOffServer(t *testing.T) {
	s := lifecycleSupervisor(t, "unused", config.RestartNo)
	cmd := exec.Command("sh", "-c", `i=0; while :; do i=$((i+1)); echo "tick $i"; sleep 0.1; done`)
	f, err := pty.Start(cmd)
	if err != nil {
		t.Fatal(err)
	}
	pid := cmd.Process.Pid
	t.Cleanup(func() { _ = syscall.Kill(-pid, syscall.SIGKILL) })
	// The handoff carries a descriptor number, as after an exec.
	fd, err := syscall.Dup(int(f.Fd()))
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	h := Handoff{Name: "web", PID: pid, PIDStart: processStart(pid), PTY: fd, Config: s.procs["web"].cfg,
		Status: Running, Want: true, StartedAt: time.Now().Add(-time.Minute), ReadyAt: time.Now().Add(-time.Minute)}
	if got := s.Adopt([]Handoff{h}); len(got) != 1 {
		t.Fatalf("adopted %v", got)
	}
	waitLog(t, s, "web", "tick 3")
	waitLog(t, s, "web", "tick 6")
	s.Recover(true)
	v := s.Snapshot(19000)[0]
	if v.PID != pid || v.Status != Running || !v.Wanted {
		t.Fatalf("after adoption: pid %d status %s wanted %v", v.PID, v.Status, v.Wanted)
	}
	if err := syscall.Kill(pid, 0); err != nil {
		t.Fatalf("Recover ended the adopted process: %v", err)
	}
	if err := s.Stop("web"); err != nil {
		t.Fatal(err)
	}
	if v := s.Snapshot(19000)[0]; v.Status != Stopped || v.PID != 0 {
		t.Fatalf("after stop: status %s pid %d", v.Status, v.PID)
	}
	if err := syscall.Kill(pid, 0); err == nil {
		t.Fatal("the adopted process survived Stop")
	}
}

// Preparing a handoff keeps each live terminal open across exec and refuses
// new launches; cancelling it restores both.
func TestPrepareAndCancelHandoff(t *testing.T) {
	s := lifecycleSupervisor(t, "echo up; sleep 60", config.RestartNo)
	if err := s.Start("web", StartOptions{}); err != nil {
		t.Fatal(err)
	}
	waitLog(t, s, "web", "up")
	cloexec := func(fd int) bool {
		flags, err := unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0)
		if err != nil {
			t.Fatal(err)
		}
		return flags&unix.FD_CLOEXEC != 0
	}
	hs, err := s.PrepareHandoff()
	if err != nil {
		t.Fatal(err)
	}
	if len(hs) != 1 || hs[0].PID != s.procs["web"].pid || hs[0].Config.Cmd != "echo up; sleep 60" {
		t.Fatalf("handoff %+v", hs)
	}
	if err := s.Start("web", StartOptions{}); err == nil {
		t.Error("Start succeeded during a handoff")
	}
	if err := KeepTerminalsOnExec(hs, true); err != nil {
		t.Fatal(err)
	}
	if hs[0].PTY <= 2 || cloexec(hs[0].PTY) {
		t.Errorf("terminal descriptor %d would close on exec", hs[0].PTY)
	}
	if err := KeepTerminalsOnExec(hs, false); err != nil {
		t.Fatal(err)
	}
	if !cloexec(hs[0].PTY) {
		t.Error("the terminal stays open across exec")
	}
	s.CancelHandoff()
	if err := s.Stop("web"); err != nil {
		t.Fatal(err)
	}
}

// After an upgrade, Recover still sees the state the previous daemon left:
// a wanted server waiting for its automatic restart starts again, and a
// server whose handoff fails has its process group ended and is restored.
func TestRecoverAfterHandoffKeepsPreviousState(t *testing.T) {
	pgid, done := startGroup(t, "sleep 60 & echo ready; wait")
	dir, state := t.TempDir(), t.TempDir()
	prev := persisted{
		Running: []string{"web", "api"},
		Groups:  map[string]pidStamp{"api": {PID: pgid, Start: processStart(pgid)}},
		Servers: map[string]exitState{"web": {Status: Crashed, Note: "exit status 3", Code: 3, HasExit: true, At: time.Now()}},
	}
	data, err := json.Marshal(prev)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(state, "state.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Shell: "sh", Port: 19000, Restore: true, Servers: []config.Server{
		{Name: "web", Cmd: "sleep 60", Dir: dir, Enabled: true, Restart: config.RestartOnFailure, Stop: time.Second},
		{Name: "api", Cmd: "sleep 60", Dir: dir, Enabled: true, Restart: config.RestartNo, Stop: time.Second},
	}}
	s := New(cfg, state)
	s.env.env, s.env.fetched = os.Environ(), time.Now()
	t.Cleanup(s.Shutdown)
	// The terminal descriptor is not usable, so the handoff fails.
	h := Handoff{Name: "api", PID: pgid, PIDStart: processStart(pgid), PTY: 1 << 20, Status: Running, Want: true}
	if got := s.Adopt([]Handoff{h}); len(got) != 0 {
		t.Fatalf("adopted %v", got)
	}
	s.Recover(true)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the process group of the failed handoff was not ended")
	}
	for _, v := range s.Snapshot(19000) {
		if !v.Wanted || v.PID == 0 || v.PID == pgid {
			t.Errorf("%s not restored: %+v", v.Name, v)
		}
	}
}
