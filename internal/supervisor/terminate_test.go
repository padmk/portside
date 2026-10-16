package supervisor

import (
	"bufio"
	"io"
	"os/exec"
	"syscall"
	"testing"
	"time"
)

// startGroup runs script in its own process group and returns the group id
// and a channel that closes once the leader has been reaped. The script
// prints "ready" once its traps and children are in place.
func startGroup(t *testing.T, script string) (int, <-chan struct{}) {
	t.Helper()
	cmd := exec.Command("sh", "-c", script)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
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
