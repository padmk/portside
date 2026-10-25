package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/padmk/portside/internal/docker"
	"github.com/padmk/portside/internal/server"
	"github.com/padmk/portside/internal/supervisor"
)

// scope limits the tools to the projects under some directories: their
// configured servers, the temporary copies of those servers, the
// containers those servers own, and listeners working in the directories.
// Directories are canonical (home expanded, absolute, symlinks resolved);
// no directories means every project.
type scope struct{ dirs []string }

// newScope resolves the directories given to --dir; each must exist.
func newScope(dirs []string) (scope, error) {
	var s scope
	for _, d := range dirs {
		if strings.TrimSpace(d) == "" {
			return scope{}, errors.New("--dir needs a path")
		}
		c := canonical(d)
		if st, err := os.Stat(c); err != nil || !st.IsDir() {
			return scope{}, fmt.Errorf("--dir %s: not a directory", d)
		}
		if !slices.Contains(s.dirs, c) {
			s.dirs = append(s.dirs, c)
		}
	}
	return s, nil
}

func (s scope) all() bool { return len(s.dirs) == 0 }

// holds reports whether dir lies in one of the scope's directories.
func (s scope) holds(dir string) bool {
	if s.all() {
		return true
	}
	if dir == "" {
		return false
	}
	d := canonical(dir)
	return slices.ContainsFunc(s.dirs, func(root string) bool { return inside(d, root) })
}

// servers returns the names of the servers in scope among views: those
// whose directory is in it, and temporary copies of those.
func (s scope) servers(views []supervisor.View) map[string]bool {
	in := map[string]bool{}
	for _, v := range views {
		if !v.Temporary && s.holds(v.Dir) {
			in[v.Name] = true
		}
	}
	for _, v := range views {
		if v.Temporary && (in[v.Origin] || s.holds(v.Dir)) {
			in[v.Name] = true
		}
	}
	return in
}

// owns reports whether a container belongs to a server in scope.
func owns(in map[string]bool, c docker.Container) bool {
	return slices.ContainsFunc(c.Servers(), func(n string) bool { return in[n] })
}

// visible narrows a container's view to the servers in scope.
func visible(in map[string]bool, c Container) Container {
	keep := func(names []string) []string {
		return slices.DeleteFunc(slices.Clone(names), func(n string) bool { return !in[n] })
	}
	c.Shared, c.IssueFor = keep(c.Shared), keep(c.IssueFor)
	return c
}

// inScope returns the servers in scope from the current state, or nil
// when the scope is every project.
func (t *tools) inScope(ctx context.Context, g target) (map[string]bool, error) {
	if t.scope.all() {
		return nil, nil
	}
	st, err := g.state(ctx)
	if err != nil {
		return nil, err
	}
	return t.scope.servers(st.Servers), nil
}

// checkServer refuses a server outside the scope with the daemon's own
// error for an unknown name.
func (t *tools) checkServer(ctx context.Context, g target, name string) error {
	in, err := t.inScope(ctx, g)
	if err != nil || in == nil {
		return err
	}
	if !in[name] {
		return fmt.Errorf("unknown server %q", name)
	}
	return nil
}

// checkContainer refuses a container (by name or ID) that no server in
// scope owns, with the daemon's own error for an unknown container.
func (t *tools) checkContainer(ctx context.Context, g target, ref string) error {
	if t.scope.all() {
		return nil
	}
	st, err := g.state(ctx)
	if err != nil {
		return err
	}
	in := t.scope.servers(st.Servers)
	for _, c := range st.Docker.Containers {
		if (c.ID == ref || c.Name == ref) && owns(in, c) {
			return nil
		}
	}
	return fmt.Errorf("no container %q", ref)
}

// listener reports whether a listener is in scope: it belongs to a server
// or a container of one in scope, or its process works in the scope.
func (s scope) listener(in map[string]bool, l server.ListenerView) bool {
	if in[l.Server] || (l.Container != nil && in[l.Container.Server]) {
		return true
	}
	return s.holds(l.Cwd)
}
