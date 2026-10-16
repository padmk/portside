package docker

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/creack/pty"

	"github.com/padmk/portside/internal/supervisor"
)

// Job kinds: recreate a compose service, optionally rebuilding its image first.
const (
	JobRecreate = "recreate"
	JobRebuild  = "rebuild"
)

// JobView is the JSON view of the last compose job run for a container.
type JobView struct {
	Kind      string     `json:"kind"`
	Running   bool       `json:"running"`
	Error     string     `json:"error,omitempty"`
	StartedAt time.Time  `json:"startedAt"`
	EndedAt   *time.Time `json:"endedAt,omitempty"`
}

type job struct {
	project string
	service string
	kind    string
	running bool
	err     string
	started time.Time
	ended   time.Time
	logs    *supervisor.LogBuffer
}

func (j *job) view() JobView {
	v := JobView{Kind: j.kind, Running: j.running, Error: j.err, StartedAt: j.started}
	if !j.ended.IsZero() {
		t := j.ended
		v.EndedAt = &t
	}
	return v
}

// Recreate runs `docker compose up --force-recreate` for the container's
// service, with --build when rebuild is set. The engine API has no notion of
// compose projects, so this one operation goes through the docker CLI; its
// output is kept for the dashboard.
func (m *Monitor) Recreate(name string, rebuild bool) error {
	c, ok := m.Find(name)
	if !ok {
		return fmt.Errorf("no container %q", name)
	}
	if !c.Compose() {
		return errors.New("only compose services can be recreated")
	}
	bin := m.host.LookPath("docker")
	if bin == "" {
		return errors.New("the docker command was not found on PATH")
	}
	cl, err := m.Client()
	if err != nil {
		return err
	}
	if m.moved(cl) {
		m.Kick()
		return errors.New("the Docker context changed; try again in a moment")
	}
	args := []string{"compose", "--project-name", c.Project, "--project-directory", c.WorkingDir}
	for _, f := range c.configFiles {
		args = append(args, "--file", f)
	}
	for _, f := range c.envFiles {
		args = append(args, "--env-file", f)
	}
	args = append(args, "up", "--detach", "--no-deps", "--force-recreate")
	if rebuild {
		args = append(args, "--build")
	}
	args = append(args, c.Service)

	kind := JobRecreate
	if rebuild {
		kind = JobRebuild
	}
	m.mu.Lock()
	// One compose up per service: it recreates every replica.
	for _, j := range m.jobs {
		if j.running && j.project == c.Project && j.service == c.Service {
			m.mu.Unlock()
			return errors.New("a recreate is already running for " + c.Service)
		}
	}
	j := &job{project: c.Project, service: c.Service, kind: kind, running: true, started: time.Now(), logs: supervisor.NewMemoryLog()}
	m.jobs[c.Name] = j
	m.changedLocked()
	m.mu.Unlock()

	cmd := exec.Command(bin, args...)
	cmd.Dir = c.WorkingDir
	// Pin the CLI to the engine the dashboard shows instead of the context
	// it would pick on its own.
	env := []string{}
	for _, kv := range m.host.Environ() {
		if !strings.HasPrefix(kv, "DOCKER_HOST=") && !strings.HasPrefix(kv, "DOCKER_CONTEXT=") {
			env = append(env, kv)
		}
	}
	cmd.Env = append(env, "DOCKER_HOST="+cl.ep.Host, "TERM=xterm-256color", "COLORTERM=truecolor")
	fmt.Fprintf(j.logs, "\x1b[2m$ docker %s\x1b[0m\r\n", strings.Join(args, " "))
	f, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: 120, Rows: 32})
	if err != nil {
		m.finish(c.Name, j, err)
		return err
	}
	copied := make(chan struct{})
	go func() {
		_, _ = io.Copy(j.logs, f)
		close(copied)
	}()
	go func() {
		err := cmd.Wait()
		// Let the last output (often the error) reach the log before the
		// end marker.
		select {
		case <-copied:
		case <-time.After(2 * time.Second):
		}
		_ = f.Close()
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			err = fmt.Errorf("exited with code %d", ee.ExitCode())
		}
		m.finish(c.Name, j, err)
	}()
	return nil
}

func (m *Monitor) finish(name string, j *job, err error) {
	m.mu.Lock()
	j.running = false
	j.ended = time.Now()
	stamp := j.ended.Format("15:04:05")
	if err != nil && !errors.Is(err, os.ErrClosed) {
		j.err = err.Error()
		fmt.Fprintf(j.logs, "\r\n\x1b[31m── %s  %s ──\x1b[0m\r\n", stamp, j.err)
	} else {
		fmt.Fprintf(j.logs, "\r\n\x1b[2m── %s  done ──\x1b[0m\r\n", stamp)
	}
	m.changedLocked()
	m.mu.Unlock()
	m.Kick()
}

// JobLog returns the output of the last job for a container.
func (m *Monitor) JobLog(name string) (*supervisor.LogBuffer, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if j := m.jobs[name]; j != nil {
		return j.logs, true
	}
	return nil, false
}

// DismissJob forgets a finished job.
func (m *Monitor) DismissJob(name string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if j := m.jobs[name]; j != nil && !j.running {
		delete(m.jobs, name)
		m.changedLocked()
	}
}
