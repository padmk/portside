package supervisor

import (
	"log"
	"time"
)

// memoryStop is a server's process group to end because it used more
// memory than its memory_limit.
type memoryStop struct {
	pid    int
	done   chan struct{}
	grace  time.Duration
	ending chan struct{}
}

// overMemoryLocked checks a usage sample of p's process tree, mem bytes,
// against its memory_limit. Over it, p is marked stopping with the note
// the crash will carry, and the returned group must be ended the way Stop
// ends one. The latest configuration's limit applies, so a changed limit
// takes effect at the next sample without a restart. Nothing is stopped
// while the server is already stopping, nor while the daemon shuts down
// or hands its servers to a new binary.
func (s *Supervisor) overMemoryLocked(p *proc, mem int64) (memoryStop, bool) {
	limit := p.cfg.MemoryLimit
	if limit.Bytes <= 0 || mem <= limit.Bytes || p.pid == 0 || s.closing || p.stopping || p.overMemory != "" {
		return memoryStop{}, false
	}
	if p.status != Starting && p.status != Running {
		return memoryStop{}, false
	}
	if p.ending != nil && !isClosed(p.ending) {
		return memoryStop{}, false
	}
	note := "used " + limit.Over(mem) + ", over memory_limit " + limit.String()
	p.overMemory, p.overMemorySeq = note, p.startSeq
	p.status = Stopping
	p.note = note
	stamp := time.Now().Format("15:04:05")
	_, _ = p.logs.Write([]byte("\r\n\x1b[31m── " + stamp + "  " + note + ", stopping ──\x1b[0m\r\n"))
	log.Printf("%s: %s; stopping it", p.cfg.Name, note)
	stop := memoryStop{pid: p.pid, done: p.done, grace: p.running.Stop, ending: s.beginEndingLocked(p)}
	s.saveLocked()
	return stop, true
}
