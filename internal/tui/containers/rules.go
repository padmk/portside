// Package containers is the terminal UI's containers pane and the right
// pane for a selected container (design/tui.md "Containers"): the
// daemon's Docker containers grouped by Compose project, their actions,
// logs, details, shell and compose jobs. The rules for which action
// applies when follow the dashboard (web/src/features/containers).
package containers

import (
	"cmp"
	"fmt"
	"regexp"
	"slices"

	"github.com/padmk/portside/internal/docker"
	"github.com/padmk/portside/internal/server"
	"github.com/padmk/portside/internal/supervisor"
	"github.com/padmk/portside/internal/tui/ui"
)

// Status is how a container shows: the display state its glyph and color
// come from, and its word.
type Status struct {
	Display supervisor.Display
	Label   string
}

// StatusOf is a container's status; health refines a running container
// (web/src/features/containers/status.ts).
func StatusOf(c docker.Container) Status {
	d := func(s supervisor.Status) supervisor.Display { return supervisor.Display(s) }
	switch c.State {
	case "running":
		switch c.Health {
		case "unhealthy":
			return Status{d(supervisor.Crashed), "unhealthy"}
		case "starting":
			return Status{d(supervisor.Starting), "checking health"}
		}
		return Status{d(supervisor.Running), "running"}
	case "restarting":
		return Status{d(supervisor.Starting), "restarting"}
	case "removing":
		return Status{d(supervisor.Stopping), "removing"}
	case "paused":
		return Status{d(supervisor.Stopped), "paused"}
	case "created":
		return Status{d(supervisor.Stopped), "created"}
	case "dead":
		return Status{d(supervisor.Crashed), "dead"}
	}
	if c.Failed {
		return Status{d(supervisor.Crashed), "crashed"}
	}
	return Status{d(supervisor.Exited), "stopped"}
}

// canStop reports whether stop and restart apply: the container runs or
// restarts.
func canStop(c docker.Container) bool { return c.State == "running" || c.State == "restarting" }

// usesDocker matches a server command that runs containers, so the engine
// being down matters to it (the dashboard's usesDocker).
var usesDocker = regexp.MustCompile(`\b(docker|docker-compose|podman)\b`)

// Shown reports whether the containers pane belongs on screen: the daemon
// reports containers, or the engine is not ready while it starts or a
// server runs containers.
func Shown(st *server.State) bool {
	if st == nil {
		return false
	}
	d := st.Docker
	if len(d.Containers) > 0 {
		return true
	}
	if d.Status == docker.Ready || d.Status == "" {
		return false
	}
	if d.Starting {
		return true
	}
	return slices.ContainsFunc(st.Servers, func(v supervisor.View) bool { return usesDocker.MatchString(v.Cmd) })
}

// Animating reports whether a container row shows a spinner: a container
// starting, restarting or being removed, or a compose job running.
func Animating(st *server.State) bool {
	if st == nil {
		return false
	}
	for _, c := range st.Docker.Containers {
		if StatusOf(c).Display == supervisor.Display(supervisor.Starting) || c.State == "removing" || (c.Job != nil && c.Job.Running) {
			return true
		}
	}
	return false
}

// Project is a Compose project's containers, or (Name "") the containers
// outside any project.
type Project struct {
	Name string
	// Server is the configured server that owns the project's first
	// container, "" for none.
	Server     string
	Containers []docker.Container
}

// Projects groups the containers by Compose project, projects by name and
// the other containers last, each in the daemon's order (service, name).
func Projects(st *server.State) []Project {
	if st == nil {
		return nil
	}
	var out []Project
	for _, c := range st.Docker.Containers {
		i := slices.IndexFunc(out, func(p Project) bool { return p.Name == c.Project })
		if i < 0 {
			out = append(out, Project{Name: c.Project, Server: c.Server})
			i = len(out) - 1
		}
		out[i].Containers = append(out[i].Containers, c)
	}
	slices.SortStableFunc(out, func(a, b Project) int {
		if (a.Name == "") != (b.Name == "") {
			if a.Name == "" {
				return 1
			}
			return -1
		}
		return cmp.Compare(a.Name, b.Name)
	})
	return out
}

