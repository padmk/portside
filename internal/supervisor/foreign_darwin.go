package supervisor

import (
	"errors"
	"syscall"

	"golang.org/x/sys/unix"
)

// foreignProcess is a process this daemon did not start, identified by
// its start time from the kernel's process table to the microsecond:
// once it is gone its pid may belong to another process, which must not
// receive the signals meant for it.
type foreignProcess struct {
	pid   int
	start unix.Timeval
}

// openForeign identifies the live process pid; false when it is gone.
func openForeign(pid int) (*foreignProcess, bool) {
	start, ok := kernelStart(pid)
	if !ok {
		return nil, false
	}
	return &foreignProcess{pid: pid, start: start}, true
}

// kernelStart returns the start time of the live (not zombie) process pid.
func kernelStart(pid int) (unix.Timeval, bool) {
	kp, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil || kp.Proc.P_pid != int32(pid) || kp.Proc.P_stat == szomb {
		return unix.Timeval{}, false
	}
	return kp.Proc.P_starttime, true
}

// alive reports whether the process still runs.
func (f *foreignProcess) alive() bool {
	start, ok := kernelStart(f.pid)
	return ok && start == f.start
}

// signal sends sig to the process while it is the one identified; the
// check and the kill are microseconds apart.
func (f *foreignProcess) signal(sig syscall.Signal) error {
	if !f.alive() {
		return nil
	}
	if err := syscall.Kill(f.pid, sig); err != nil && !errors.Is(err, syscall.ESRCH) {
		return err
	}
	return nil
}

func (f *foreignProcess) close() {}
