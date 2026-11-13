package supervisor

import (
	"fmt"
	"os"
	"os/exec"
	"slices"
	"sync"
	"syscall"
	"time"

	"github.com/creack/pty"
)

// fakeWorld is the process table and the clock that the daemons of one
// model run share (see model_test.go). Its processes are leaders of their
// own group with a real terminal, whose process side the world holds open
// while a member of the group lives; they exit only when the test or a
// signal says so, and their pids lie above every system's pid range, so a
// signal that reached the real kernel would find no such group. Its timers
// fire only when the test advances the clock.
type fakeWorld struct {
	mu       sync.Mutex
	now      time.Time
	lastPID  int
	procs    map[int]*fakeProc
	timers   []*fakeTimer
	timerSeq int
	// daemon is the daemon that owns the processes now: an upgrade in
	// place hands them to the next one, and the replaced daemon's waits
	// never return, as it no longer exists.
	daemon int
	// launches counts the processes started per server directory;
	// violations collects what broke an invariant the moment it happened.
	launches   map[string]int
	violations []string
}

type fakeProc struct {
	pid   int
	dir   string // the server's directory, which tells servers apart
	stamp string
	tty   *os.File // the terminal's process side; nil once the group is gone
	// leader is true until the leader exits; orphan while a member that
	// holds the terminal outlives it (it ignores SIGTERM, not SIGINT).
	leader, orphan bool
	// slow leaders take their time after SIGINT: they exit once released.
	slow, interrupted bool
	code              int
	// exited is closed when the leader exits; reapable once the kernel
	// lets wait reap it (a zombie until then), reaped once it has.
	exited          chan struct{}
	reapable        chan struct{}
	reaped          bool
	owner           int
	exitedAt, start time.Time
}

type fakeTimer struct {
	at     time.Time
	seq    int
	daemon int
	f      func()
}

func newFakeWorld() *fakeWorld {
	return &fakeWorld{
		now:      time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC),
		lastPID:  1 << 24, // above Linux's pid_max (1<<22) and macOS's 99999
		procs:    map[int]*fakeProc{},
		launches: map[string]int{},
	}
}

func (w *fakeWorld) clock() time.Time {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.now
}

func (w *fakeWorld) violate(format string, args ...any) {
	w.violations = append(w.violations, fmt.Sprintf(format, args...))
}

// liveLocked returns the process of the server in dir whose leader still
// runs, or nil.
func (w *fakeWorld) liveLocked(dir string) *fakeProc {
	for _, p := range w.procs {
		if p.leader && p.dir == dir {
			return p
		}
	}
	return nil
}

// exitLocked ends p's leader with code. With release, the zombie is
// reapable at once; otherwise the test lets wait reap it later.
func (w *fakeWorld) exitLocked(p *fakeProc, code int, release bool) {
	if !p.leader {
		return
	}
	p.leader, p.code, p.exitedAt = false, code, w.now
	close(p.exited)
	if release {
		close(p.reapable)
	}
	w.closeIfGoneLocked(p)
}

// reapableLocked lets wait reap p's zombie.
func (w *fakeWorld) reapableLocked(p *fakeProc) {
	select {
	case <-p.reapable:
	default:
		close(p.reapable)
	}
}

func (w *fakeWorld) closeIfGoneLocked(p *fakeProc) {
	if !p.leader && !p.orphan && p.tty != nil {
		_ = p.tty.Close()
		p.tty = nil
	}
}

// handoff gives the processes to the next daemon and forgets the timers of
// the one it replaces, as exec would.
func (w *fakeWorld) handoff() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.daemon++
	for _, p := range w.procs {
		p.owner = w.daemon
	}
	w.timers = slices.DeleteFunc(w.timers, func(t *fakeTimer) bool { return t.daemon != w.daemon })
	return w.daemon
}

// nextTimer removes and returns the earliest timer of the current daemon
// due by until, advancing the clock to it, or nil.
func (w *fakeWorld) nextTimer(until time.Time) *fakeTimer {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.timers = slices.DeleteFunc(w.timers, func(t *fakeTimer) bool { return t.daemon != w.daemon })
	if len(w.timers) == 0 {
		return nil
	}
	first := slices.MinFunc(w.timers, func(a, b *fakeTimer) int {
		if c := a.at.Compare(b.at); c != 0 {
			return c
		}
		return a.seq - b.seq
	})
	if first.at.After(until) {
		return nil
	}
	w.timers = slices.DeleteFunc(w.timers, func(t *fakeTimer) bool { return t == first })
	if first.at.After(w.now) {
		w.now = first.at
	}
	return first
}