// Find is the container an item names: by id, else by name (a recreated
// container keeps its name and gets a new id).
func Find(st *server.State, it ui.Selection) (docker.Container, bool) {
	if st == nil || it.Kind != ui.SelectContainer {
		return docker.Container{}, false
	}
	for _, c := range st.Docker.Containers {
		if c.ID == it.ID {
			return c, true
		}
	}
	for _, c := range st.Docker.Containers {
		if c.Name == it.Name {
			return c, true
		}
	}
	return docker.Container{}, false
}

// FindProject is a project's containers.
func FindProject(st *server.State, name string) (Project, bool) {
	for _, p := range Projects(st) {
		if p.Name == name {
			return p, true
		}
	}
	return Project{}, false
}

// Item names a container.
func Item(c docker.Container) ui.Selection { return ui.ContainerItem(c.ID, c.Name) }

// ShortName is the name a container goes by in its project: its service,
// else its name.
func ShortName(c docker.Container) string {
	if c.Service != "" {
		return c.Service
	}
	return c.Name
}

// FirstPort is the first published TCP port, 0 for none.
func FirstPort(c docker.Container) int {
	for _, p := range c.Ports {
		if p.Proto == "tcp" && p.Public > 0 {
			return p.Public
		}
	}
	return 0
}

// URL is the address of the first published TCP port as it opens from
// here: unchanged on this computer; for another one its forwarded port,
// or the proxy of the server whose configured port it is
// (ui.Session.PortURL); "" when there is none.
func URL(env *ui.Env, c docker.Container) string {
	p := FirstPort(c)
	if p == 0 {
		return ""
	}
	serverPort := 0
	if env.State != nil && c.Server != "" {
		for _, v := range env.State.Servers {
			if v.Name == c.Server {
				serverPort = v.Port
			}
		}
	}
	return env.Session.PortURL(c.Server, serverPort, p)
}

// jobRunning reports whether a compose job runs for the container's
// project (or the container, outside one): compose runs one at a time.
func jobRunning(st *server.State, c docker.Container) bool {
	if c.Job != nil && c.Job.Running {
		return true
	}
	if c.Project == "" || st == nil {
		return false
	}
	return slices.ContainsFunc(st.Docker.Containers, func(o docker.Container) bool {
		return o.Project == c.Project && o.Job != nil && o.Job.Running
	})
}

// composeRef is the project's container that its compose actions go
// through: the first that belongs to a compose service.
func composeRef(p Project) (docker.Container, bool) {
	for _, c := range p.Containers {
		if c.Compose() {
			return c, true
		}
	}
	return docker.Container{}, false
}

// ToggleVerb is what space does: stop a running container, else start.
func ToggleVerb(c docker.Container) string {
	if canStop(c) {
		return "stop"
	}
	return "start"
}

// ProjectToggleVerb is what space does on a project header: stop when a
// container runs, else start.
func ProjectToggleVerb(p Project) string {
	if slices.ContainsFunc(p.Containers, canStop) {
		return "stop-project"
	}
	return "start-project"
}

// projectVerbs maps the container verbs on a project header to the
// project's.
var projectVerbs = map[string]string{"start": "start-project", "stop": "stop-project"}

// Off is why action id does nothing on item it (a container or a project
// header), "" when it acts: the one rule for keys, menus, the palette
// and the command line.
func Off(env *ui.Env, id string, it ui.Selection) string {
	st := env.State
	if st == nil {
		return "not connected"
	}
	if st.Docker.Status != docker.Ready && id != "dismiss-job" {
		return "Docker is not running; :start-docker starts it"
	}
	if it.Kind == ui.SelectProject {
		return projectOff(st, id, it.Name)
	}
	c, ok := Find(st, it)
	if !ok {
		return "no container named " + it.Name
	}
	busy := jobRunning(st, c)
	job := ""
	if busy {
		job = "a compose job is running for " + cmp.Or(c.Project, c.Name)
	}
	switch id {
	case "toggle":
		return Off(env, ToggleVerb(c), it)
	case "start":
		switch {
		case busy:
			return job
		case canStop(c) || c.State == "paused":
			return "already " + StatusOf(c).Label
		case c.State == "removing":
			return "being removed"
		}
	case "stop", "restart":
		switch {
		case busy:
			return job
		case !canStop(c):
			return "not running"
		}
	case "shell":
		if c.State != "running" {
			return "not running; start it to open a shell"
		}
	case "open", "copy-url":
		switch {
		case FirstPort(c) == 0:
			return "publishes no port"
		case URL(env, c) == "":
			return env.Session.NoAddress(FirstPort(c))
		case id == "open" && c.State != "running":
			return "not running"
		}
	case "recreate", "rebuild", "pull":
		switch {
		case !c.Compose():
			return "not a compose service"
		case id == "rebuild" && !c.Buildable:
			return "no known build; pull instead"
		case busy:
			return job
		}
	case "start-project", "stop-project", "down-project":
		if c.Project == "" {
			return "not in a compose project"
		}
		return projectOff(st, id, c.Project)
	case "dismiss-job":
		switch {
		case c.Job == nil:
			return "no compose job to dismiss"
		case c.Job.Running:
			return "the job is still running"
		}
	default:
		return "not for a container"
	}
	return ""
}

