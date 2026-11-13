package supervisor

import (
	"log"
	"time"
)

// overMemoryLocked checks a usage sample of p's process tree, mem bytes,
// against its memory_limit. Over it, p is marked stopping with the note
// the crash will carry, and the returned effects end its group the way
// Stop does. The latest configuration's limit applies, so a changed limit
// takes effect at the next sample without a restart. Nothing is stopped
// while the server is already stopping, nor while the daemon shuts down
// or hands its servers to a new binary.
func (s *Supervisor) overMemoryLocked(p *proc, mem int64) []effect {
	limit := p.cfg.MemoryLimit
	if limit.Bytes <= 0 || mem <= limit.Bytes || p.live() == nil || s.closing || p.stopping() != nil || p.next() != nil {
		return nil
	}
	if p.ending != nil && !isClosed(p.ending) {
		return nil
	}
	note := "used " + limit.Over(mem) + ", over memory_limit " + limit.String()
	effs := s.transitionLocked(p, evOverMemory{note: note})
	stamp := time.Now().Format("15:04:05")
	_, _ = p.logs.Write([]byte("\r\n\x1b[31m── " + stamp + "  " + note + ", stopping ──\x1b[0m\r\n"))
	log.Printf("%s: %s; stopping it", p.cfg.Name, note)
	s.saveLocked()
	return effs
}
