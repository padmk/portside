package supervisor

import (
	"errors"
	"fmt"
	"log"
	"os"
	"slices"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/unix"

	"github.com/padmk/portside/internal/config"
)

// Handoff describes a live server that a daemon passes to the binary it
// re-executes in its own process. The server keeps running: the process
// stays this daemon's child and its terminal's descriptor stays open across
// the exec, so the new daemon only has to take both over.
type Handoff struct {
	Name     string `json:"name"`
	PID      int    `json:"pid"`
	PIDStart string `json:"pidStart"`
	// PTY is the descriptor number of the terminal's controlling side.
	PTY         int `json:"pty"`
	pty         *os.File
	Config      config.Server `json:"config"` // what the process was started with
	Env         []string      `json:"env"`
	Shell       string        `json:"shell"`
	Tool        string        `json:"tool,omitempty"`   // version manager the process runs under
	Launch      *launchInputs `json:"launch,omitempty"` // what Env was built from
	Status      Status        `json:"status"`
	Note        string        `json:"note,omitempty"`
	Want        bool          `json:"want"`
	Retries     int           `json:"retries"`
	StartedAt   time.Time     `json:"startedAt"`
	ReadyAt     time.Time     `json:"readyAt"`
	ListenHost  string        `json:"listenHost,omitempty"`
	ListenPorts []int         `json:"listenPorts,omitempty"`
	// LogReady says whether a line already matched the server's ready_log
	// (nil from a daemon before it), and LogLine is the unfinished line the
	// match continues with.
	LogReady *bool  `json:"logReady,omitempty"`
	LogLine  []byte `json:"logLine,omitempty"`
	// MissingSince is when a running server stopped answering, so its lost
	// readiness stays reported.
	MissingSince time.Time `json:"missingSince,omitzero"`
	Cols         uint16    `json:"cols"`
	Rows         uint16    `json:"rows"`
}

// PrepareHandoff refuses new launches and returns every live server. A
// server that is being stopped blocks the handoff, since its stop must
// finish in one place. KeepTerminalsOnExec then keeps the terminals open
// across exec; CancelHandoff resumes launches when the exec fails.
func (s *Supervisor) PrepareHandoff() ([]Handoff, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closing {
		return nil, errors.New("the daemon is shutting down")
	}
	for _, name := range s.order {
		if p := s.procs[name]; p != nil && p.pid != 0 && p.stopping {
			return nil, fmt.Errorf("%s is stopping; try again once it has stopped", name)
		}
	}
	// Readers stop between reads, so what the processes write from now on
	// stays in their terminals for the new daemon, and the state below
	// (ready_log matches) covers everything read so far. A reader that does
	// not stop leaves a window in which a ready_log line can be missed (its
	// output still reaches the log file), which is logged rather than worth
	// refusing the upgrade for.
	var paused sync.WaitGroup
	for _, name := range s.order {
		if p := s.procs[name]; p != nil && p.pid != 0 && p.reader != nil {
			paused.Go(func() {
				if !p.reader.Pause(2 * time.Second) {
					log.Printf("handoff: %s: its terminal could not be paused; a ready_log line it prints during the upgrade may be missed", name)
				}
			})
		}
	}
	paused.Wait()
	// The state file keeps the process groups for orphan recovery, in case
	// the new binary does not take them over.
	s.saveLocked()
	s.closing = true
	var out []Handoff
	for _, name := range s.order {
		p := s.procs[name]
		if p == nil || p.pid == 0 || p.pty == nil {
			continue
		}
		// Read together, under the lock the matcher runs under.
		var logReady bool
		var logLine []byte
		p.logs.Locked(func() {
			logReady = p.logReady.Load()
			if m := p.readyMatch; m != nil && !logReady {
				logLine = slices.Clone(m.line)
			}
		})
		out = append(out, Handoff{
			Name: name, PID: p.pid, PIDStart: p.pidStart, pty: p.pty,
			Config: p.running, Env: slices.Clone(p.runningEnv), Shell: p.runningShell, Tool: p.runningTool, Launch: p.launch,
			Status: p.status, Note: p.note, Want: p.want, Retries: p.retries,
			StartedAt: p.startedAt, ReadyAt: p.readyAt,
			ListenHost: p.listenHost, ListenPorts: slices.Clone(p.listenPorts),
			Cols: p.cols, Rows: p.rows, LogReady: &logReady, LogLine: logLine, MissingSince: p.missingSince,
		})
	}
	return out, nil
}

// CancelHandoff resumes normal operation after a failed exec.
func (s *Supervisor) CancelHandoff() {
	s.mu.Lock()
	for _, p := range s.procs {
		if p.reader != nil {
			p.reader.Resume()
		}
	}
	s.closing = false
	s.changedLocked()
	s.mu.Unlock()
}

// KeepTerminalsOnExec keeps the terminals of hs open across exec and records
// their descriptor numbers, or (keep false) lets exec close them again. It
// takes no supervisor lock, so a caller holding syscall.ForkLock can use it.
func KeepTerminalsOnExec(hs []Handoff, keep bool) error {
	for i := range hs {
		fd, err := KeepOnExec(hs[i].pty, keep)
		if err != nil && keep {
			return fmt.Errorf("%s: terminal: %w", hs[i].Name, err)
		}
		hs[i].PTY = fd
	}
	return nil
}