// projectOff is why action id does nothing on a project's header.
func projectOff(st *server.State, id, name string) string {
	p, ok := FindProject(st, name)
	if !ok {
		return "no project named " + name
	}
	if id == "toggle" {
		id = ProjectToggleVerb(p)
	}
	if v, ok := projectVerbs[id]; ok {
		id = v
	}
	switch id {
	case "start-project", "stop-project", "down-project":
	case "restart":
		return "a project has no restart; restart its containers"
	default:
		return "needs a container, not a project header"
	}
	ref, ok := composeRef(p)
	if name == "" || !ok {
		return "not a compose project"
	}
	if jobRunning(st, ref) {
		return "a compose job is running for " + name
	}
	switch id {
	case "start-project":
		if !slices.ContainsFunc(p.Containers, func(c docker.Container) bool { return !canStop(c) }) {
			return "every container runs"
		}
	case "stop-project":
		if !slices.ContainsFunc(p.Containers, canStop) {
			return "nothing running"
		}
	}
	return ""
}

// ProjectVerb resolves an action on a project header to the project's
// action: toggle, start and stop become start-project or stop-project.
func ProjectVerb(st *server.State, id, name string) string {
	if id == "toggle" {
		p, _ := FindProject(st, name)
		return ProjectToggleVerb(p)
	}
	if v, ok := projectVerbs[id]; ok {
		return v
	}
	return id
}

// RunningCount is a project's running containers over all of them.
func RunningCount(p Project) string {
	n := 0
	for _, c := range p.Containers {
		if c.State == "running" {
			n++
		}
	}
	return fmt.Sprintf("%d/%d", n, len(p.Containers))
}

// PaletteActions are the container actions the palette lists for each
// container, and ProjectActions the ones for each compose project; start
// and stop are toggle there, and a container's project actions are its
// project's entries.
var (
	PaletteActions = []string{"toggle", "restart", "shell", "open", "copy-url", "recreate", "rebuild", "pull", "dismiss-job"}
	ProjectActions = []string{"start-project", "stop-project", "down-project"}
)

// Label names action id on a container or project for the palette:
// "Restart container shop-db-1", "Stop project shop".
func Label(st *server.State, id string, it ui.Selection) string {
	if it.Kind == ui.SelectProject {
		switch id {
		case "start-project":
			return "Start project " + it.Name
		case "stop-project":
			return "Stop project " + it.Name
		case "down-project":
			return "Remove the containers of project " + it.Name
		}
		return id + " project " + it.Name
	}
	what := "container " + it.Name
	switch id {
	case "toggle":
		c, _ := Find(st, it)
		if ToggleVerb(c) == "stop" {
			return "Stop " + what
		}
		return "Start " + what
	case "restart":
		return "Restart " + what
	case "shell":
		return "Shell in " + what
	case "open":
		return "Open " + what + " in the browser"
	case "copy-url":
		return "Copy the address of " + what
	case "recreate":
		return "Recreate " + what
	case "rebuild":
		return "Rebuild and recreate " + what
	case "pull":
		return "Pull and recreate " + what
	case "dismiss-job":
		return "Dismiss the job of " + what
	}
	return id + " " + what
}
