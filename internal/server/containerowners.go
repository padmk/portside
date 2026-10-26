package server

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/padmk/portside/internal/config"
	"github.com/padmk/portside/internal/docker"
)

// attachContainers chooses the closest project directory, keeping servers
// sharing that directory together. Explicit project names take precedence.
func attachContainers(containers []docker.Container, servers []config.Server) {
	byDir := map[string][]string{}
	composeDir := map[string]bool{}
	byProject := map[string][]string{}
	byLocalProject := map[string][]string{}
	dirOf := map[string]string{}
	for _, sv := range servers {
		dir := canonicalDir(sv.Dir)
		dirOf[sv.Name] = dir
		names := byDir[dir]
		if config.IsComposeCommand(sv.Cmd) && !composeDir[dir] {
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
		names := closestFirst(byProject[c.Project], dirOf, c.WorkingDir)
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
			names = closestFirst(byLocalProject[c.LocalProject], dirOf, c.WorkingDir)
		}
		if len(names) > 0 {
			c.Server, c.SharedWith = names[0], slices.Clone(names[1:])
		}
	}
}

// publishedBy reports whether a running container publishing port belongs
// to server, by the same ownership rules as the dashboard. A container that
// no server owns, such as one from a compose file outside every server's
// directory, also counts, as its project cannot be matched to a server.
// Before that, a container whose Compose working directory is server's own
// directory, or whose project is one of server's `projects`, is server's
// even when the closest-directory rule would give it to another server
// (a compose file under another server's directory, say).
func (s *Server) publishedBy(server string, port int) bool {
	if s.opt.Docker == nil {
		return false
	}
	containers := s.opt.Docker.Publishing(port)
	defs := s.sup.Definitions()
	if i := slices.IndexFunc(defs, func(sv config.Server) bool { return sv.Name == server }); i >= 0 {
		own := defs[i]
		dir := canonicalDir(own.Dir)
		for _, c := range containers {
			if (c.WorkingDir != "" && canonicalDir(c.WorkingDir) == dir) || (c.Project != "" && slices.Contains(own.Projects, c.Project)) {
				return true
			}
		}
	}
	attachContainers(containers, defs)
	unowned := ""
	for _, c := range containers {
		if slices.Contains(c.Servers(), server) {
			return true
		}
		if c.Server == "" {
			unowned = c.Name
		}
	}
	if unowned != "" {
		log.Printf("%s: port %d is published by container %s, which no server's project matches; treating it as this server's", server, port, unowned)
		return true
	}
	return false
}

// closestFirst orders servers sharing a project so that those whose
// directory holds the container's working directory come first, the deepest
// first. A temporary server in a worktree nested in its origin's checkout
// thus owns the containers started from that worktree.
func closestFirst(names []string, dirOf map[string]string, workingDir string) []string {
	if len(names) < 2 || workingDir == "" {
		return names
	}
	wd := canonicalDir(workingDir)
	depth := func(name string) int {
		dir := dirOf[name]
		if wd == dir || strings.HasPrefix(wd, dir+string(filepath.Separator)) {
			return len(dir)
		}
		return -1
	}
	out := slices.Clone(names)
	slices.SortStableFunc(out, func(a, b string) int { return depth(b) - depth(a) })
	return out
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
			s.sup.NotifyUser(c.Name+" "+reason, message)
		}
		previous = next
	}
}