// KeepOnExec keeps f open across exec, or lets exec close it again, and
// returns its descriptor number. Unlike f.Fd() it leaves the blocking mode
// alone: that mode belongs to the open file, which a listener or terminal
// still in use shares. Any process started while f is kept open inherits
// it, so callers hold syscall.ForkLock until the exec.
func KeepOnExec(f *os.File, keep bool) (int, error) {
	rc, err := f.SyscallConn()
	if err != nil {
		return 0, err
	}
	var num int
	var opErr error
	err = rc.Control(func(fd uintptr) {
		num = int(fd)
		flag := unix.FD_CLOEXEC
		if keep {
			flag = 0
		}
		_, opErr = unix.FcntlInt(fd, unix.F_SETFD, flag)
	})
	if err != nil {
		return 0, err
	}
	return num, opErr
}

// Adopt takes over servers handed off by the daemon this process replaced.
// Call it before Recover, which saves the state and stops adopted servers
// that are no longer configured. A server whose process is gone or whose
// terminal descriptor is not usable is skipped; Recover then ends its
// process group and restores it as after a crash. It returns the names it
// adopted.
func (s *Supervisor) Adopt(hs []Handoff) []string {
	var adopted []string
	for _, h := range hs {
		f, err := inheritTerminal(h)
		if err != nil {
			log.Printf("handoff: %s: %v; recovering it as after a crash", h.Name, err)
			continue
		}
		process, err := os.FindProcess(h.PID)
		if err != nil {
			f.Close()
			continue
		}
		s.mu.Lock()
		p := s.procs[h.Name]
		if p == nil {
			// Removed from the config meanwhile: take it over to stop it.
			p = &proc{cfg: h.Config, logs: newLogBuffer(s.logDir, h.Name), removed: true}
			s.procs[h.Name] = p
			s.order = append(s.order, h.Name)
			s.adoptedRemoved = append(s.adoptedRemoved, h.Name)
		}
		if p.pid != 0 {
			s.mu.Unlock()
			f.Close()
			continue
		}
		status := h.Status
		if status != Running {
			status = Starting
		}
		p.gen++
		gen := p.gen
		// A server still waiting for its ready_log line keeps waiting; the
		// output the process writes meanwhile waits in its terminal. A daemon
		// that did not say had already let it count as ready.
		logReady := h.LogReady == nil || *h.LogReady
		p.logReady.Store(logReady)
		if logReady {
			installReadyLog(p, "", nil)
		} else {
			installReadyLog(p, h.Config.ReadyLog, h.LogLine)
		}
		p.process = process
		p.pty = f
		p.pid = h.PID
		// Saved by an older daemon perhaps in its own locale; keep the C
		// locale text that this one compares.
		p.pidStart = h.PIDStart
		if now := processStart(h.PID); now != "" {
			p.pidStart = now
		}
		p.running = h.Config
		p.runningEnv = h.Env
		p.runningShell = h.Shell
		p.runningTool = h.Tool
		p.launch = h.Launch
		p.detected = nil
		p.status = status
		p.note = h.Note
		p.want = h.Want
		p.retries = h.Retries
		p.startedAt = h.StartedAt
		p.readyAt = h.ReadyAt
		p.listenHost = h.ListenHost
		p.listenPorts = h.ListenPorts
		if h.Cols > 0 && h.Rows > 0 {
			p.cols, p.rows = h.Cols, h.Rows
		}
		p.restartPending = false
		p.missingSince = h.MissingSince
		p.hasExit = false
		p.stopping = false
		p.external = nil
		p.listening = false
		p.hist.reset()
		p.done = make(chan struct{})
		p.copied = make(chan struct{})
		copied := p.copied
		p.reader = newOutputReader(f, p.logs)
		reader := p.reader
		s.changedLocked()
		s.mu.Unlock()

		go func() {
			defer close(copied)
			reader.run()
		}()
		go s.wait(h.Name, p, gen)
		go s.watchReady(h.Name, p, gen)
		adopted = append(adopted, h.Name)
	}
	return adopted
}

// inheritTerminal checks that the handed-off descriptor is a terminal and
// the process is the one that was started, and keeps the descriptor away
// from processes this daemon starts later.
func inheritTerminal(h Handoff) (*os.File, error) {
	if h.PTY <= 2 || h.PID <= 1 || h.PIDStart == "" {
		return nil, errors.New("incomplete handoff")
	}
	var st syscall.Stat_t
	if err := syscall.Fstat(h.PTY, &st); err != nil {
		return nil, fmt.Errorf("terminal descriptor %d: %w", h.PTY, err)
	}
	if st.Mode&syscall.S_IFMT != syscall.S_IFCHR {
		return nil, fmt.Errorf("descriptor %d is not a terminal", h.PTY)
	}
	syscall.CloseOnExec(h.PTY)
	// Non-blocking, so Go's poller serves it and a later upgrade can pause
	// its reader (a daemon before that left it blocking).
	_ = syscall.SetNonblock(h.PTY, true)
	f := os.NewFile(uintptr(h.PTY), "pty-"+h.Name)
	if !sameStart(h.PID, h.PIDStart) {
		f.Close()
		return nil, fmt.Errorf("process %d is gone", h.PID)
	}
	return f, nil
}
