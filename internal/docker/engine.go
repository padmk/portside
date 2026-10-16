package docker

import (
	"bytes"
	"errors"
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
	if args := m.engineStarter(engineName(ep), ep.Context); args != nil {
		return args, nil
	}
	for _, name := range []string{"OrbStack", "Docker Desktop", "Colima", "Rancher Desktop"} {
		if m.installed(name) {
			if args := m.engineStarter(name, ""); args != nil {
				return args, nil
			}
		}
	}
	if runtime.GOOS == "linux" && m.host.LookPath("systemctl") != "" {
		return []string{m.host.LookPath("systemctl"), "--user", "start", "docker"}, nil
	}
	return nil, errors.New("no container engine found; set docker_start in the config")
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
	}
	return nil
}

func (m *Monitor) installed(name string) bool {
	apps := map[string]string{"OrbStack": "OrbStack.app", "Docker Desktop": "Docker.app", "Rancher Desktop": "Rancher Desktop.app"}
	tools := map[string]string{"OrbStack": "orb", "Colima": "colima", "Rancher Desktop": "rdctl"}
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
