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
	Running []string            `json:"running"`
	Groups  map[string]pidStamp `json:"groups,omitempty"`
}

type pidStamp struct {
	PID   int    `json:"pid"`
	Start string `json:"start"`
}

func (s *Supervisor) saveLocked() {
	if s.closing {
		return // the wanted set was saved before shutdown began
	}
	st := persisted{Groups: map[string]pidStamp{}}
	for _, name := range s.order {
		p := s.procs[name]
		if p == nil {
			continue
		}
		if p.want {
			st.Running = append(st.Running, name)
		}
		if p.pid != 0 {
			st.Groups[name] = pidStamp{PID: p.pid, Start: p.pidStart}
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
// config asks for it). Call once, before serving requests.
func (s *Supervisor) Recover() {
	st := s.load()
	for _, g := range st.Groups {
		if g.PID <= 1 || g.Start == "" {
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
	restore := s.cfg.Restore
	s.mu.Unlock()
	if !restore {
		s.mu.Lock()
		s.saveLocked()
		s.mu.Unlock()
		return
	}
	for _, name := range st.Running {
		s.mu.Lock()
		p, ok := s.procs[name]
		enabled := ok && p.cfg.Enabled
		s.mu.Unlock()
		if enabled {
			_ = s.Start(name, StartOptions{})
		}
	}
	s.mu.Lock()
	s.saveLocked()
	s.mu.Unlock()
}
