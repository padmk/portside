package docker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/padmk/portside/internal/supervisor"
)

// startWait is how long a requested engine start is reported as in progress.
const startWait = 90 * time.Second

// startTimeout bounds a start command that never returns.
const startTimeout = 5 * time.Minute

var errTimedOut = errors.New("the start command did not finish in 5 minutes")

// StartEngine launches the container engine behind the active context, or
// the configured start command. It returns once the launch has begun; the
// monitor reports when the engine answers.
func (m *Monitor) StartEngine() error {
	m.mu.Lock()
	ep := m.ep
	busy := m.status == Ready || (!m.startedAt.IsZero() && time.Since(m.startedAt) < startWait)
	m.mu.Unlock()
	if busy {
		return nil
	}
	if ep.Host == "" {
		ep, _ = Resolve(m.getenv)
	}
	args, err := m.startCommand(ep)
	if err != nil {
		return err
	}
	var out bytes.Buffer
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Env = m.host.Environ()
	cmd.Dir, _ = os.UserHomeDir()
	cmd.Stdout, cmd.Stderr = &out, &out
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	// A child left holding the output pipe must not keep Wait from returning.
	cmd.WaitDelay = 2 * time.Second
	if err := cmd.Start(); err != nil {
		return err
	}
	m.mu.Lock()
	m.startedAt = time.Now()
	m.startErr = ""
	m.changedLocked()
	m.mu.Unlock()
	go func() {
		// Some starters (colima, systemctl) block until the engine is up;
		// their failure output is the most useful message there is.
		done := make(chan struct{})
		var err error
		go func() {
			err = cmd.Wait()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(startTimeout):
			supervisor.Terminate(cmd.Process.Pid, done, 5*time.Second)
			err = errTimedOut
		}
		if err != nil {
			msg := strings.TrimSpace(out.String())
			if lines := strings.Split(msg, "\n"); len(lines) > 3 {
				msg = strings.Join(lines[len(lines)-3:], "\n")
			}
			if msg == "" || err == errTimedOut {
				msg = err.Error()
			}
			m.mu.Lock()
			if m.status != Ready {
				m.startedAt = time.Time{}
				m.startErr = "start failed: " + msg
				m.changedLocked()
			}
			m.mu.Unlock()
		}
		m.Kick()
	}()
	m.Kick()
	return nil
}

// startCommand picks how to start the engine: the configured command, the
// tool matching the active context, or the first engine installed.
func (m *Monitor) startCommand(ep Endpoint) ([]string, error) {
	if c := strings.TrimSpace(m.custom()); c != "" {
		return []string{"/bin/sh", "-c", c}, nil
	}
	if ep.Network == "ssh" || (ep.Network == "tcp" && !isLoopback(ep.Address)) {
		return nil, errors.New("the engine runs on another computer; start it there")
	}
	name := engineName(ep)
	if args := m.engineStarter(name, ep.Context); args != nil {
		return args, nil
	}
	if name == "Docker" && runtime.GOOS == "linux" {
		if systemctl := m.host.LookPath("systemctl"); systemctl != "" {
			// Rootless Docker is a user unit; the usual rootful one is a
			// system unit, which needs root to start.
			if m.unitExists(systemctl, true, "docker.service") {
				return []string{systemctl, "--user", "start", "docker"}, nil
			}
			if m.unitExists(systemctl, false, "docker.service") {
				return nil, errors.New("Docker runs as a system service: run `sudo systemctl start docker`")
			}
		}
	}
	for _, name := range []string{"OrbStack", "Docker Desktop", "Colima", "Rancher Desktop", "Podman"} {
		if m.installed(name) {
			if args := m.engineStarter(name, ""); args != nil {
				return args, nil
			}
		}
	}
	return nil, errors.New("no container engine found; set docker_start in the config")
}

// unitExists reports whether systemd knows a unit, as a user unit or a
// system one.
func (m *Monitor) unitExists(systemctl string, user bool, unit string) bool {
	args := []string{"cat", unit}
	if user {
		args = []string{"--user", "cat", unit}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, systemctl, args...)
	cmd.Env = m.host.Environ()
	return cmd.Run() == nil
}

