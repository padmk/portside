package supervisor

import (
	"os/exec"
	"syscall"
	"testing"
	"time"
)

// A foreign process held for a takeover is signalled only while it is
// the process the daemon identified: once its pid belongs to another
// process (here one that started at another time), the escalation does
// not reach that one.
func TestForeignSignalSparesARecycledPID(t *testing.T) {
	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	fp, ok := openForeign(cmd.Process.Pid)
	if !ok {
		t.Fatal("a running process was not identified")
	}
	defer fp.close()
	if !fp.alive() {
		t.Fatal("the identified process does not count as alive")
	}
	recycled(fp)
	if fp.alive() {
		t.Fatal("a process with another start time counts as the identified one")
	}
	if err := fp.signal(syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	if err := cmd.Process.Signal(syscall.Signal(0)); err != nil {
		t.Fatalf("the process now holding the pid was signalled: %v", err)
	}
}

// Once the identified process has ended, it is no longer alive and
// signalling it is a no-op.
func TestForeignEndedIsNotAlive(t *testing.T) {
	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	fp, ok := openForeign(cmd.Process.Pid)
	if !ok {
		t.Fatal("a running process was not identified")
	}
	defer fp.close()
	if err := fp.signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	_ = cmd.Wait()
	if fp.alive() {
		t.Fatal("an ended process counts as alive")
	}
	if err := fp.signal(syscall.SIGKILL); err != nil {
		t.Fatalf("signalling an ended process: %v", err)
	}
}
