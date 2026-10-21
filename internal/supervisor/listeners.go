package supervisor

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"syscall"
	"time"

	"github.com/padmk/portside/internal/config"
	"github.com/padmk/portside/internal/ports"
)

// IsManagedPID reports whether pid belongs to a process tree Portside runs.
func (s *Supervisor) IsManagedPID(pid int) (string, bool) {
	s.mu.Lock()
	roots := map[int]string{}
	for name, p := range s.procs {
		if p.pid != 0 {
			roots[p.pid] = name
		}
	}
	s.mu.Unlock()
	if len(roots) == 0 {
		return "", false
	}
	table := sampleProcesses(context.Background())
	for cur, hops := pid, 0; cur > 1 && hops < 64; hops++ {
		if name, ok := roots[cur]; ok {
			return name, true
		}
		ps, ok := table[cur]
		if !ok {
			break
		}
		cur = ps.ppid
	}
	return "", false
}

// composeOwnsPort reports whether a container runtime holds the port for a
// server that is itself a compose stack. Its containers may still be up from
// an earlier run; "docker compose up" attaches to them instead of failing.
// When the owner is hidden, as a rootful engine's docker-proxy is from other
// accounts, published asks the engine whether a container of this server's
// compose project publishes the port.
func composeOwnsPort(name, cmd string, port int, published func(server string, port int) bool) bool {
	if !config.IsComposeCommand(cmd) {
		return false
	}
	if owner, ok := ports.Owner(context.Background(), port); ok {
		return IsContainerRuntime(owner.Command)
	}
	return published != nil && published(name, port)
}

// containerRuntimes are parts of process names that publish container ports
// on the host: Docker (docker-proxy, Docker Desktop's com.docker.backend),
// OrbStack, Colima and Lima, Podman and its port forwarders (gvproxy on a
// machine, conmon, pasta or slirp4netns rootless), and rootless Docker's
// rootlesskit and rootlessport.
var containerRuntimes = []string{"docker", "com.docke", "orbstack", "vpnkit", "colima", "lima", "podman", "gvproxy", "conmon", "pasta", "slirp4netns", "rootless"}

// IsContainerRuntime reports whether a process name belongs to a container
// engine that publishes container ports on the host.
func IsContainerRuntime(command string) bool {
	lc := strings.ToLower(command)
	for _, rt := range containerRuntimes {
		if strings.Contains(lc, rt) {
			return true
		}
	}
	return false
}

// protected process names are never killed by takeover: stopping them would
// take down a container engine, AirPlay or the session rather than a stray
// dev server.
var protected = append([]string{"launchd", "controlce", "rapportd", "systemd"}, containerRuntimes...)

// Killable reports why a foreign listener may not be killed, or "" if it may.
func (s *Supervisor) Killable(l ports.Listener) string {
	if l.PID <= 1 || l.PID == s.selfPID {
		return "refusing to kill this process"
	}
	lc := strings.ToLower(l.Command)
	for _, p := range protected {
		if strings.Contains(lc, p) {
			return fmt.Sprintf("%s looks like a system or container service; stop it there", l.Command)
		}
	}
	return ""
}

// KillListener ends a foreign process that holds a port.
func (s *Supervisor) KillListener(l ports.Listener) error {
	return s.killForeign(l)
}

func (s *Supervisor) killForeign(l ports.Listener) error {
	if why := s.Killable(l); why != "" {
		return errors.New(why)
	}
	if name, ok := s.IsManagedPID(l.PID); ok {
		return s.Stop(name)
	}
	// Remember who we are signalling: once that process is gone its pid may be
	// reused, and nothing else must receive the escalation.
	identity := processStart(l.PID)
	if identity == "" {
		return nil // already gone
	}
	same := func() bool { return processStart(l.PID) == identity }
	if err := syscall.Kill(l.PID, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
		return fmt.Errorf("kill %d: %w", l.PID, err)
	}
	for i := 0; i < 50 && same(); i++ {
		time.Sleep(100 * time.Millisecond)
	}
	if same() {
		_ = syscall.Kill(l.PID, syscall.SIGKILL)
	}
	for i := 0; i < 30 && l.Port > 0 && ports.Listening(l.Port); i++ {
		time.Sleep(100 * time.Millisecond)
	}
	if l.Port > 0 && ports.Listening(l.Port) {
		return fmt.Errorf("port %d is still in use", l.Port)
	}
	return nil
}
