package supervisor

import (
	"errors"
	"os"
	"strconv"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

// foreignProcess is a process this daemon did not start, held by a pidfd
// where the kernel has them (Linux 5.3 and later): signals through it
// reach that process only, also once its pid belongs to another one.
// Without one, it is identified by its start time in clock ticks since
// boot, checked right before each signal.
type foreignProcess struct {
	pid   int
	fd    int // the pidfd, -1 for none
	start uint64
}

// openForeign identifies the live process pid; false when it is gone. The
// pidfd is opened before the start time is read, so a pidfd of another
// process that got the pid in between does not match it.
func openForeign(pid int) (*foreignProcess, bool) {
	fd, err := unix.PidfdOpen(pid, 0)
	if err != nil {
		fd = -1
	}
	start, ok := kernelStart(pid)
	if !ok {
		if fd >= 0 {
			_ = unix.Close(fd) // a descriptor done with
		}
		return nil, false
	}
	return &foreignProcess{pid: pid, fd: fd, start: start}, true
}

// kernelStart returns the start time of the live (not zombie) process pid
// from /proc/<pid>/stat: the state is the first field after the command
// name in parentheses, the start time the twentieth.
func kernelStart(pid int) (uint64, bool) {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return 0, false
	}
	s := string(b)
	i := strings.LastIndexByte(s, ')')
	if i < 0 {
		return 0, false
	}
	f := strings.Fields(s[i+1:])
	if len(f) < 20 || f[0] == "Z" || f[0] == "X" {
		return 0, false
	}
	start, err := strconv.ParseUint(f[19], 10, 64)
	return start, err == nil
}

// alive reports whether the process still runs.
func (f *foreignProcess) alive() bool {
	start, ok := kernelStart(f.pid)
	return ok && start == f.start
}

// signal sends sig to the process while it runs.
func (f *foreignProcess) signal(sig syscall.Signal) error {
	if f.fd >= 0 {
		err := unix.PidfdSendSignal(f.fd, sig, nil, 0)
		if errors.Is(err, syscall.ESRCH) {
			return nil
		}
		return err
	}
	if !f.alive() {
		return nil
	}
	if err := syscall.Kill(f.pid, sig); err != nil && !errors.Is(err, syscall.ESRCH) {
		return err
	}
	return nil
}

func (f *foreignProcess) close() {
	if f.fd >= 0 {
		_ = unix.Close(f.fd) // a descriptor done with
		f.fd = -1
	}
}
