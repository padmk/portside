package supervisor

import "golang.org/x/sys/unix"

// zombie reports whether the child pid has exited and waits to be reaped.
// waitid with WNOWAIT looks without reaping it, so wait still gets the
// exit status. With WNOHANG and no child to report, the kernel zeroes the
// signal number it would otherwise set to SIGCHLD; once the child is
// reaped there is no such child.
func zombie(pid int) bool {
	var info unix.Siginfo
	err := unix.Waitid(unix.P_PID, pid, &info, unix.WEXITED|unix.WNOHANG|unix.WNOWAIT, nil)
	return err == nil && info.Signo == int32(unix.SIGCHLD)
}
