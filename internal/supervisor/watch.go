package supervisor

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/padmk/portside/internal/config"
)

// A server with watch patterns restarts when the files they match change.
// The files are polled, which needs no descriptor per file (as kqueue on
// macOS would) and no cgo: every WatchInterval the directories a pattern
// can match are walked, outside the lock, and each file's path, mtime and
// size compared with the previous scan. A change restarts the server once
// a scan finds nothing new at least WatchQuiet after it, so a burst of
// saves gives one restart.

const (
	// WatchInterval is how often a server's watched files are compared, at
	// most. A server whose scan takes longer waits watchShare times as long
	// as it took, times the number of servers scanned, so all scans
	// together use about 2% of a core, and a large tree slows only its
	// own server.
	WatchInterval = time.Second
	watchShare    = 50
	// WatchQuiet is how long changes must have stopped before a restart.
	WatchQuiet = time.Second
	// WatchFileLimit is how many files a server's patterns may match; files
	// past it, in walk order, are not watched.
	WatchFileLimit = 20000
)

// fileStamp is what a scan compares of a file.
type fileStamp struct {
	mod  int64 // mtime in nanoseconds
	size int64
}

// watchState is what the scans of one server found.
type watchState struct {
	// key is the directory and patterns the scans are for; a change starts
	// over with a new baseline.
	key string
	// files is the latest scan; nil until the first one, the baseline.
	files map[string]fileStamp
	// changed holds the files changed since the last restart, and
	// lastChange when the latest scan found one.
	changed    map[string]bool
	lastChange time.Time
	// over says the patterns match more files than the limit; logged says
	// the daemon's log has said so for key.
	over, logged bool
	// restarting is set while a restart this began is under way.
	restarting bool
	// nextScan is when the files are compared next.
	nextScan time.Time
}

// watchMode is what a scan does for a server.
type watchMode int

const (
	watchOff    watchMode = iota // no scans: changes cannot restart it
	watchAbsorb                  // scans keep the baseline, changes restart nothing
	watchOn                      // changes restart it
)

// watchModeLocked tells what changes do to p. A server the user stopped,
// a disabled or removed one, and a task that is not running ignore them; a
// server stopping or restarting already takes them in, so a restart never
// stacks on another.
func watchModeLocked(p *proc) watchMode {
	switch {
	case p.removed || !p.cfg.Enabled:
		return watchOff
	case p.watch.restarting || p.status == Stopping:
		return watchAbsorb
	case p.cfg.Oneshot:
		if p.status == Starting {
			return watchOn
		}
		return watchOff
	case p.status == Starting || p.status == Running || p.status == Crashed || p.status == Exited:
		return watchOn
	}
	return watchOff
}

