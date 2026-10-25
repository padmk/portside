package supervisor

import (
	"context"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/padmk/portside/internal/config"
	"github.com/padmk/portside/internal/ports"
)

// procSample is one row of `ps`: resident memory in bytes and CPU percent.
type procSample struct {
	ppid int
	rss  int64
	cpu  float64
}

// sampleProcesses reads the process table once. It works on macOS and Linux.
func sampleProcesses(ctx context.Context) map[int]procSample {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "ps", "-A", "-o", "pid=,ppid=,rss=,%cpu=").Output()
	if err != nil {
		return nil
	}
	res := make(map[int]procSample, 512)
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) < 4 {
			continue
		}
		pid, err1 := strconv.Atoi(f[0])
		ppid, err2 := strconv.Atoi(f[1])
		rss, err3 := strconv.ParseInt(f[2], 10, 64)
		cpu, err4 := strconv.ParseFloat(strings.Replace(f[3], ",", ".", 1), 64)
		if err1 != nil || err2 != nil || err3 != nil || err4 != nil {
			continue
		}
		res[pid] = procSample{ppid: ppid, rss: rss * 1024, cpu: cpu}
	}
	return res
}

// treeUsage sums CPU and memory over root and all of its descendants.
func treeUsage(table map[int]procSample, children map[int][]int, root int) (cpu float64, mem int64, alive bool) {
	if _, ok := table[root]; !ok {
		return 0, 0, false
	}
	stack := []int{root}
	seen := map[int]bool{}
	for len(stack) > 0 {
		p := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if seen[p] {
			continue
		}
		seen[p] = true
		s := table[p]
		cpu += s.cpu
		mem += s.rss
		stack = append(stack, children[p]...)
	}
	return cpu, mem, true
}

func childIndex(table map[int]procSample) map[int][]int {
	idx := make(map[int][]int, len(table))
	for pid, s := range table {
		idx[s.ppid] = append(idx[s.ppid], pid)
	}
	return idx
}

// history is a fixed-size ring of recent samples for sparklines, evenly
// spaced: a sample after a longer gap starts it over.
type history struct {
	cpu []float64
	mem []int64
	at  time.Time // when the last sample was added
}

const historyLen = 60

// SampleInterval is how often usage is sampled while a client shows it,
// and how often ports are checked; histories hold historyLen samples at
// this spacing.
const SampleInterval = 2 * time.Second

// IdleSampleInterval is how often usage is sampled while no client shows it.
const IdleSampleInterval = 30 * time.Second

// historyGap is the longest spacing kept within one history.
const historyGap = 3 * SampleInterval

func (h *history) add(cpu float64, mem int64, now time.Time) {
	if !h.at.IsZero() && now.Sub(h.at) > historyGap {
		h.reset()
	}
	h.at = now
	h.cpu = append(h.cpu, cpu)
	h.mem = append(h.mem, mem)
	if len(h.cpu) > historyLen {
		h.cpu = h.cpu[len(h.cpu)-historyLen:]
		h.mem = h.mem[len(h.mem)-historyLen:]
	}
}

func (h *history) reset() { h.cpu, h.mem, h.at = nil, nil, time.Time{} }

// WatchUsage registers a client that shows resource usage now, such as an
// open dashboard, and returns a function that unregisters it. While any is
// registered usage is sampled every SampleInterval; the first one gets a
// sample at once.
func (s *Supervisor) WatchUsage() func() {
	s.mu.Lock()
	s.usageWatchers++
	first := s.usageWatchers == 1
	s.mu.Unlock()
	if first {
		select {
		case s.sampleNow <- struct{}{}:
		default:
		}
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			s.mu.Lock()
			s.usageWatchers--
			s.mu.Unlock()
		})
	}
}

// Run performs periodic bookkeeping until ctx ends: resource sampling,
// listening checks and detection of foreign processes on configured ports.
func (s *Supervisor) Run(ctx context.Context) {
	tick := time.NewTicker(SampleInterval)
	defer tick.Stop()
	go func() {
		prune := time.NewTicker(PruneInterval)
		defer prune.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-prune.C:
				s.pruneTemporary()
			}
		}
	}()
	knownExternal := map[string]int{}
	var lastUsage time.Time
	for {
		s.mu.Lock()
		watched := s.usageWatchers > 0
		s.mu.Unlock()
		usage := watched || time.Since(lastUsage) >= IdleSampleInterval-SampleInterval/2
		if usage {
			lastUsage = time.Now()
		}
		s.check(ctx, knownExternal, usage)
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		case <-s.sampleNow:
		}
	}
}

