package supervisor

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/padmk/portside/internal/ports"
)

const readinessNoteDelay = 10 * time.Second
const listenerLossDelay = 4 * time.Second

func loopbackHost(host string) string {
	if host == "" {
		return "127.0.0.1"
	}
	return host
}

// probeReady returns the responding loopback address and a readiness failure,
// if any. A health failure can still have a live TCP listener.
func probeReady(port int, health string) (host, reason string) {
	reason = fmt.Sprintf("waiting for port %d", port)
	for _, candidate := range []string{"127.0.0.1", "::1"} {
		addr := net.JoinHostPort(candidate, strconv.Itoa(port))
		c, err := net.DialTimeout("tcp", addr, 250*time.Millisecond)
		if err != nil {
			continue
		}
		c.Close()
		host = candidate
		if health == "" {
			return candidate, ""
		}
		path := "/" + strings.TrimLeft(health, "/")
		client := http.Client{Timeout: 2 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		}}
		resp, err := client.Get("http://" + addr + path)
		if err != nil {
			reason = "health " + path + " is not responding"
			continue
		}
		resp.Body.Close()
		if resp.StatusCode >= 200 && resp.StatusCode < 400 {
			return candidate, ""
		}
		reason = fmt.Sprintf("health %s answers %d", path, resp.StatusCode)
	}
	return host, reason
}

// Readiness is probed every startingProbe until a server is running, then
// every runningProbe, which still reports a lost listener soon after
// listenerLossDelay.
const (
	startingProbe = 400 * time.Millisecond
	runningProbe  = time.Second
)

// watchReady keeps readiness current while tolerating short hot reload gaps.
func (s *Supervisor) watchReady(_ string, p *proc, gen int) {
	every := startingProbe
	tick := time.NewTicker(every)
	defer tick.Stop()
	var lastPorts time.Time
	for range tick.C {
		s.mu.Lock()
		if p.gen != gen || (p.status != Starting && p.status != Running) || p.pid == 0 {
			s.mu.Unlock()
			return
		}
		port, health, started, pid := p.running.Port, p.running.Health, p.startedAt, p.pid
		readyLog := ""
		if p.status == Starting && !p.logReady {
			readyLog = p.running.ReadyLog
		}
		after := p.running.ReadyAfter
		if port == 0 && after == 0 {
			after = 1500 * time.Millisecond
		}
		next := startingProbe
		if p.status == Running {
			next = runningProbe
		}
		s.mu.Unlock()
		if next != every {
			every = next
			tick.Reset(every)
		}

		host, reason := "", ""
		ready := true
		if port > 0 {
			host, reason = probeReady(port, health)
			ready = host != "" && reason == ""
		}
		if readyLog != "" {
			ready = false
			if reason == "" {
				reason = "waiting for a line matching " + readyLog
			}
		}
		if time.Since(started) < after {
			ready = false
		}
		var listenPorts []int
		inspect := !ready && time.Since(started) >= readinessNoteDelay && time.Since(lastPorts) >= 5*time.Second
		if inspect {
			lastPorts = time.Now()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			table := sampleProcesses(ctx)
			for _, l := range ports.List(ctx) {
				if inProcessTree(table, l.PID, pid) && l.Port != port && !slices.Contains(listenPorts, l.Port) {
					listenPorts = append(listenPorts, l.Port)
				}
			}
			cancel()
			slices.Sort(listenPorts)
		}
		s.mu.Lock()
		if p.gen == gen && p.pid != 0 && (p.status == Starting || p.status == Running) {
			s.updateReadinessLocked(p, ready, host, reason, listenPorts, inspect, time.Now())
		}
		s.mu.Unlock()
	}
}

func inProcessTree(table map[int]procSample, pid, root int) bool {
	for hops := 0; pid > 1 && hops < 64; hops++ {
		if pid == root {
			return true
		}
		p, ok := table[pid]
		if !ok {
			return false
		}
		pid = p.ppid
	}
	return false
}

