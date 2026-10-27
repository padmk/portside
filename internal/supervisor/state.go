package supervisor

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/padmk/portside/internal/paths"
)

// persisted survives daemon restarts: which servers the user wants running
// and which process groups the previous daemon left behind.
type persisted struct {
	Running []string             `json:"running"`
	Groups  map[string]pidStamp  `json:"groups,omitempty"`
	Servers map[string]exitState `json:"servers,omitempty"`
	// Temporary lists the temporary servers that run in other checkouts.
	Temporary []temporary `json:"temporary,omitempty"`
	// Saved is when the file was written; every group in Groups had its
	// leader alive then (see orphanedGroup).
	Saved time.Time `json:"saved,omitzero"`
}

type exitState struct {
	Status  Status    `json:"status"`
	Note    string    `json:"note,omitempty"`
	Code    int       `json:"exitCode"`
	HasExit bool      `json:"hasExit"`
	At      time.Time `json:"exitedAt"`
	// What hooks have reported, so a restart or an upgrade neither repeats
	// nor forgets it.
	Trigger       string    `json:"trigger,omitempty"`
	Streak        int       `json:"streak,omitempty"`
	Incident      string    `json:"incident,omitempty"`
	IncidentSince time.Time `json:"incidentSince,omitzero"`
	// RetryAt is when a pending retry after retry_after starts the server.
	RetryAt time.Time `json:"retryAt,omitzero"`
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
	s.savedLocked(writeState(s.state, s.stateLocked()))
}

// savedLocked records whether a state write worked, logging when that
// changes: without the file, a server the user stopped starts again after
// a daemon crash.
func (s *Supervisor) savedLocked(err error) {
	msg := ""
	if err != nil {
		// The temporary file's name changes with every write.
		if pe, ok := errors.AsType[*fs.PathError](err); ok {
			err = pe.Err
		}
		msg = fmt.Sprintf("%s: %v", paths.ShortenHome(filepath.Dir(s.state)), err)
	}
	if msg == s.saveErr {
		return
	}
	switch {
	case msg != "" && s.saveErr == "":
		log.Printf("state: cannot save to %s; servers stopped since may start again after a crash", msg)
	case msg == "":
		log.Print("state: saved again")
	}
	s.saveErr = msg
	s.changedLocked()
}

// SaveError reports why the server state could not be saved, or "" when the
// last write worked.
func (s *Supervisor) SaveError() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saveErr
}

