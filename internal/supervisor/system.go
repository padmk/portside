package supervisor

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
	"time"

	"github.com/creack/pty"
)

// system is what the lifecycle of a server's process asks of the operating
// system: starting a command in a terminal, finding, identifying and
// signalling processes and their groups, and the timers of automatic
// restarts and of the waits around an exit. hostSystem is the real one; the
// model tests replace it with a fake process table and clock
// (model_test.go).
type system interface {
	// startInTerminal starts cmd as the leader of a new process group with
	// a new terminal of size and returns the terminal's controlling side.
	startInTerminal(cmd *exec.Cmd, size *pty.Winsize) (*os.File, process, error)
	// findProcess returns the child pid, such as one handed over by the
	// daemon this one replaced.
	findProcess(pid int) (process, error)
	// processStart identifies a live process by its start time; "" when
	// it does not exist.
	processStart(pid int) string
	// sameStart reports whether the live process pid has the start time
	// saved for it earlier (see procstart.go).
	sameStart(pid int, saved string) bool
	// signalGroup sends sig to process group pgid; signal 0 checks that
	// the group still has a member.
	signalGroup(pgid int, sig syscall.Signal) error
	// groupMembers returns the start times of the members of group pgid.
	groupMembers(pgid int) []time.Time
	// afterFunc calls f in its own goroutine after d.
	afterFunc(d time.Duration, f func())
	// after delivers the time on the returned channel after d.
	after(d time.Duration) <-chan time.Time
}

// process is a server's leader as the supervisor waits for it.
type process interface {
	pid() int
	// wait reaps the process and returns its exit code (see exitCode).
	wait() int
	// reaped reports whether the process is known to have ended and been
	// reaped: its state says so, not its pid, which another process may
	// have by now.
	reaped() bool
}

type hostSystem struct{}

func (hostSystem) startInTerminal(cmd *exec.Cmd, size *pty.Winsize) (*os.File, process, error) {
	f, err := pty.StartWithSize(cmd, size)
	if err != nil {
		return nil, nil, err
	}
	return f, hostProcess{cmd.Process}, nil
}

func (hostSystem) findProcess(pid int) (process, error) {
	p, err := os.FindProcess(pid)
	if err != nil {
		return nil, err
	}
	return hostProcess{p}, nil
}

func (hostSystem) processStart(pid int) string            { return processStart(pid) }
func (hostSystem) sameStart(pid int, saved string) bool   { return sameStart(pid, saved) }
func (hostSystem) groupMembers(pgid int) []time.Time      { return groupMembers(pgid) }
func (hostSystem) afterFunc(d time.Duration, f func())    { time.AfterFunc(d, f) }
func (hostSystem) after(d time.Duration) <-chan time.Time { return time.After(d) }

func (hostSystem) signalGroup(pgid int, sig syscall.Signal) error {
	return syscall.Kill(-pgid, sig)
}

type hostProcess struct{ p *os.Process }

func (h hostProcess) pid() int  { return h.p.Pid }
func (h hostProcess) wait() int { return exitCode(h.p.Wait()) }

func (h hostProcess) reaped() bool {
	return errors.Is(h.p.Signal(syscall.Signal(0)), os.ErrProcessDone)
}