func isLoopback(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// explain replaces "not running" with the reason when the engine cannot
// serve the Docker API at all.
func (m *Monitor) explain(ep Endpoint, err error) error {
	if ep.Network != "unix" || describe(err) != "Docker is not running" || !rancherEndpoint(ep) {
		return err
	}
	if m.rancherEngine() == "containerd" {
		return errors.New("Rancher Desktop uses containerd, which has no Docker API: choose dockerd (moby) as its container engine")
	}
	return err
}

// rancherEndpoint reports whether ep is Rancher Desktop's: its context, or
// its socket ~/.rd/docker.sock, also behind /var/run/docker.sock.
func rancherEndpoint(ep Endpoint) bool {
	if ep.Context == "rancher-desktop" {
		return true
	}
	home, _ := os.UserHomeDir()
	sock := filepath.Join(home, ".rd", "docker.sock")
	return ep.Address == sock || ep.Target == sock
}

// rancherEngine reads the container engine chosen in Rancher Desktop's
// settings, "" when it is not installed. The file is in rdctl's config
// directory: ~/Library/Preferences/rancher-desktop on macOS,
// $XDG_CONFIG_HOME/rancher-desktop (default ~/.config) on Linux.
func (m *Monitor) rancherEngine() string {
	home, _ := os.UserHomeDir()
	dir := filepath.Join(home, ".config")
	if runtime.GOOS == "darwin" {
		dir = filepath.Join(home, "Library", "Preferences")
	} else if x := m.getenv("XDG_CONFIG_HOME"); x != "" {
		dir = x
	}
	data, err := os.ReadFile(filepath.Join(dir, "rancher-desktop", "settings.json"))
	if err != nil {
		return ""
	}
	var s struct {
		ContainerEngine struct {
			Name string `json:"name"`
		} `json:"containerEngine"`
	}
	_ = json.Unmarshal(data, &s)
	return s.ContainerEngine.Name
}

func (m *Monitor) engineStarter(name, context string) []string {
	mac := runtime.GOOS == "darwin"
	switch name {
	case "OrbStack":
		if p := m.host.LookPath("orb"); p != "" {
			return []string{p, "start"}
		}
		if mac {
			return []string{"/usr/bin/open", "-g", "-a", "OrbStack"}
		}
	case "Docker Desktop":
		if mac {
			return []string{"/usr/bin/open", "-g", "-a", "Docker"}
		}
		if p := m.host.LookPath("systemctl"); p != "" {
			return []string{p, "--user", "start", "docker-desktop"}
		}
	case "Colima":
		if p := m.host.LookPath("colima"); p != "" {
			if profile, ok := strings.CutPrefix(context, "colima-"); ok && profile != "" {
				return []string{p, "start", "--profile", profile}
			}
			return []string{p, "start"}
		}
	case "Rancher Desktop":
		if p := m.host.LookPath("rdctl"); p != "" {
			return []string{p, "start"}
		}
		if mac {
			return []string{"/usr/bin/open", "-g", "-a", "Rancher Desktop"}
		}
	case "Podman":
		if mac {
			if p := m.host.LookPath("podman"); p != "" {
				return []string{p, "machine", "start"}
			}
		} else if p := m.host.LookPath("systemctl"); p != "" {
			return []string{p, "--user", "start", "podman.socket"}
		}
	}
	return nil
}

func (m *Monitor) installed(name string) bool {
	apps := map[string]string{"OrbStack": "OrbStack.app", "Docker Desktop": "Docker.app", "Rancher Desktop": "Rancher Desktop.app"}
	tools := map[string]string{"OrbStack": "orb", "Colima": "colima", "Rancher Desktop": "rdctl", "Podman": "podman"}
	if runtime.GOOS == "darwin" && apps[name] != "" {
		home, _ := os.UserHomeDir()
		for _, dir := range []string{"/Applications", filepath.Join(home, "Applications")} {
			if _, err := os.Stat(filepath.Join(dir, apps[name])); err == nil {
				return true
			}
		}
	}
	return tools[name] != "" && m.host.LookPath(tools[name]) != ""
}
