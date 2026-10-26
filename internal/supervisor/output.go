package supervisor

import (
	"errors"
	"os"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

// outputReader copies a server's terminal into its log. Pause stops it
// between two reads, so whatever the process writes afterwards stays in the
// terminal for the daemon that takes the server over in an upgrade; Resume
// lets it continue when the upgrade does not happen.
type outputReader struct {
	f    *os.File
	logs *LogBuffer

	mu      sync.Mutex
	pausing bool
	stopped chan struct{} // closed once the reader has stopped for the pause
	resume  chan struct{} // closed by Resume
}

func newOutputReader(f *os.File, logs *LogBuffer) *outputReader {
	return &outputReader{f: f, logs: logs}
}

// run copies until the terminal ends.
func (r *outputReader) run() {
	buf := make([]byte, 32*1024)
	for {
		k, err := r.f.Read(buf)
		if k > 0 {
			_, _ = r.logs.Write(buf[:k])
		}
		if r.holdIfPaused() {
			continue
		}
		if err != nil && !errors.Is(err, os.ErrDeadlineExceeded) {
			return
		}
	}
}

// holdIfPaused waits for Resume while a pause is requested, reporting
// whether it did.
func (r *outputReader) holdIfPaused() bool {
	r.mu.Lock()
	if !r.pausing {
		r.mu.Unlock()
		return false
	}
	stopped, resume := r.stopped, r.resume
	r.mu.Unlock()
	select {
	case <-stopped:
	default:
		close(stopped)
	}
	<-resume
	return true
}

// Pause stops the reader before its next read and reports whether it
// stopped within timeout; a terminal without read deadlines cannot be
// interrupted while it waits for output.
func (r *outputReader) Pause(timeout time.Duration) bool {
	r.mu.Lock()
	if r.pausing {
		r.mu.Unlock()
		return true
	}
	r.pausing = true
	r.stopped, r.resume = make(chan struct{}), make(chan struct{})
	stopped := r.stopped
	r.mu.Unlock()
	if err := r.f.SetReadDeadline(time.Now()); err != nil {
		return false
	}
	select {
	case <-stopped:
		return true
	case <-time.After(timeout):
		return false
	}
}

// Resume lets a paused reader continue.
func (r *outputReader) Resume() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.pausing {
		return
	}
	r.pausing = false
	_ = r.f.SetReadDeadline(time.Time{})
	close(r.resume)
}

// pollableTerminal returns f's terminal as a non-blocking file that Go's
// poller serves, so read deadlines (and so Pause) work, and closes f. It
// returns f itself when that fails.
func pollableTerminal(f *os.File) *os.File {
	rc, err := f.SyscallConn()
	if err != nil {
		return f
	}
	dup := -1
	_ = rc.Control(func(fd uintptr) { dup, _ = unix.Dup(int(fd)) })
	if dup < 0 {
		return f
	}
	unix.CloseOnExec(dup)
	if err := unix.SetNonblock(dup, true); err != nil {
		_ = unix.Close(dup)
		return f
	}
	g := os.NewFile(uintptr(dup), f.Name())
	_ = f.Close()
	return g
}

// setTerminalSize resizes the terminal without f.Fd(), which would put the
// file back into blocking mode and end its read deadlines.
func setTerminalSize(f *os.File, cols, rows uint16) error {
	rc, err := f.SyscallConn()
	if err != nil {
		return err
	}
	var ioErr error
	if err := rc.Control(func(fd uintptr) {
		ioErr = unix.IoctlSetWinsize(int(fd), unix.TIOCSWINSZ, &unix.Winsize{Col: cols, Row: rows})
	}); err != nil {
		return err
	}
	return ioErr
}
