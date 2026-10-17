package server

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/padmk/portside/internal/config"
	"github.com/padmk/portside/internal/docker"
	"github.com/padmk/portside/internal/supervisor"
)

// attachContainers chooses the closest project directory, keeping servers
// sharing that directory together. Explicit project names take precedence.
func attachContainers(containers []docker.Container, servers []config.Server) {
	byDir := map[string][]string{}
	composeDir := map[string]bool{}
	byProject := map[string][]string{}
	byLocalProject := map[string][]string{}
	for _, sv := range servers {
		dir := canonicalDir(sv.Dir)
		names := byDir[dir]
		if supervisor.IsComposeCommand(sv.Cmd) && !composeDir[dir] {
			composeDir[dir] = true
			names = append([]string{sv.Name}, names...)
		} else {
			names = append(names, sv.Name)
		}
		byDir[dir] = names
		for _, project := range sv.Projects {
			byProject[project] = append(byProject[project], sv.Name)
		}
		for _, project := range localProjects(dir) {
			byLocalProject[project] = append(byLocalProject[project], sv.Name)
		}
	}
	for i := range containers {
		c := &containers[i]
		c.Server, c.SharedWith = "", nil
		names := byProject[c.Project]
		if len(names) == 0 && c.WorkingDir != "" {
			dir := canonicalDir(c.WorkingDir)
			for {
				if len(byDir[dir]) > 0 {
					names = byDir[dir]
					break
				}
				parent := filepath.Dir(dir)
				if parent == dir {
					break
				}
				dir = parent
			}
		}
		if len(names) == 0 && c.LocalProject != "" {
			names = byLocalProject[c.LocalProject]
		}
		if len(names) > 0 {
			c.Server, c.SharedWith = names[0], slices.Clone(names[1:])
		}
	}
}

// localProjects reads local CLI project IDs without crossing the repository
// boundary. A .git file also bounds linked worktrees.
func localProjects(dir string) []string {
	var out []string
	for {
		var cfg struct {
			Project string `toml:"project_id"`
		}
		if _, err := toml.DecodeFile(filepath.Join(dir, "supabase", "config.toml"), &cfg); err == nil && strings.TrimSpace(cfg.Project) != "" && !slices.Contains(out, cfg.Project) {
			out = append(out, cfg.Project)
		}
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return out
}

// WatchContainerIssues notifies once per unhealthy or failed transition for
// attached containers. A reconnect retains the previous observations.
func (s *Server) WatchContainerIssues(ctx context.Context) {
	changes := s.opt.Docker.Subscribe()
	defer s.opt.Docker.Unsubscribe(changes)
	previous := map[string]string{}
	for {
		select {
		case <-ctx.Done():
			return
		case <-changes:
		}
		st := s.dockerState(s.sup.Snapshot(s.opt.Port))
		if st.Status != docker.Ready || len(st.Containers) == 0 {
			continue
		}
		next := map[string]string{}
		for _, c := range st.Containers {
			if c.Server == "" {
				continue
			}
			reason := ""
			if c.State == "running" && c.Health == "unhealthy" {
				reason = "unhealthy"
			}
			if c.Failed {
				reason = "failed"
			}
			next[c.ID] = reason
			if reason == "" || previous[c.ID] == reason {
				continue
			}
			message := fmt.Sprintf("%s: container %s is %s", strings.Join(c.Servers(), ", "), c.Name, reason)
			if c.ExitCode != nil {
				message += fmt.Sprintf(" (exit code %d)", *c.ExitCode)
			}
			s.sup.NotifyProblem(c.Name+" "+reason, message)
		}
		previous = next
	}
}