// runWatches scans the watched files every s.watchEvery until ctx ends,
// and stays idle while no server has watch patterns.
func (s *Supervisor) runWatches(ctx context.Context) {
	tick := time.NewTicker(s.watchEvery)
	defer tick.Stop()
	for {
		if !s.checkWatches(time.Now()) {
			tick.Stop()
			select {
			case <-ctx.Done():
				return
			case <-s.watchWake:
			}
			tick.Reset(s.watchEvery)
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// wakeWatchesLocked lets runWatches look at the servers again after a
// config change.
func (s *Supervisor) wakeWatchesLocked() {
	select {
	case s.watchWake <- struct{}{}:
	default:
	}
}

// watchJob is one server's scan.
type watchJob struct {
	name  string
	p     *proc
	key   string
	dir   string
	watch *config.Watch
}

// watchStart is a start or restart that changed files asked for.
type watchStart struct {
	name  string
	p     *proc
	alive bool
	seq   int
}

// checkWatches scans the watched files of servers that changes can
// restart, at now, and starts what they ask for. It reports whether any
// server has watch patterns, or the daemon is closing (an upgrade in place
// can fail, and the scans then go on).
func (s *Supervisor) checkWatches(now time.Time) bool {
	s.mu.Lock()
	if s.closing {
		s.mu.Unlock()
		return true
	}
	var jobs []watchJob
	watched := false
	active := 0 // servers whose files are compared, due now or not
	for _, name := range s.order {
		p := s.procs[name]
		if p == nil {
			continue
		}
		if p.cfg.Watch == nil {
			p.watch = nil
			continue
		}
		watched = true
		key := p.cfg.Dir + "\x00" + p.cfg.Watch.String()
		if p.watch == nil || p.watch.key != key {
			p.watch = &watchState{key: key}
		}
		if watchModeLocked(p) == watchOff {
			p.watch.files, p.watch.changed = nil, nil
			continue
		}
		active++
		if now.Before(p.watch.nextScan) {
			continue
		}
		jobs = append(jobs, watchJob{name: name, p: p, key: key, dir: p.cfg.Dir, watch: p.cfg.Watch})
	}
	limit := s.watchLimit
	s.mu.Unlock()

	scans := make([]map[string]fileStamp, len(jobs))
	over := make([]bool, len(jobs))
	took := make([]time.Duration, len(jobs))
	for i, j := range jobs {
		began := time.Now()
		scans[i], over[i] = scanWatched(j.dir, j.watch, limit)
		took[i] = time.Since(began)
	}

	s.mu.Lock()
	var starts []watchStart
	changedView := false
	for i, j := range jobs {
		p := j.p
		if s.closing || s.procs[j.name] != p || p.watch == nil || p.watch.key != j.key {
			continue
		}
		w := p.watch
		w.nextScan = now.Add(max(s.watchEvery, took[i]*watchShare*time.Duration(active)))
		mode := watchModeLocked(p)
		if mode == watchOff {
			w.files, w.changed = nil, nil
			continue
		}
		if w.over != over[i] {
			w.over = over[i]
			changedView = true
		}
		if w.over && !w.logged {
			w.logged = true
			log.Printf("watch: %s: the patterns match more than %d files; only the first are watched", j.name, limit)
		}
		if w.files == nil {
			w.files = scans[i]
			continue
		}
		changed := changedFiles(w.files, scans[i])
		w.files = scans[i]
		if mode == watchAbsorb {
			w.changed = nil
			continue
		}
		if len(changed) > 0 {
			if w.changed == nil {
				w.changed = map[string]bool{}
			}
			for _, f := range changed {
				w.changed[f] = true
			}
			w.lastChange = now
			continue
		}
		// A tenth of the quiet period is spare for a scan that began a
		// little sooner after the previous one than the interval.
		if len(w.changed) == 0 || now.Sub(w.lastChange) < s.watchQuiet*9/10 {
			continue
		}
		alive := p.status == Starting || p.status == Running
		verb := "starting"
		if alive {
			verb = "restarting"
		}
		fmt.Fprintf(p.logs, "\r\n\x1b[2m── %s  %s: changed %s ──\x1b[0m\r\n", time.Now().Format("15:04:05"), verb, changedSummary(w.changed))
		w.changed = nil
		w.restarting = true
		starts = append(starts, watchStart{name: j.name, p: p, alive: alive, seq: p.startSeq})
	}
	if changedView {
		s.changedLocked()
	}
	s.mu.Unlock()
	for _, st := range starts {
		go s.watchRestart(st)
	}
	return watched
}

// watchRestart starts or restarts a server for changed files, the way an
// explicit restart or start does (dependencies, port checks, the stop
// sequence, a fresh round of automatic restarts). A start or stop someone
// else asks for meanwhile wins: the server is not started after it.
func (s *Supervisor) watchRestart(st watchStart) {
	defer func() {
		s.mu.Lock()
		if st.p.watch != nil {
			st.p.watch.restarting = false
		}
		s.mu.Unlock()
	}()
	s.mu.Lock()
	closing := s.closing
	s.mu.Unlock()
	if closing {
		return
	}
	p := st.p
	if st.alive {
		// Only the run the changes were seen for: a start or stop since
		// then wins. The start again is reserved before the stop, as a
		// start asked for while a server stops, so the server stays wanted
		// throughout and nothing waiting for it sees it stopped; wait
		// launches it once the group has exited, unless a stop comes first.
		s.mu.Lock()
		if s.procs[st.name] != p || p.startSeq != st.seq || p.pid == 0 || p.stopping || (p.ending != nil && !isClosed(p.ending)) || s.closing || !p.cfg.Enabled || p.removed {
			s.mu.Unlock()
			return
		}
		p.retries = 0
		p.againTrigger = triggerWatch
		s.reserveLocked(p)
		p.stopping = true
		p.status = Stopping
		pid, done, grace := p.pid, p.done, p.running.Stop
		ending := s.beginEndingLocked(p)
		s.changedLocked()
		s.mu.Unlock()
		Terminate(pid, done, grace)
		s.endEnding(ending)
		return
	}
	// Reserved under the lock that checks nothing else started or stopped
	// it since, so a stop in between wins.
	s.mu.Lock()
	if s.procs[st.name] != p || p.startSeq != st.seq || p.alive() || p.pid != 0 || s.closing || !p.cfg.Enabled || p.removed {
		s.mu.Unlock()
		return
	}
	deps, token := s.reserveStartLocked(p, triggerWatch)
	s.mu.Unlock()
	if err := s.launch(st.name, deps, StartOptions{trigger: triggerWatch}, token); err != nil {
		log.Printf("watch: %s: %v", st.name, err)
	}
}

// scanWatched records the files below dir that w matches, at most limit
// of them, walking only directories a pattern can match; it reports
// whether there were more. Symbolic links to directories are not followed.
func scanWatched(dir string, w *config.Watch, limit int) (map[string]fileStamp, bool) {
	files := map[string]fileStamp{}
	over := false
	var walk func(abs, rel string)
	walk = func(abs, rel string) {
		entries, err := os.ReadDir(abs)
		if err != nil {
			return
		}
		for _, e := range entries {
			if over {
				return
			}
			r := e.Name()
			if rel != "" {
				r = rel + "/" + r
			}
			full := filepath.Join(abs, e.Name())
			if e.IsDir() {
				if w.Enter(r) {
					walk(full, r)
				}
				continue
			}
			if !w.Match(r) {
				continue
			}
			info, err := e.Info()
			if err == nil && info.Mode()&os.ModeSymlink != 0 {
				info, err = os.Stat(full)
			}
			if err != nil || info.IsDir() {
				continue
			}
			if len(files) >= limit {
				over = true
				return
			}
			files[r] = fileStamp{mod: info.ModTime().UnixNano(), size: info.Size()}
		}
	}
	walk(dir, "")
	return files, over
}

// changedFiles lists the files added, changed or removed between two
// scans, sorted.
func changedFiles(old, cur map[string]fileStamp) []string {
	var out []string
	for f, st := range cur {
		if was, ok := old[f]; !ok || was != st {
			out = append(out, f)
		}
	}
	for f := range old {
		if _, ok := cur[f]; !ok {
			out = append(out, f)
		}
	}
	slices.Sort(out)
	return out
}

// changedSummary names the first changed file and counts the rest, as in
// "src/app.py (+3 more)".
func changedSummary(changed map[string]bool) string {
	files := make([]string, 0, len(changed))
	for f := range changed {
		files = append(files, f)
	}
	slices.Sort(files)
	if len(files) == 1 {
		return files[0]
	}
	return fmt.Sprintf("%s (+%d more)", files[0], len(files)-1)
}

// watchNote is the note of a server whose patterns match more files than
// the limit.
func watchNote(limit int) string {
	return fmt.Sprintf("watch covers only the first %d files; narrow its patterns", limit)
}

// joinNote adds extra to a server's note.
func joinNote(note, extra string) string {
	if note == "" {
		return extra
	}
	return note + "; " + extra
}
