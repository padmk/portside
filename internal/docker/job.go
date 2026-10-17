package docker

import (
	"context"
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

// Service job kinds, with optional image builds or pulls.
const (
	JobRecreate = "recreate"
	JobRebuild  = "rebuild"
	JobPull     = "pull"
)

// JobView is the JSON view of the last compose job run for a container.
type JobView struct {
	Kind      string     `json:"kind"`
	Running   bool       `json:"running"`
	Error     string     `json:"error,omitempty"`
	StartedAt time.Time  `json:"startedAt"`
	EndedAt   *time.Time `json:"endedAt,omitempty"`
}

// Job runs one compose operation and retains its terminal output.
type Job struct {
	project string
	service string
	kind    string
	running bool
	err     string
	started time.Time
	ended   time.Time
	logs    *supervisor.LogBuffer
	done    chan struct{}
}

func (j *Job) view() JobView {
	v := JobView{Kind: j.kind, Running: j.running, Error: j.err, StartedAt: j.started}
	if !j.ended.IsZero() {
		t := j.ended
		v.EndedAt = &t
	}
	return v
}

// Recreate runs a compose service job, optionally building or pulling its image.
func (m *Monitor) Recreate(name string, rebuild, pull bool) (*Job, error) {
	c, ok := m.Find(name)
	if !ok {
		return nil, fmt.Errorf("no container %q", name)
	}
	if !c.Compose() {
		return nil, errors.New("only compose services can be recreated")
	}
	if rebuild && !c.Buildable {
		return nil, errors.New("this compose service has no known build")
	}
	if rebuild && pull {
		return nil, errors.New("choose either build or pull")
	}
	kind := JobRecreate
	if rebuild {
		kind = JobRebuild
	}
	if pull {
		kind = JobPull
	}
	return m.runCompose(c, kind)
}

// ProjectAction starts, stops or removes an existing compose project's containers.
// Removal never asks compose to remove volumes.
func (m *Monitor) ProjectAction(ref, action string) (*Job, error) {
	c, ok := m.Find(ref)
	if !ok {
		return nil, fmt.Errorf("no container %q", ref)
	}
	if !c.Compose() {
		return nil, errors.New("only compose projects support stack actions")
	}
	switch action {
	case "start", "stop", "down":
	default:
		return nil, errors.New("invalid project action")
	}
	return m.runCompose(c, action)
}

func composeArgs(c Container, kind string) []string {
	args := []string{"compose", "--project-name", c.Project, "--project-directory", c.WorkingDir}
	for _, f := range c.configFiles {
		args = append(args, "--file", f)
	}
	for _, f := range c.envFiles {
		args = append(args, "--env-file", f)
	}
	switch kind {
	case "start", "stop", "down":
		return append(args, "--profile", "*", kind)
	}
	args = append(args, "up", "--detach", "--no-deps", "--force-recreate")
	if kind == JobRebuild {
		args = append(args, "--build")
	}
	if kind == JobPull {
		args = append(args, "--pull", "always")
	}
	return append(args, c.Service)
}

func (m *Monitor) runCompose(c Container, kind string) (*Job, error) {
	bin := m.host.LookPath("docker")
	if bin == "" {
		return nil, errors.New("the docker command was not found on PATH")
	}
	cl, err := m.Client()
	if err != nil {
		return nil, err
	}
	if m.moved(cl) {
		m.Kick()
		return nil, errors.New("the Docker context changed; try again in a moment")
	}
	args := composeArgs(c, kind)
	service := c.Service
	if kind == "start" || kind == "stop" || kind == "down" {
		service = ""
	}
	m.mu.Lock()
	// One compose up per service: it recreates every replica.
	for _, j := range m.jobs {
		if j.running && j.project == c.Project && (j.service == service || j.service == "" || service == "") {
			m.mu.Unlock()
			return nil, errors.New("a compose job is already running for project " + c.Project)
		}
	}
	j := &Job{project: c.Project, service: service, kind: kind, running: true, started: time.Now(), logs: supervisor.NewMemoryLog(), done: make(chan struct{})}
	if kind == "stop" || kind == "down" {
		for _, e := range m.entries {
			if e.c.Project == c.Project && (e.c.State == "running" || e.c.State == "paused" || e.c.State == "restarting") {
				m.markStoppedLocked(e.c.ID, time.Now())
			}
		}
	}
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
		return nil, err
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
	return j, nil
}

func (m *Monitor) finish(name string, j *Job, err error) {
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
	close(j.done)
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

// JobResult includes a bounded plain-text tail of a completed compose job.
type JobResult struct {
	JobView
	Output string `json:"output"`
}

// Wait returns a bounded output tail for this exact job, even if a later job
// replaces it in the dashboard before the client receives the response.
func (j *Job) Wait(ctx context.Context) (JobResult, error) {
	select {
	case <-ctx.Done():
		return JobResult{}, ctx.Err()
	case <-j.done:
	}
	return JobResult{JobView: j.view(), Output: strings.Join(j.logs.Tail(100, 1000), "\n")}, nil
}
