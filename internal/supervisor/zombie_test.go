package supervisor

import (
	"os/exec"
	"testing"
	"time"
)

// A start that comes after a server's process exited, before wait reaped
// it, follows the run (F1): the supervisor tells the exited child from a
// running one without reaping it, so wait still gets its exit code.
func TestExitedChildIsToldFromARunningOne(t *testing.T) {
	cmd := exec.Command("sh", "-c", "exit 3")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	h := hostProcess{cmd.Process}
	deadline := time.Now().Add(5 * time.Second)
	for !h.exited() {
		if time.Now().After(deadline) {
			t.Fatal("an exited child that was not reaped does not count as exited")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if h.reaped() {
		t.Fatal("the look reaped the child")
	}
	if code := h.wait(); code != 3 {
		t.Fatalf("wait after the look got code %d, want 3", code)
	}
	if !h.exited() {
		t.Fatal("a reaped child does not count as exited")
	}

	sleeper := exec.Command("sleep", "30")
	if err := sleeper.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sleeper.Process.Kill(); _ = sleeper.Wait() }()
	if (hostProcess{sleeper.Process}).exited() {
		t.Fatal("a running child counts as exited")
	}
}
