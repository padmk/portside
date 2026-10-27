package supervisor

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/creack/pty"

	"github.com/padmk/portside/internal/app"
	"github.com/padmk/portside/internal/config"
	"github.com/padmk/portside/internal/paths"
)

// slowStop is a server that takes a second to exit after Ctrl-C and records
// every Ctrl-C it gets in the file named by $INTS.
const slowStop = `trap 'echo int >> "$INTS"; sleep 1; exit 0' INT; echo up; while :; do sleep 0.1; done`

// upLine is the line the servers above print once their traps are set; the
// command echoed before their output contains "up" too.
const upLine = "\nup\r\n"

func slowStopSupervisor(t *testing.T) (*Supervisor, string) {
	t.Helper()
	s := lifecycleSupervisor(t, slowStop, config.RestartNo)
	ints := filepath.Join(t.TempDir(), "ints")
	s.procs["web"].cfg.Env = map[string]string{"INTS": ints}
	return s, ints
}

func waitStatus(t *testing.T, s *Supervisor, name string, want Status) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		s.mu.Lock()
		st := s.procs[name].status
		s.mu.Unlock()
		if st == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s is %s, not %s; log: %q", name, st, want, s.Snapshot(19000)[0].Tail)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// A Start while the server is stopping takes effect once it has exited;