// sample samples usage and checks the ports of all servers.
func (s *Supervisor) sample(ctx context.Context, knownExternal map[string]int) {
	s.check(ctx, knownExternal, true)
}

// check checks the ports of all servers and, with usage, samples the CPU
// and memory of their process trees (one ps call for all) and which tool a
// restart would use. It signals a change only when something changed.
func (s *Supervisor) check(ctx context.Context, knownExternal map[string]int, usage bool) {
	var table map[int]procSample
	var children map[int][]int
	if usage {
		table = sampleProcesses(ctx)
		children = childIndex(table)
	}
	now := time.Now()

	type check struct {
		name  string
		port  int
		alive bool
		pid   int
		start string
	}
	var checks []check
	changed := false
	forwarded := map[string]bool{} // stopped servers whose port forwards to another computer
	type detection struct {
		name string
		pid  int
		cfg  config.Server
	}
	var detections []detection
	s.mu.Lock()
	prepend, globalEnv := s.cfg.PathPrepend, s.cfg.Env
	for name, p := range s.procs {
		if p.pid != 0 && usage {
			if p.launch != nil {
				detections = append(detections, detection{name, p.pid, p.cfg})
			}
			cpu, mem, ok := treeUsage(table, children, p.pid)
			if ok {
				p.cpu, p.mem = cpu, mem
				p.hist.add(cpu, mem, now)
				changed = true
			}
		}
		// watchReady keeps the listener of a starting or running server current.
		if p.cfg.Port > 0 && p.status != Starting && p.status != Stopping && !(p.pid != 0 && p.status == Running) {
			port := p.cfg.Port
			if p.pid != 0 {
				port = p.running.Port
			}
			pid := 0
			if p.external != nil {
				pid = p.external.PID
			}
			checks = append(checks, check{name, port, p.pid != 0, pid, p.externalStart})
			forwarded[name] = s.forwardedFromLocked(p) != ""
		}
	}
	s.mu.Unlock()

	if len(detections) > 0 {
		base := s.env.get()
		// Files such as a new .envrc change which tool a restart would use.
		for _, d := range detections {
			env := launchEnv(base, d.cfg, prepend, globalEnv)
			key := detectTool(d.cfg, lookupEnv(env, "PATH"), nvmScript(env)).key()
			s.mu.Lock()
			if p := s.procs[d.name]; p != nil && p.pid == d.pid && (p.detected == nil || *p.detected != key) {
				p.detected = &key
				changed = true
			}
			s.mu.Unlock()
		}
	}
	for _, c := range checks {
		// A forwarded port answers from the other computer; nothing here holds it.
		listening := !forwarded[c.name] && ports.Listening(c.port)
		var owner *ports.Listener
		if !c.alive && listening {
			if knownExternal[c.name] != c.port || c.pid == 0 || c.start == "" || processStart(c.pid) != c.start {
				if l, ok := ports.Owner(ctx, c.port); ok {
					owner = &l
				} else {
					owner = &ports.Listener{Port: c.port}
				}
			}
		}
		identity := ""
		if owner != nil {
			identity = processStart(owner.PID)
		}
		s.mu.Lock()
		p, ok := s.procs[c.name]
		if ok && c.alive == (p.pid != 0) {
			if c.alive {
				changed = changed || p.listening != listening
				p.listening = listening
			} else if !listening {
				if p.external != nil {
					p.external = nil
					if p.status == Blocked {
						p.status = Stopped
						p.note = ""
					}
					changed = true
				}
				delete(knownExternal, c.name)
			} else if owner != nil {
				changed = changed || p.external == nil || *p.external != *owner || p.externalStart != identity
				p.external = owner
				p.externalStart = identity
				knownExternal[c.name] = c.port
			}
		}
		s.mu.Unlock()
	}
	if changed {
		s.mu.Lock()
		s.changedLocked()
		s.mu.Unlock()
	}
}
