package supervisor

import (
	"errors"
	"time"

	"golang.org/x/sys/unix"
)

// szomb is a process's p_stat when it has exited and waits to be reaped
// (SZOMB in <sys/proc.h>).
const szomb = 5

// exitWait returns once the child pid has ended, without reaping it: a
// kqueue's NOTE_EXIT reports the exit and leaves the zombie for reap. A
// child that ended before the watch began is reported at once or not
// found (ESRCH); either ends the wait, which zombie_test.go checks on the
// real system. Other failures fall back to looking at its state every
// 100 ms.
func exitWait(pid int) {
	kq, err := unix.Kqueue()
	if err != nil {
		pollExit(pid)
		return
	}
	defer func() { _ = unix.Close(kq) }() // a descriptor done with
	change := []unix.Kevent_t{{Ident: uint64(pid), Filter: unix.EVFILT_PROC, Flags: unix.EV_ADD | unix.EV_ONESHOT, Fflags: unix.NOTE_EXIT}}
	out := make([]unix.Kevent_t, 1)
	for {
		n, err := unix.Kevent(kq, change, out, nil)
		switch {
		case errors.Is(err, unix.EINTR):
			continue
		case errors.Is(err, unix.ESRCH):
			return
		case err != nil:
			pollExit(pid)
			return
		case n == 0:
			continue
		case out[0].Flags&unix.EV_ERROR != 0:
			if unix.Errno(out[0].Data) == unix.ESRCH {
				return
			}
			pollExit(pid)
			return
		case out[0].Fflags&unix.NOTE_EXIT != 0:
			return
		}
		change = nil // registered: wait for the exit
	}
}

// pollExit returns once zombie says the child pid has ended.
func pollExit(pid int) {
	for {
		if ended, _ := zombie(pid); ended {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// zombie reports whether the child pid has ended and waits to be reaped,
// from the kernel's process table, which a look leaves as it is. A pid
// that is not there any more (an empty answer, EIO from the library) has
// ended too.
func zombie(pid int) (bool, error) {
	kp, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	switch {
	case errors.Is(err, unix.ENOENT), errors.Is(err, unix.ESRCH), errors.Is(err, unix.EIO):
		return true, nil
	case err != nil:
		return false, err
	case kp.Proc.P_pid != int32(pid):
		return true, nil
	}
	return kp.Proc.P_stat == szomb, nil
}
