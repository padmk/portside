package docker

import (
	"encoding/json"
	"errors"
	"log"
	"os"
	"path/filepath"
	"time"
)

// UseStateDir restores deliberate stops before the monitor starts following events.
// A file that cannot be read is logged and replaced by the next stop; the
// monitor then starts without remembered stops.
func (m *Monitor) UseStateDir(dir string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.statePath = filepath.Join(dir, "container-stops.json")
	data, err := os.ReadFile(m.statePath)
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	var saved map[string]time.Time
	if err == nil {
		err = json.Unmarshal(data, &saved)
	}
	if err != nil {
		log.Printf("container stop state: %v; starting without remembered stops", err)
		return
	}
	for id, at := range saved {
		m.killed[id], m.killedAt[id] = true, at
	}
}

// pruneStopsLocked forgets stops of containers the engine no longer lists.
func (m *Monitor) pruneStopsLocked(listed map[string]bool) {
	pruned := false
	for id := range m.killedAt {
		if !listed[id] {
			delete(m.killed, id)
			delete(m.killedAt, id)
			pruned = true
		}
	}
	if pruned {
		m.saveStopsLocked()
	}
}

// RecordStop remembers an explicit request even if the events stream misses it.
func (m *Monitor) RecordStop(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.markStoppedLocked(id, time.Now())
}

func (m *Monitor) markStoppedLocked(id string, at time.Time) {
	m.killed[id], m.killedAt[id] = true, at
	m.saveStopsLocked()
}

func (m *Monitor) clearStoppedLocked(id string) {
	if !m.killed[id] {
		return
	}
	delete(m.killed, id)
	delete(m.killedAt, id)
	m.saveStopsLocked()
}

func (m *Monitor) saveStopsLocked() {
	if m.statePath == "" {
		return
	}
	data, err := json.Marshal(m.killedAt)
	if err == nil {
		err = os.MkdirAll(filepath.Dir(m.statePath), 0o700)
	}
	if err == nil {
		err = os.WriteFile(m.statePath+".tmp", data, 0o600)
	}
	if err == nil {
		err = os.Rename(m.statePath+".tmp", m.statePath)
	}
	if err != nil {
		log.Printf("container stop state: %v", err)
	}
}