// fakeSystem is the system one daemon of a fakeWorld sees.
type fakeSystem struct {
	w      *fakeWorld
	daemon int
}

func (f *fakeSystem) startInTerminal(cmd *exec.Cmd, size *pty.Winsize) (*os.File, process, error) {
	master, tty, err := pty.Open()
	if err != nil {
		return nil, nil, err
	}
	_ = pty.Setsize(master, size)
	w := f.w
	w.mu.Lock()
	defer w.mu.Unlock()
	if p := w.liveLocked(cmd.Dir); p != nil {
		w.violate("a second process started in %s while pid %d still runs", cmd.Dir, p.pid)
	}
	w.lastPID++
	p := &fakeProc{
		pid: w.lastPID, dir: cmd.Dir, stamp: fmt.Sprintf("fake start %d", w.lastPID), tty: tty, leader: true,
		exited: make(chan struct{}), reapable: make(chan struct{}), owner: f.daemon, start: w.now,
	}
	w.procs[p.pid] = p
	w.launches[cmd.Dir]++
	return master, &fakeHandle{w: w, p: p, daemon: f.daemon}, nil
}

func (f *fakeSystem) findProcess(pid int) (process, error) {
	f.w.mu.Lock()
	defer f.w.mu.Unlock()
	p := f.w.procs[pid]
	if p == nil {
		return nil, fmt.Errorf("no process %d", pid)
	}
	return &fakeHandle{w: f.w, p: p, daemon: f.daemon}, nil
}

// processStart: a zombie still has its entry, as in ps.
func (f *fakeSystem) processStart(pid int) string {
	f.w.mu.Lock()
	defer f.w.mu.Unlock()
	if p := f.w.procs[pid]; p != nil && !p.reaped {
		return p.stamp
	}
	return ""
}

func (f *fakeSystem) sameStart(pid int, saved string) bool {
	return saved != "" && f.processStart(pid) == saved
}

func (f *fakeSystem) groupMembers(int) []time.Time { return nil }

func (f *fakeSystem) signalGroup(pgid int, sig syscall.Signal) error {
	w := f.w
	w.mu.Lock()
	defer w.mu.Unlock()
	p := w.procs[pgid]
	if p == nil || (!p.leader && !p.orphan && p.reaped) {
		return syscall.ESRCH
	}
	switch sig {
	case syscall.SIGINT:
		p.orphan = false
		if p.slow {
			p.interrupted = true
		} else {
			w.exitLocked(p, 128+int(sig), true)
		}
	case syscall.SIGTERM:
		if !p.slow {
			w.exitLocked(p, 128+int(sig), true)
		}
	case syscall.SIGKILL:
		p.orphan = false
		w.exitLocked(p, 128+int(sig), true)
	default:
		// Signal 0 only checks that the group lives; the supervisor sends
		// no other signal to a server's group.
	}
	w.closeIfGoneLocked(p)
	return nil
}

func (f *fakeSystem) afterFunc(d time.Duration, fn func()) {
	w := f.w
	w.mu.Lock()
	defer w.mu.Unlock()
	w.timerSeq++
	w.timers = append(w.timers, &fakeTimer{at: w.now.Add(d), seq: w.timerSeq, daemon: f.daemon, f: fn})
}

func (f *fakeSystem) after(d time.Duration) <-chan time.Time {
	ch := make(chan time.Time, 1)
	f.afterFunc(d, func() { ch <- f.w.clock() })
	return ch
}

// fakeHandle is one daemon's handle on a fake process.
type fakeHandle struct {
	w      *fakeWorld
	p      *fakeProc
	daemon int
}

func (h *fakeHandle) pid() int { return h.p.pid }

func (h *fakeHandle) wait() int {
	<-h.p.exited
	<-h.p.reapable
	h.w.mu.Lock()
	mine := h.p.owner == h.daemon
	if mine {
		h.p.reaped = true
	}
	code := h.p.code
	h.w.mu.Unlock()
	if !mine {
		select {} // this daemon was replaced in place: it no longer runs
	}
	return code
}

func (h *fakeHandle) reaped() bool {
	h.w.mu.Lock()
	defer h.w.mu.Unlock()
	return h.p.reaped
}

func syscallDup(fd int) (int, error) { return syscall.Dup(fd) }
