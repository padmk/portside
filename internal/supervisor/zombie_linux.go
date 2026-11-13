package supervisor

import (
	"errors"
	"time"

	"golang.org/x/sys/unix"
)

// exitWait returns once the child pid has ended, without reaping it:
// waitid with WNOWAIT leaves the zombie for reap. ECHILD, which only a
// reap elsewhere could cause, ends the wait as well.
func exitWait(pid int) {
	for {
		var info unix.Siginfo
		err := unix.Waitid(unix.P_PID, pid, &info, unix.WEXITED|unix.WNOWAIT, nil)
		switch {
		case err == nil, errors.Is(err, unix.ECHILD):
			return
		case errors.Is(err, unix.EINTR):
		default:
			time.Sleep(100 * time.Millisecond) // retried: the child is still ours
		}
	}
}

// zombie reports whether the child pid has ended and waits to be reaped.
// waitid with WNOHANG and WNOWAIT looks without reaping: with no child to
// report, the kernel zeroes the signal number it otherwise sets to
// SIGCHLD. ECHILD means it is not waiting any more, so it has ended.
func zombie(pid int) (bool, error) {
	var info unix.Siginfo
	err := unix.Waitid(unix.P_PID, pid, &info, unix.WEXITED|unix.WNOHANG|unix.WNOWAIT, nil)
	switch {
	case errors.Is(err, unix.ECHILD):
		return true, nil
	case err != nil:
		return false, err
	}
	return info.Signo == int32(unix.SIGCHLD), nil
}
