package supervisor

import (
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/creack/pty"
)

// TestPauseKeepsOutputInTheTerminal checks that a paused reader leaves what
// the process writes in its terminal, for the terminal as started and as a
// daemon adopting it after an upgrade opens it.
func TestPauseKeepsOutputInTheTerminal(t *testing.T) {
	for _, adopted := range []bool{false, true} {
		cmd := exec.Command("sh", "-c", "read x; echo after; sleep 60")
		f, err := pty.Start(cmd)
		if err != nil {
			t.Fatal(err)
		}
		pid := cmd.Process.Pid
		t.Cleanup(func() { _ = syscall.Kill(-pid, syscall.SIGKILL) })
		// As startOne keeps it, or as a daemon adopting it after an upgrade
		// opens the descriptor it inherited.
		term := pollableTerminal(f)
		if adopted {
			fd, err := syscall.Dup(int(term.Fd()))
			if err != nil {
				t.Fatal(err)
			}
			term = inheritedTerminal(t, fd)
		}
		logs := NewMemoryLog()
		r := newOutputReader(term, logs)
		go r.run()
		if !r.Pause(2 * time.Second) {
			t.Fatalf("adopted %v: the reader did not pause", adopted)
		}
		if _, err := term.Write([]byte("go\n")); err != nil {
			t.Fatal(err)
		}
		time.Sleep(500 * time.Millisecond)
		if strings.Contains(string(logs.Raw()), "after") {
			t.Fatalf("adopted %v: a paused reader read on", adopted)
		}
		r.Resume()
		deadline := time.Now().Add(3 * time.Second)
		for !strings.Contains(string(logs.Raw()), "after") {
			if time.Now().After(deadline) {
				t.Fatalf("adopted %v: output after Resume missing: %q", adopted, logs.Raw())
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
}

// inheritedTerminal opens fd the way inheritTerminal does after an exec.
func inheritedTerminal(t *testing.T, fd int) *os.File {
	t.Helper()
	_ = syscall.SetNonblock(fd, true)
	return os.NewFile(uintptr(fd), "pty")
}

func TestPauseReturnsAtOnceForAnEndedReader(t *testing.T) {
	cmd := exec.Command("sh", "-c", "exec 0<&- 1>&- 2>&-; sleep 60")
	f, err := pty.Start(cmd)
	if err != nil {
		t.Fatal(err)
	}
	pid := cmd.Process.Pid
	t.Cleanup(func() { _ = syscall.Kill(-pid, syscall.SIGKILL) })
	r := newOutputReader(pollableTerminal(f), NewMemoryLog())
	go r.run()
	select {
	case <-r.done:
	case <-time.After(3 * time.Second):
		t.Skip("this system keeps reading a terminal its process closed")
	}
	start := time.Now()
	if !r.Pause(2 * time.Second) {
		t.Fatal("Pause reported failure for an ended reader")
	}
	if d := time.Since(start); d > 200*time.Millisecond {
		t.Fatalf("Pause waited %v for an ended reader", d)
	}
}
