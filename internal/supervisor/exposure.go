package supervisor

import (
	"context"
	"time"

	"github.com/padmk/portside/internal/discover"
	"github.com/padmk/portside/internal/ports"
)

// ExposureInterval is how often the listening addresses of running servers
// are read again; a server that becomes ready is read at once.
const ExposureInterval = IdleSampleInterval

// NetworkWarning reports whether a listener with exposure e should be
// pointed out: it is reachable from the network and its server does not set
// `lan = true`. Tailnet-only and loopback listeners are not.
func NetworkWarning(e ports.Exposure, lan bool) bool {
	return e == ports.Network && !lan
}

// SetTailnet sets how checkExposure learns this machine's Tailscale
// addresses, so a listener on one of them counts as tailnet; without it
// such listeners count as reachable from the network.
func (s *Supervisor) SetTailnet(own ports.TailnetAddrs) {
	s.mu.Lock()
	s.tailnet = own
	s.mu.Unlock()
}

// requestExposureLocked asks Run to read listening addresses at its next
// check, which it makes at once.
func (s *Supervisor) requestExposureLocked() {
	s.exposureWanted = true
	select {
	case s.sampleNow <- struct{}{}:
	default:
	}
}

// exposureDue reports whether check should read listening addresses: when
// asked, and every ExposureInterval along with a usage sample (whose process
// table it reuses), so it adds one lsof call and no ps call.
func (s *Supervisor) exposureDue(usage bool, now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.exposureWanted || (usage && now.Sub(s.exposureAt) >= ExposureInterval-SampleInterval/2)
}

// checkExposure classifies the listening addresses of every running
// server: those on its port, whoever holds them (a compose stack's are held
// by the container runtime), and those of its process tree on other ports.
// table may be nil, in which case it is read here.
func (s *Supervisor) checkExposure(ctx context.Context, table map[int]procSample, now time.Time) {
	type target struct {
		name     string
		pid, gen int
		port     int
		dir, cmd string
	}
	var targets []target
	s.mu.Lock()
	s.exposureWanted, s.exposureAt = false, now
	own := s.tailnet
	for name, p := range s.procs {
		if p.pid != 0 && p.status == Running {
			targets = append(targets, target{name, p.pid, p.gen, p.running.Port, p.running.Dir, p.running.Cmd})
		}
	}
	s.mu.Unlock()
	if len(targets) == 0 {
		return
	}
	socks := ports.Sockets(ctx)
	if table == nil {
		table = sampleProcesses(ctx)
	}
	found := make([]ports.Exposure, len(targets))
	kinds := make([]string, len(targets))
	for i, t := range targets {
		found[i] = serverExposure(socks, t.port, func(pid int) bool { return inProcessTree(table, pid, t.pid) }, own)
		if found[i] == ports.Network {
			// The dashboard's fix hint depends on the tool.
			kinds[i] = discover.CommandKind(t.dir, t.cmd)
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	changed := false
	for i, t := range targets {
		if p := s.procs[t.name]; p != nil && p.gen == t.gen && p.pid == t.pid && (p.exposure != found[i] || p.kind != kinds[i]) {
			p.exposure, p.kind = found[i], kinds[i]
			changed = true
		}
	}
	if changed {
		s.changedLocked()
	}
}

// serverExposure classifies the addresses of the listeners on port and of
// those owned by the server's processes (owns) on any port; own gives this
// machine's Tailscale addresses.
func serverExposure(socks []ports.Listener, port int, owns func(pid int) bool, own ports.TailnetAddrs) ports.Exposure {
	var addrs []string
	for _, l := range socks {
		if (port > 0 && l.Port == port) || owns(l.PID) {
			addrs = append(addrs, l.Addresses...)
		}
	}
	return ports.Classify(addrs, own)
}
