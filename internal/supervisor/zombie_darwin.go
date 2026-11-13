package supervisor

import "golang.org/x/sys/unix"

// szomb is a process's p_stat when it has exited and waits to be reaped
// (SZOMB in <sys/proc.h>).
const szomb = 5

// zombie reports whether the child pid has exited and waits to be reaped,
// from the process's state in the kernel's process table, which a look
// leaves as it is. A pid that no longer exists is no zombie.
func zombie(pid int) bool {
	kp, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	return err == nil && kp.Proc.P_pid == int32(pid) && kp.Proc.P_stat == szomb
}