// a Stop after that Start still wins.
func TestStartWhileStopping(t *testing.T) {
	s, _ := slowStopSupervisor(t)
	if err := s.Start("web", StartOptions{}); err != nil {
		t.Fatal(err)
	}
	waitLog(t, s, "web", upLine)
	first := s.Snapshot(19000)[0].PID
	go func() { _ = s.Stop("web") }()
	waitStatus(t, s, "web", Stopping)
	if err := s.Start("web", StartOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := s.WaitReady(context.Background(), "web", time.Time{}, 5*time.Second); err != nil {
		t.Fatal(err)
	}
	v := s.Snapshot(19000)[0]
	if v.PID == 0 || v.PID == first || !v.Wanted {
		t.Fatalf("not started again: %+v", v)
	}

	go func() { _ = s.Stop("web") }()
	waitStatus(t, s, "web", Stopping)
	if err := s.Start("web", StartOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := s.Stop("web"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	if v := s.Snapshot(19000)[0]; v.PID != 0 || v.Wanted || v.Status != Stopped {
		t.Fatalf("a Stop after the Start did not win: %+v", v)
	}
}

// A Start after the leader exited, while a Stop still waits for the rest of
// its group (a child that may hold the port), starts the server only once
// the whole group is gone.
func TestStartWaitsForTheStoppingGroup(t *testing.T) {
	s, gone := slowStopSupervisor(t)
	s.procs["web"].cfg.Cmd = `trap 'exit 0' INT; (trap '' INT HUP; sleep 4; echo gone >> "$INTS") & echo up; while :; do sleep 0.1; done`
	s.procs["web"].cfg.Stop = 8 * time.Second
	if err := s.Start("web", StartOptions{}); err != nil {
		t.Fatal(err)
	}
	waitLog(t, s, "web", upLine)
	first := s.Snapshot(19000)[0].PID
	stopped := make(chan struct{})
	go func() { _ = s.Stop("web"); close(stopped) }()
	waitStatus(t, s, "web", Stopped)
	if _, err := os.Stat(gone); err == nil {
		t.Fatal("the child exited before the Start; nothing to wait for")
	}
	if err := s.Start("web", StartOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := s.WaitReady(context.Background(), "web", time.Time{}, 10*time.Second); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(gone); err != nil {
		t.Error("started before the old group was gone")
	}
	if v := s.Snapshot(19000)[0]; v.PID == 0 || v.PID == first {
		t.Fatalf("not started again: %+v", v)
	}
	<-stopped
}

// Shutdown leaves a server that a Stop is ending to that Stop: a second
// Ctrl-C would abort a graceful stop such as docker compose's.
func TestShutdownDuringStopSendsOneInterrupt(t *testing.T) {
	s, ints := slowStopSupervisor(t)
	// Ctrl-C is only counted, so the stop goes on to SIGTERM after its
	// grace period and an interrupt meanwhile is seen.
	s.procs["web"].cfg.Cmd = `trap 'echo int >> "$INTS"' INT; trap 'exit 0' TERM; echo up; while :; do sleep 0.1; done`
	if err := s.Start("web", StartOptions{}); err != nil {
		t.Fatal(err)
	}
	waitLog(t, s, "web", upLine)
	stopped := make(chan struct{})
	go func() { _ = s.Stop("web"); close(stopped) }()
	waitStatus(t, s, "web", Stopping)
	s.Shutdown()
	<-stopped
	data, _ := os.ReadFile(ints)
	if n := strings.Count(string(data), "int"); n != 1 {
		t.Fatalf("the server got %d interrupts", n)
	}
}

// Shutdown while a Stop is still ending a server's group, whose leader has
// exited but whose child ignores every signal until SIGKILL, waits for that
// Stop to end the group.
func TestShutdownWaitsForAStoppingGroup(t *testing.T) {
	s, pgid, stopped := stoppingGroup(t)
	s.Shutdown()
	if syscall.Kill(-pgid, 0) == nil {
		t.Error("Shutdown returned while the group was still alive")
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
	}
	<-stopped
}

// An upgrade is refused while a Stop is still ending a server's group, also
// after its leader exited: exec would end the Stop's escalation and leave
// the child running unwatched.
func TestHandoffWaitsForAStoppingGroup(t *testing.T) {
	s, _, stopped := stoppingGroup(t)
	if _, err := s.PrepareHandoff(); err == nil || !strings.Contains(err.Error(), "web is stopping") {
		t.Fatalf("PrepareHandoff = %v, want a refusal while web's group is ending", err)
	}
	<-stopped
	if _, err := s.PrepareHandoff(); err != nil {
		t.Fatalf("PrepareHandoff after the stop: %v", err)
	}
	s.CancelHandoff()
}

// stoppingGroup starts web as a server whose leader exits on Ctrl-C while
// its child ignores every signal until SIGKILL, stops it, and returns the
// group once the leader has exited and the Stop is still ending the child.
func stoppingGroup(t *testing.T) (s *Supervisor, pgid int, stopped chan struct{}) {
	t.Helper()
	s, _ = slowStopSupervisor(t)
	s.procs["web"].cfg.Cmd = `trap 'exit 0' INT; (trap '' INT HUP TERM; while :; do sleep 0.1; done) & echo up; while :; do sleep 0.1; done`
	if err := s.Start("web", StartOptions{}); err != nil {
		t.Fatal(err)
	}
	waitLog(t, s, "web", upLine)
	pgid = s.Snapshot(19000)[0].PID
	stopped = make(chan struct{})
	go func() { _ = s.Stop("web"); close(stopped) }()
	waitStatus(t, s, "web", Stopped)
	if syscall.Kill(-pgid, 0) != nil {
		t.Fatal("the group ended with its leader; nothing to wait for")
	}
	return s, pgid, stopped
}

// A second Stop after the leader exited returns only once the first Stop has
// ended the group.
func TestSecondStopWaitsForTheGroup(t *testing.T) {
	s, pgid, stopped := stoppingGroup(t)
	if err := s.Stop("web"); err != nil {
		t.Fatal(err)
	}
	if syscall.Kill(-pgid, 0) == nil {
		t.Error("the second Stop returned while the group was still alive")
	}
	<-stopped
}

// Removing a server from the config while its group is being ended does not
// let Shutdown or an upgrade forget that group.
func TestRemovedServerStillEndsBeforeShutdown(t *testing.T) {
	s, pgid, stopped := stoppingGroup(t)
	s.Apply(&config.Config{Shell: "sh", Port: 19000})
	if _, err := s.PrepareHandoff(); err == nil {
		t.Error("PrepareHandoff accepted an upgrade while the removed server's group was ending")
	}
	s.Shutdown()
	if syscall.Kill(-pgid, 0) == nil {
		t.Error("Shutdown returned while the removed server's group was still alive")
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
	}
	<-stopped
}

// A state file that cannot be written is reported, so the user learns that
// a stop may not survive a daemon crash, and the report clears once a write
// works again.
func TestStateSaveErrorIsReported(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes into a read-only directory")
	}
	s := lifecycleSupervisor(t, "sleep 60", config.RestartNo)
	dir := filepath.Dir(s.state)
	if err := s.Start("web", StartOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	if err := s.Stop("web"); err != nil {
		t.Fatal(err)
	}
	if s.SaveError() == "" {
		t.Fatal("no save error reported for a read-only state directory")
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := s.Start("web", StartOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveError(); err != "" {
		t.Errorf("save error after a write worked again: %s", err)
	}
}

// A server renamed while a Stop ends its group starts under the new name
// only once that group is gone.
func TestRenamedServerWaitsForItsStoppingGroup(t *testing.T) {
	s, pgid, stopped := stoppingGroup(t)
	cfg := *s.cfg
	sv := s.procs["web"].cfg
	sv.Name = "renamed"
	cfg.Servers = []config.Server{sv}
	s.ApplyRenamed(&cfg, "web", "renamed")
	if err := s.Start("renamed", StartOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := s.WaitReady(context.Background(), "renamed", time.Time{}, 15*time.Second); err != nil {
		t.Fatal(err)
	}
	if syscall.Kill(-pgid, 0) == nil {
		t.Error("the renamed server started while its old group was still alive")
	}
	<-stopped
}

func readState(t *testing.T, s *Supervisor) persisted {
	t.Helper()
	data, err := os.ReadFile(s.state)
	if err != nil {
		t.Fatal(err)
	}
	var st persisted
	if err := json.Unmarshal(data, &st); err != nil {
		t.Fatal(err)
	}
	return st
}

// After Shutdown the state still lists the servers to restore, but not
// their process groups, whose ids may belong to other programs by the next
// start; no restart is announced for a server that crashed just before.
func TestShutdownSavesStateWithoutGroups(t *testing.T) {
	cfg := &config.Config{Shell: "sh", Port: 19000, Servers: []config.Server{
		{Name: "web", Cmd: "sleep 60", Dir: t.TempDir(), Enabled: true, Restart: config.RestartNo, Stop: time.Second},
		{Name: "api", Cmd: "exit 3", Dir: t.TempDir(), Enabled: true, Restart: config.RestartOnFailure, Stop: time.Second},
	}}
	s := New(cfg, t.TempDir())
	s.env.env, s.env.fetched = os.Environ(), time.Now()
	for _, name := range []string{"web", "api"} {
		if err := s.Start(name, StartOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.WaitReady(context.Background(), "api", time.Time{}, 5*time.Second); err == nil {
		t.Fatal("api did not crash")
	}
	if st := readState(t, s); len(st.Groups) != 1 {
		t.Fatalf("groups before shutdown: %v", st.Groups)
	}
	s.Shutdown()
	st := readState(t, s)
	if len(st.Groups) != 0 || len(st.Running) != 2 {
		t.Fatalf("after shutdown: groups %v, running %v", st.Groups, st.Running)
	}
	if note := st.Servers["api"].Note; strings.Contains(note, "restarting") || s.procs["api"].restartPending {
		t.Fatalf("a restart is still announced: %q", note)
	}
}

// recoverGroup writes a state that names group pgid for server web and runs
// Recover in a new supervisor.
func recoverGroup(t *testing.T, g pidStamp, saved, logWritten time.Time) {
	t.Helper()
	state := t.TempDir()
	data, err := json.Marshal(persisted{Groups: map[string]pidStamp{"web": g}, Saved: saved})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(statePath(state), data, 0o600); err != nil {
		t.Fatal(err)
	}
	if !logWritten.IsZero() {
		logFile := filepath.Join(paths.LogDir(state), "web.log")
		if err := os.MkdirAll(filepath.Dir(logFile), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(logFile, []byte("output\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(logFile, logWritten, logWritten); err != nil {
			t.Fatal(err)
		}
	}
	cfg := &config.Config{Shell: "sh", Port: 19000, Servers: []config.Server{{Name: "web", Cmd: "sleep 60", Dir: t.TempDir(), Enabled: true}}}
	s := New(cfg, state)
	t.Cleanup(s.Shutdown)
	s.Recover(false)
}

// The saved group's leader is gone and another program now has a group
// with the same id: Recover leaves it alone.
func TestRecoverSparesRecycledGroup(t *testing.T) {
	pgid, leaderDone := startGroup(t, "sleep 60 & echo ready")
	<-leaderDone
	saved := time.Now().Add(-time.Hour)
	g := pidStamp{PID: pgid, Start: saved.Add(-time.Minute).Format(startLayout)}
	recoverGroup(t, g, saved, saved)
	if syscall.Kill(-pgid, 0) != nil {
		t.Fatal("Recover ended an unrelated process group")
	}
}

// A server's leader is gone after a daemon crash, and its children run on
// in its group: Recover ends them, also those started after the state was
// saved but before the daemon last wrote the server's log.
func TestRecoverEndsOrphanedGroup(t *testing.T) {
	pgid, leaderDone := startGroup(t, "sleep 60 & echo ready; sleep 0.3")
	g := pidStamp{PID: pgid, Start: processStart(pgid)}
	if g.Start == "" {
		t.Fatal("no start time for the leader")
	}
	<-leaderDone
	time.Sleep(1200 * time.Millisecond)
	recoverGroup(t, g, time.Now().Add(-time.Hour), time.Now())
	deadline := time.Now().Add(5 * time.Second)
	for syscall.Kill(-pgid, 0) == nil {
		if time.Now().After(deadline) {
			t.Fatal("Recover left the orphaned group running")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// A server whose process exits while the daemon re-executes counts as a
// crash in the new daemon: its restart policy restarts it whatever restore
// says, or its on_crash command reports it and it stays down, also when
// restore brings back the servers that were running.
func TestExitDuringHandoff(t *testing.T) {
	for _, restore := range []bool{false, true} {
		t.Run(fmt.Sprintf("restore=%v", restore), func(t *testing.T) { exitDuringHandoff(t, restore) })
	}
}

func exitDuringHandoff(t *testing.T, restore bool) {
	dir := t.TempDir()
	out := filepath.Join(t.TempDir(), "calls")
	cfg := &config.Config{Shell: "sh", Port: 19000, Restore: restore, Servers: []config.Server{
		{Name: "web", Cmd: "sleep 60", Dir: dir, Enabled: true, Restart: config.RestartOnFailure, Stop: time.Second},
		{Name: "api", Cmd: "sleep 60", Dir: dir, Enabled: true, Restart: config.RestartNo, Stop: time.Second,
			OnCrash: fmt.Sprintf(`printf '%%s\n' "$%s" >> %s`, app.Env("NOTE"), out)},
	}}
	s := New(cfg, t.TempDir())
	s.env.env, s.env.fetched = os.Environ(), time.Now()
	// The state the previous daemon saved before the upgrade.
	s.previous = persisted{Running: []string{"web", "api"}}
	t.Cleanup(s.Shutdown)
	var hs []Handoff
	for _, name := range []string{"web", "api"} {
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
		hs = append(hs, Handoff{Name: name, PID: os.Getpid(), PIDStart: "Mon Jan 2 15:04:05 2006", PTY: fd,
			Config: s.procs[name].cfg, Status: Running, Want: true, StartedAt: time.Now().Add(-time.Minute)})
	}
	if got := s.Adopt(hs); len(got) != 0 {
		t.Fatalf("adopted %v", got)
	}
	s.Recover(true)
	views := map[string]View{}
	for _, v := range s.Snapshot(19000) {
		views[v.Name] = v
	}
	if v := views["web"]; v.PID == 0 || !v.Wanted {
		t.Errorf("web was not restarted: %+v", v)
	}
	if v := views["api"]; v.PID != 0 || v.Wanted || v.Status != Crashed {
		t.Errorf("api: %+v", v)
	}
	if calls := hookCalls(t, out, 5*time.Second); len(calls) != 1 || calls[0] != "exited during the upgrade" {
		t.Errorf("on_crash calls: %q", calls)
	}
}

// An automatic restart still waiting for its backoff when the daemon
// upgrades in place happens in the new daemon at its time, with the
// restarts its round already used, also with restore = false.
func TestPendingRestartSurvivesUpgrade(t *testing.T) {
	old := restartDelay
	restartDelay = time.Second
	t.Cleanup(func() { restartDelay = old })
	cfg := &config.Config{Shell: "sh", Port: 19000, Servers: []config.Server{{Name: "web", Cmd: "exit 3", Dir: t.TempDir(), Enabled: true, Restart: config.RestartOnFailure, Stop: time.Second}}}
	state := t.TempDir()
	before := New(cfg, state)
	before.env.env, before.env.fetched = os.Environ(), time.Now()
	if err := before.Start("web", StartOptions{}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for v := before.Snapshot(19000)[0]; !v.RestartPending; v = before.Snapshot(19000)[0] {
		if time.Now().After(deadline) {
			t.Fatalf("no restart pending: %+v", v)
		}
		time.Sleep(5 * time.Millisecond)
	}
	if _, err := before.PrepareHandoff(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { before.CancelHandoff(); before.Shutdown() })

	after := New(cfg, state)
	after.env.env, after.env.fetched = os.Environ(), time.Now()
	t.Cleanup(after.Shutdown)
	after.Adopt(nil)
	after.Recover(true)
	after.mu.Lock()
	p := after.procs["web"]
	pending, retries, note := p.restartPending, p.retries, p.note
	after.mu.Unlock()
	if !pending || retries != 1 || !strings.Contains(note, "restarting in") {
		t.Fatalf("after the upgrade: pending %v, retries %d, note %q", pending, retries, note)
	}
	// The restart happens and its crash continues the round.
	deadline = time.Now().Add(5 * time.Second)
	for {
		after.mu.Lock()
		retries = p.retries
		after.mu.Unlock()
		if retries >= 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the pending restart did not happen: retries %d", retries)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
