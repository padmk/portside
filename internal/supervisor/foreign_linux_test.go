package supervisor

import "golang.org/x/sys/unix"

// recycled makes fp stand for a process that held its pid before: one
// that started a clock tick earlier, held without a pidfd (a pidfd keeps
// pointing at the process it was opened for, which is the guarantee).
func recycled(fp *foreignProcess) {
	if fp.fd >= 0 {
		_ = unix.Close(fp.fd) // a descriptor done with
		fp.fd = -1
	}
	fp.start--
}