func (s *Supervisor) updateReadinessLocked(p *proc, ready bool, host, reason string, listenPorts []int, inspect bool, now time.Time) {
	oldNote, oldStatus, oldListening, oldHost := p.note, p.status, p.listening, p.listenHost
	p.listening = host != "" && p.running.Port > 0
	if host != "" {
		p.listenHost = host
	}
	if ready {
		p.missingSince = time.Time{}
		p.note = ""
		p.listenPorts = nil
		if p.status == Starting {
			p.status, p.readyAt = Running, now
			s.requestExposureLocked()
		}
	} else {
		if inspect {
			p.listenPorts = listenPorts
		}
		if p.status == Running {
			if p.missingSince.IsZero() {
				p.missingSince = now
			}
			if now.Sub(p.missingSince) >= listenerLossDelay {
				p.note = reason
				if strings.HasPrefix(reason, "waiting for port ") {
					p.note = fmt.Sprintf("not listening on :%d", p.running.Port)
				}
			}
		} else if now.Sub(p.startedAt) >= readinessNoteDelay {
			p.note = reason
			if len(p.listenPorts) > 0 && strings.HasPrefix(reason, "waiting for port ") {
				ps := make([]string, len(p.listenPorts))
				for i, port := range p.listenPorts {
					ps[i] = strconv.Itoa(port)
				}
				p.note = fmt.Sprintf("listening on %s, not %d: does the command use {PORT}?", strings.Join(ps, ", "), p.running.Port)
			}
		}
	}
	if oldNote != p.note || oldStatus != p.status || oldListening != p.listening || oldHost != p.listenHost || inspect {
		s.changedLocked()
	}
}

// ReadyTimeout is how long ensure and restart wait for readiness when the
// caller gives no timeout.
const ReadyTimeout = 90 * time.Second

// WaitReady checks the live listener, including servers already marked running.
func (s *Supervisor) WaitReady(ctx context.Context, name string, timeout time.Duration) error {
	ch := s.Subscribe()
	defer s.Unsubscribe(ch)
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	poll := time.NewTicker(time.Second)
	defer poll.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		s.mu.Lock()
		p, ok := s.procs[name]
		var st Status
		var note string
		var wanted bool
		var port, gen int
		var health string
		if ok {
			st, note, wanted, port, health, gen = p.status, p.note, p.want, p.running.Port, p.running.Health, p.gen
			if st == Crashed || st == Exited {
				note += "\n" + strings.Join(p.logs.Tail(5, 240), "\n")
			}
		}
		s.mu.Unlock()
		switch {
		case !ok:
			return fmt.Errorf("unknown server %q", name)
		case st == Running:
			if port == 0 {
				return nil
			}
			host, reason := probeReady(port, health)
			s.mu.Lock()
			current := p.gen == gen && p.status == Running
			if current {
				s.updateReadinessLocked(p, host != "" && reason == "", host, reason, nil, false, time.Now())
			}
			s.mu.Unlock()
			if !current {
				continue
			}
			if reason != "" {
				if strings.HasPrefix(reason, "waiting for port ") {
					reason = fmt.Sprintf("not listening on :%d", port)
				}
				return fmt.Errorf("%s: %s", name, reason)
			}
			return nil
		case st == Stopped && wanted:
		case st == Crashed, st == Exited, st == Blocked, st == Stopped:
			if note == "" {
				note = string(st)
			}
			return fmt.Errorf("%s: %s", name, strings.TrimSpace(note))
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			if note == "" {
				note = "waiting for readiness"
			}
			return fmt.Errorf("%s: not ready after %s: %s", name, timeout, note)
		case <-ch:
		case <-poll.C:
		}
	}
}

// ActiveAddress is the loopback endpoint used for health, links and proxying.
func (s *Supervisor) ActiveAddress(name string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.procs[name]
	if !ok {
		return "", false
	}
	port := p.cfg.Port
	if p.pid != 0 {
		port = p.running.Port
	}
	if port <= 0 {
		return "", false
	}
	return net.JoinHostPort(loopbackHost(p.listenHost), strconv.Itoa(port)), true
}