func (s *Supervisor) stateLocked() persisted {
	st := persisted{Groups: map[string]pidStamp{}, Servers: map[string]exitState{}, Saved: time.Now()}
	for _, name := range s.order {
		p := s.procs[name]
		if p == nil {
			continue
		}
		st.Servers[name] = exitState{
			Status: p.status, Note: p.note, Code: p.exitCode, HasExit: p.hasExit, At: p.exitedAt,
			Trigger: p.trigger, Streak: p.streak, Incident: p.incident, IncidentSince: p.incidentSince,
		}
		if p.restartPending && !p.retryAt.IsZero() {
			e := st.Servers[name]
			e.RetryAt = p.retryAt
			st.Servers[name] = e
		}
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
	return st
}

func writeState(path string, st persisted) error {
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), paths.DirMode); err != nil {
		return err
	}
	return paths.WriteFile(path, data)
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
	exited := s.handoffRestarts
	s.handoffRestarts = nil
	gone := s.handoffExited
	s.handoffExited = nil
	// Servers adopted from the daemon this process replaced keep running.
	adopted := map[int]bool{}
	for _, p := range s.procs {
		if p.pid != 0 {
			adopted[p.pid] = true
		}
	}
	// A group's leader was alive when the state was saved and, unless it
	// exited during an upgrade, when the previous daemon last wrote the
	// server's log: a crashed daemon's servers outlive it.
	alive := map[string]time.Time{}
	for name := range st.Groups {
		alive[name] = st.Saved
		if p := s.procs[name]; p != nil && !gone[name] && p.logs.previousWrite.After(st.Saved) {
			alive[name] = p.logs.previousWrite
		}
	}
	for name, last := range st.Servers {
		if p := s.procs[name]; p != nil && p.pid == 0 && !gone[name] {
			p.exitCode, p.hasExit, p.exitedAt = last.Code, last.HasExit, last.At
			if last.Status == Crashed || last.Status == Exited || last.Status == Blocked {
				// A restart it announced is scheduled again below, or not at all.
				p.status = last.Status
				p.note, _, _ = strings.Cut(last.Note, restartingNote)
			}
		}
	}
	s.mu.Unlock()
	for name, g := range st.Groups {
		if g.PID <= 1 || g.Start == "" || adopted[g.PID] {
			continue
		}
		if !orphanedGroup(g, alive[name]) {
			if syscall.Kill(-g.PID, 0) == nil {
				log.Printf("recover: left process group %d (%s) alone: its processes cannot be tied to the server", g.PID, name)
			}
			continue
		}
		_ = syscall.Kill(-g.PID, syscall.SIGTERM)
		for i := 0; i < 30 && syscall.Kill(-g.PID, 0) == nil; i++ {
			time.Sleep(100 * time.Millisecond)
		}
		_ = syscall.Kill(-g.PID, syscall.SIGKILL)
	}
	// Servers that exited during the upgrade restart as after a crash,
	// whatever restore says; a stop requested meanwhile (restore false) wins.
	started := map[string]bool{}
	if restore {
		for _, name := range exited {
			started[name] = true
			_ = s.start(name, StartOptions{}) // the round its restarts belong to goes on
		}
	}
	s.mu.Lock()
	// A pending retry goes on with the time it had left, after an upgrade
	// in place, or after a crash when restore brings servers back.
	if restore && (s.upgraded || s.cfg.Restore) {
		for name, last := range st.Servers {
			p := s.procs[name]
			if last.RetryAt.IsZero() || p == nil || p.pid != 0 || gone[name] || started[name] || !p.cfg.Enabled || p.removed || !p.cfg.RetriesAfterGivingUp() {
				continue
			}
			started[name] = true
			p.want = true
			s.scheduleRetryLocked(p, time.Now(), last.RetryAt)
			s.changedLocked()
		}
	}
	restore = restore && s.cfg.Restore
	s.mu.Unlock()
	if restore {
		for _, name := range st.Running {
			// A server that exited during the upgrade was handled above, by
			// its restart policy.
			if started[name] || gone[name] {
				continue
			}
			s.mu.Lock()
			p, ok := s.procs[name]
			enabled := ok && p.cfg.Enabled && !p.removed
			s.mu.Unlock()
			if enabled {
				_ = s.Start(name, StartOptions{trigger: triggerRestore})
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

// restoreHistoryLocked brings back what the hooks knew about each server
// before this daemon started: the trigger of its round of runs, its crash
// streak and the problem a hook reported, so reports from an upgrade or a
// restart on continue them.
func (s *Supervisor) restoreHistoryLocked(saved map[string]exitState) {
	for name, last := range saved {
		if p := s.procs[name]; p != nil {
			p.trigger, p.streak, p.incident, p.incidentSince = last.Trigger, last.Streak, last.Incident, last.IncidentSince
		}
	}
}

// orphanedGroup reports whether the process group g still holds only
// processes a server started, so ending it cannot hit an unrelated program
// that got the recycled id. A live leader must have the saved start time.
// Without the leader, every member must have started between the leader's
// start and alive, a time the leader was known to live: it held the id
// until then, so a group with that id that an unrelated program creates
// later has only members started after it.
func orphanedGroup(g pidStamp, alive time.Time) bool {
	if processStart(g.PID) != "" {
		return sameStart(g.PID, g.Start)
	}
	from, err := time.ParseInLocation(startLayout, g.Start, time.Local)
	if err != nil || alive.IsZero() {
		return false // saved by a daemon that did not record enough
	}
	// ps shows whole seconds, so a member started in the second of alive
	// cannot be placed on either side of it.
	until := alive.Truncate(time.Second)
	members := groupMembers(g.PID)
	if len(members) == 0 {
		return false
	}
	for _, started := range members {
		if started.Before(from) || !started.Before(until) {
			return false
		}
	}
	return true
}

// groupMembers returns the start times of the processes in group pgid.
// A process whose start time cannot be read gets the zero time, which no
// group verifies with.
func groupMembers(pgid int) []time.Time {
	cmd := exec.Command("ps", "-A", "-o", "pgid=,lstart=")
	cmd.Env = cLocaleEnv()
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	var members []time.Time
	for line := range strings.Lines(string(out)) {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		if id, err := strconv.Atoi(f[0]); err != nil || id != pgid {
			continue
		}
		started, _ := time.ParseInLocation(startLayout, strings.Join(f[1:], " "), time.Local)
		members = append(members, started)
	}
	return members
}
