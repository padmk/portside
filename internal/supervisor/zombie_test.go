package supervisor

import (
	"os/exec"
	"testing"
	"time"
)

// The supervisor sees a child's end before it reaps it: exitWait returns
// once the child has exited, also when it exited before the wait began,
// and leaves a zombie, which zombie reports and reap then collects with
// its exit code; a running child is no zombie and exitWait waits for it.
func TestExitWaitLeavesAZombie(t *testing.T) {
	for _, early := range []bool{false, true} {
		cmd := exec.Command("sh", "-c", "sleep 0.2; exit 3")
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		h := hostProcess{cmd.Process}
		if early {
			time.Sleep(time.Second) // ended before the wait begins
		}
		ended := make(chan struct{})
		go func() { h.exitWait(); close(ended) }()
		select {
		case <-ended:
		case <-time.After(5 * time.Second):
			t.Fatalf("early %v: exitWait did not return after the child exited", early)
		}
		if z, err := h.zombie(); !z || err != nil {
			t.Fatalf("early %v: an exited child that was not reaped: zombie %v, %v", early, z, err)
		}
		if code := h.reap(); code != 3 {
			t.Fatalf("early %v: reap got code %d, want 3", early, code)
		}
	}

	sleeper := exec.Command("sleep", "30")
	if err := sleeper.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sleeper.Process.Kill(); _ = sleeper.Wait() }()
	h := hostProcess{sleeper.Process}
	if z, err := h.zombie(); z || err != nil {
		t.Fatalf("a running child: zombie %v, %v", z, err)
	}
	ended := make(chan struct{})
	go func() { h.exitWait(); close(ended) }()
	select {
	case <-ended:
		t.Fatal("exitWait returned for a running child")
	case <-time.After(300 * time.Millisecond):
	}
	_ = sleeper.Process.Kill()
	select {
	case <-ended:
	case <-time.After(5 * time.Second):
		t.Fatal("exitWait did not return after the child was killed")
	}
}
