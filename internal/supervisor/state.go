package supervisor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// persisted survives daemon restarts: which servers the user wants running
// and which process groups the previous daemon left behind.
type persisted struct {
	Running []string             `json:"running"`
	Groups  map[string]pidStamp  `json:"groups,omitempty"`
	Servers map[string]exitState `json:"servers,omitempty"`
	// Temporary lists the temporary servers that run in other checkouts.
	Temporary []temporary `json:"temporary,omitempty"`
}

type exitState struct {
	Status  Status    `json:"status"`
	Note    string    `json:"note,omitempty"`
	Code    int       `json:"exitCode"`
	HasExit bool      `json:"hasExit"`
	At      time.Time `json:"exitedAt"`
}

type pidStamp struct {
	PID   int    `json:"pid"`
	Start string `json:"start"`
}

func statePath(stateDir string) string { return filepath.Join(stateDir, "state.json") }

func (s *Supervisor) saveLocked() {
	if s.closing {
		return // the wanted set was saved before shutdown began
	}
	st := persisted{Groups: map[string]pidStamp{}, Servers: map[string]exitState{}}
	for _, name := range s.order {
		p := s.procs[name]
		if p == nil {
			continue
		}
		st.Servers[name] = exitState{Status: p.status, Note: p.note, Code: p.exitCode, HasExit: p.hasExit, At: p.exitedAt}
		if p.want {
			st.Running = append(st.Running, name)
		}
		if p.pid != 0 {
			st.Groups[name] = pidStamp{PID: p.pid, Start: p.pidStart}
		}
		if p.temp != nil && !p.removed {
			st.Temporary = append(st.Temporary, *p.temp)
		}
	}
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return
	}
	_ = os.MkdirAll(filepath.Dir(s.state), 0o755)
	tmp := s.state + ".tmp"
	if os.WriteFile(tmp, data, 0o644) == nil {
		_ = os.Rename(tmp, s.state)
	}
}

func (s *Supervisor) load() persisted {
	var st persisted
	data, err := os.ReadFile(s.state)
	if err == nil {
		_ = json.Unmarshal(data, &st)
	}
	return st
}

// Recover ends process groups orphaned by a daemon that died without
// cleaning up, then starts the servers that were running before (if the
// config asks for it and restore is set). Servers taken over by Adopt are
// left running. Call once, before serving requests.
func (s *Supervisor) Recover(restore bool) {
	s.mu.Lock()
	st := s.previous
	s.previous = persisted{}
	removed := s.adoptedRemoved
	s.adoptedRemoved = nil
	// Servers adopted from the daemon this process replaced keep running.
	adopted := map[int]bool{}
	for _, p := range s.procs {
		if p.pid != 0 {
			adopted[p.pid] = true
		}
	}
	for name, last := range st.Servers {
		if p := s.procs[name]; p != nil && p.pid == 0 {
			p.exitCode, p.hasExit, p.exitedAt = last.Code, last.HasExit, last.At
			if last.Status == Crashed || last.Status == Exited || last.Status == Blocked {
				p.status, p.note = last.Status, last.Note
			}
		}
	}
	s.mu.Unlock()
	for _, g := range st.Groups {
		if g.PID <= 1 || g.Start == "" || adopted[g.PID] {
			continue
		}
		// A live leader must be the same process (same start time), not an
		// unrelated program that got the recycled pid. If the leader is gone
		// but its group still has members, they are ours: a pid cannot be
		// reused while a process group with that id exists.
		if start := processStart(g.PID); start != "" && start != g.Start {
			continue
		}
		if syscall.Kill(-g.PID, 0) != nil {
			continue
		}
		_ = syscall.Kill(-g.PID, syscall.SIGTERM)
		for i := 0; i < 30 && syscall.Kill(-g.PID, 0) == nil; i++ {
			time.Sleep(100 * time.Millisecond)
		}
		_ = syscall.Kill(-g.PID, syscall.SIGKILL)
	}
	s.mu.Lock()
	restore = restore && s.cfg.Restore
	s.mu.Unlock()
	if restore {
		for _, name := range st.Running {
			s.mu.Lock()
			p, ok := s.procs[name]
			enabled := ok && p.cfg.Enabled && !p.removed
			s.mu.Unlock()
			if enabled {
				_ = s.Start(name, StartOptions{})
			}
		}
	}
	s.mu.Lock()
	s.saveLocked()
	s.mu.Unlock()
	for _, name := range removed {
		go func() { _ = s.Stop(name) }()
	}
}
