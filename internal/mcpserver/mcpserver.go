// Package mcpserver exposes the daemon to coding agents over the Model
// Context Protocol (stdio). Agents can reuse running servers instead of
// launching duplicates, start what they need, and read logs.
package mcpserver

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/padmk/portside/internal/app"
	"github.com/padmk/portside/internal/client"
	"github.com/padmk/portside/internal/docker"
	"github.com/padmk/portside/internal/paths"
	"github.com/padmk/portside/internal/server"
	"github.com/padmk/portside/internal/supervisor"
)

const instructions = `This machine runs local dev servers under ` + app.Name + `.
Before starting a dev server yourself, call list_servers: reuse a server that is running,
or call ensure_server to start it and get its URL. Never launch a second copy of a managed
server in your own shell. Read server_logs (match narrows it to e.g. "error|warn") before
restarting a crashed server. A server lists the containers of its compose project; when one
is unhealthy, restarting or exited with an error, read container_logs. Do not stop or restart
servers the user did not ask about. While this machine is connected to another of the user's
computers, the server and container tools act there when given its name as computer.`

// Run serves MCP on stdin/stdout until the client disconnects.
func Run(ctx context.Context, c *client.Client) error {
	s := mcp.NewServer(&mcp.Implementation{Name: app.ID, Version: app.Version}, &mcp.ServerOptions{Instructions: instructions})
	t := &tools{c: c}

	mcp.AddTool(s, &mcp.Tool{Name: "list_servers", Description: "List managed dev servers with status, URL, port, resource usage and the containers of their compose projects. Pass dir to see only the servers of one checkout, and computer to see another computer's servers while this one is connected to it.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true}}, t.list)
	mcp.AddTool(s, &mcp.Tool{Name: "ensure_server", Description: "Start a server (and its dependencies) if it is not running, wait until it accepts connections, and return its URL and whether it was reused or started. Safe to call repeatedly."}, t.ensure)
	mcp.AddTool(s, &mcp.Tool{Name: "restart_server", Description: "Restart a server and wait until it is ready (default 90s). Use after a change that hot reload cannot pick up, or to recover from a crash after reading its logs."}, t.restart)
	mcp.AddTool(s, &mcp.Tool{Name: "stop_server", Description: "Stop a server gracefully."}, t.stop)
	mcp.AddTool(s, &mcp.Tool{Name: "server_logs", Description: "Return the last lines of a server's terminal output as plain text, optionally only lines matching a pattern.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true}}, t.logs)
	mcp.AddTool(s, &mcp.Tool{Name: "list_containers", Description: "List every container on the active Docker engine with its state, health, published ports and the server it belongs to.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true}}, t.containers)
	mcp.AddTool(s, &mcp.Tool{Name: "container_logs", Description: "Return the last lines of a container's output as plain text, optionally only lines matching a pattern.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true}}, t.containerLogs)
	mcp.AddTool(s, &mcp.Tool{Name: "list_ports", Description: "List every process listening on a TCP port, with its working directory and whether " + app.Name + " manages it.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true}}, t.ports)

	return s.Run(ctx, &mcp.StdioTransport{})
}

type tools struct{ c *client.Client }

func (t *tools) ready(ctx context.Context) error {
	_, err := t.c.Ensure(ctx)
	return err
}

// target is the computer a tool call acts on: this one, or the connected
// computer named by its computer argument, reached through this daemon.
type target struct {
	c  *client.Client
	cn *client.Connection // nil for this computer
}

// on makes sure the daemon runs and resolves the computer argument.
func (t *tools) on(ctx context.Context, computer string) (target, error) {
	if err := t.ready(ctx); err != nil {
		return target{}, err
	}
	if strings.TrimSpace(computer) == "" {
		return target{c: t.c}, nil
	}
	c, cn, err := t.c.On(ctx, computer)
	if err != nil {
		return target{}, err
	}
	return target{c: c, cn: cn}, nil
}

// computer names the computer for results, "" for this one.
func (g target) computer() string {
	if g.cn == nil {
		return ""
	}
	return g.cn.Peer.Name
}

// server is the compact view of v, with URLs that open from this computer.
func (g target) server(v supervisor.View) Server {
	s := compact(v)
	if g.cn != nil {
		s.Computer = g.cn.Peer.Name
		if s.URL != "" {
			s.URL = g.cn.LocalURL(v.Name, s.URL)
		}
		if s.Alias != "" {
			s.Alias = g.cn.LocalURL(v.Name, s.Alias)
		}
	}
	return s
}

// Server is the compact view returned to agents.
type Server struct {
	RestartPending bool    `json:"restartPending"`
	ListenPorts    []int   `json:"listenPorts,omitempty"`
	Listening      bool    `json:"listening"`
	Name           string  `json:"name"`
	Group          string  `json:"group,omitempty"`
	Status         string  `json:"status" jsonschema:"stopped, starting, running, stopping, crashed, exited or blocked"`
	URL            string  `json:"url,omitempty" jsonschema:"direct URL on loopback"`
	Alias          string  `json:"alias,omitempty" jsonschema:"same server through http://<name>.localhost"`
	Port           int     `json:"port,omitempty"`
	Dir            string  `json:"dir"`
	Cmd            string  `json:"cmd"`
	Note           string  `json:"note,omitempty"`
	External       string  `json:"external,omitempty" jsonschema:"set when another process holds the port"`
	CPU            float64 `json:"cpuPercent,omitempty"`
	MemMB          int64   `json:"memMB,omitempty"`

	Containers []Container `json:"containers,omitempty" jsonschema:"containers of the compose project in the server's directory"`
	Outcome    string      `json:"outcome,omitempty" jsonschema:"what ensure_server did: reused (already running) or started"`
	Computer   string      `json:"computer,omitempty" jsonschema:"the other computer the server runs on; its URLs open from this one"`
}

// Container is the compact view of a container returned to agents.
type Container struct {
	Name     string   `json:"name"`
	Service  string   `json:"service,omitempty" jsonschema:"compose service"`
	Project  string   `json:"project,omitempty" jsonschema:"compose project"`
	Server   string   `json:"server,omitempty" jsonschema:"the server it belongs to"`
	Shared   []string `json:"sharedWith,omitempty" jsonschema:"other servers in the same directory that use it too"`
	IssueFor []string `json:"issueFor,omitempty" jsonschema:"servers it needs attention for: unhealthy, restarting, or failed while the server ran"`
	Image    string   `json:"image"`
	State    string   `json:"state" jsonschema:"created, running, paused, restarting, removing, exited or dead"`
	Status   string   `json:"status" jsonschema:"the engine's summary, e.g. Up 3 hours (healthy)"`
	Health   string   `json:"health,omitempty" jsonschema:"starting, healthy or unhealthy"`
	Failed   bool     `json:"failed,omitempty" jsonschema:"exited in failure (not finished or stopped on request), or dead"`
	ExitCode *int     `json:"exitCode,omitempty"`
	Restarts int      `json:"restarts,omitempty"`
	Ports    []string `json:"ports,omitempty" jsonschema:"published ports as host:container/protocol"`
	Dir      string   `json:"projectDir,omitempty" jsonschema:"compose project directory, where docker compose commands for it run"`
}

func brief(c docker.Container) Container {
	out := Container{Name: c.Name, Service: c.Service, Project: c.Project, Server: c.Server, Shared: c.SharedWith, IssueFor: c.IssueFor, Image: c.Image,
		State: c.State, Status: c.Status, Health: c.Health, Failed: c.Failed, Restarts: c.Restarts, Dir: c.WorkingDir}
	if c.State == "exited" || c.State == "dead" {
		out.ExitCode = c.ExitCode
	}
	for _, p := range c.Ports {
		out.Ports = append(out.Ports, fmt.Sprintf("%d:%d/%s", p.Public, p.Private, p.Proto))
	}
	return out
}

// engine describes the container engine in one line.
func engine(d docker.State) string {
	switch {
	case d.Status == docker.Ready && d.Context != "":
		return strings.TrimSpace("ready: "+d.Engine+" "+d.Version) + " (context " + d.Context + ")"
	case d.Status == docker.Ready:
		return strings.TrimSpace("ready: " + d.Engine + " " + d.Version)
	case d.Starting:
		return "starting: " + d.Engine
	case d.Error != "":
		return d.Status + ": " + d.Error
	}
	return d.Status
}

func (g target) state(ctx context.Context) (server.State, error) {
	var st server.State
	err := g.c.Do(ctx, http.MethodGet, "/api/state", nil, &st)
	return st, err
}

// withContainers adds the server's containers from the current state.
func (g target) withContainers(ctx context.Context, s Server) Server {
	if st, err := g.state(ctx); err == nil {
		for _, c := range st.Docker.Containers {
			if slices.Contains(c.Servers(), s.Name) {
				s.Containers = append(s.Containers, brief(c))
			}
		}
	}
	return s
}

func compact(v supervisor.View) Server {
	s := Server{Name: v.Name, Group: v.Group, Status: string(v.Status), URL: v.URL, Alias: v.Alias, Port: v.Port,
		Dir: v.Dir, Cmd: v.Cmd, Note: v.Note, RestartPending: v.RestartPending, ListenPorts: v.ListenPorts, Listening: v.Listening, CPU: v.CPU, MemMB: v.Mem >> 20}
	if v.External != nil && v.PID == 0 {
		s.External = fmt.Sprintf("%s (pid %d) in %s", v.External.Command, v.External.PID, v.External.Cwd)
	} else if v.ForwardedFrom != "" {
		s.Note = strings.TrimSpace(s.Note + " port " + strconv.Itoa(v.Port) + " is forwarded from " + v.ForwardedFrom + "; starting this server takes it back")
	}
	if v.Stale {
		s.Note = strings.TrimSpace(s.Note + " config changed since start; restart to apply")
	}
	return s
}

type empty struct{}

type listOut struct {
	Servers   []Server `json:"servers"`
	Dashboard string   `json:"dashboard"`
	Docker    string   `json:"docker" jsonschema:"container engine: ready, connecting or unavailable, with the reason"`
	Computer  string   `json:"computer,omitempty" jsonschema:"the other computer these servers run on"`
}

type listIn struct {
	Dir      string `json:"dir,omitempty" jsonschema:"only servers for this checkout: its own directory, one inside it, or one containing it. Pass the task's working directory to tell a worktree from the main checkout"`
	Computer string `json:"computer,omitempty" jsonschema:"another computer this one is connected to, by name, to list its servers instead"`
}

// related reports whether a server in dir belongs to the checkout at want.
func related(dir, want string) bool {
	inside := func(a, b string) bool { return a == b || strings.HasPrefix(a, b+string(filepath.Separator)) }
	return inside(dir, want) || inside(want, dir)
}

func canonical(p string) string {
	p = paths.ExpandHome(p)
	if abs, err := filepath.Abs(p); err == nil {
		p = abs
	}
	// Resolve symlinks in the deepest part that exists, so a path not yet
	// created still compares with the directories it lies under.
	rest := ""
	for dir := p; ; dir = filepath.Dir(dir) {
		if real, err := filepath.EvalSymlinks(dir); err == nil {
			return filepath.Join(real, rest)
		}
		if dir == filepath.Dir(dir) {
			return p
		}
		rest = filepath.Join(filepath.Base(dir), rest)
	}
}

func (t *tools) list(ctx context.Context, _ *mcp.CallToolRequest, in listIn) (*mcp.CallToolResult, listOut, error) {
	g, err := t.on(ctx, in.Computer)
	if err != nil {
		return nil, listOut{}, err
	}
	st, err := g.state(ctx)
	if err != nil {
		return nil, listOut{}, err
	}
	out := listOut{Dashboard: t.c.Base, Servers: []Server{}, Docker: engine(st.Docker), Computer: g.computer()}
	byServer := map[string][]Container{}
	for _, c := range st.Docker.Containers {
		for _, name := range c.Servers() {
			byServer[name] = append(byServer[name], brief(c))
		}
	}
	// Directories on another computer cannot be resolved here.
	resolve := canonical
	if g.cn != nil {
		resolve = filepath.Clean
	}
	want := ""
	if in.Dir != "" {
		want = resolve(in.Dir)
	}
	for _, v := range st.Servers {
		if want != "" && !related(resolve(v.Dir), want) {
			continue
		}
		s := g.server(v)
		s.Containers = byServer[v.Name]
		out.Servers = append(out.Servers, s)
	}
	return nil, out, nil
}

type nameIn struct {
	Name     string `json:"name" jsonschema:"server name as shown by list_servers"`
	Computer string `json:"computer,omitempty" jsonschema:"another computer this one is connected to, by name, when the server runs there"`
}

type ensureIn struct {
	Name     string `json:"name" jsonschema:"server name as shown by list_servers"`
	Timeout  string `json:"timeout,omitempty" jsonschema:"how long to wait for readiness, e.g. 90s (default)"`
	Computer string `json:"computer,omitempty" jsonschema:"another computer this one is connected to, by name, when the server runs there"`
}

func (t *tools) ensure(ctx context.Context, _ *mcp.CallToolRequest, in ensureIn) (*mcp.CallToolResult, Server, error) {
	g, err := t.on(ctx, in.Computer)
	if err != nil {
		return nil, Server{}, err
	}
	timeout := in.Timeout
	if timeout == "" {
		timeout = "90s"
	}
	outcome := "started"
	if st, err := g.state(ctx); err == nil {
		for _, v := range st.Servers {
			if v.Name == in.Name && v.Status == supervisor.Running {
				outcome = "reused"
			}
		}
	}
	var v supervisor.View
	if err := g.c.Do(ctx, http.MethodPost, "/api/servers/"+url.PathEscape(in.Name)+"/ensure", map[string]string{"timeout": timeout}, &v); err != nil {
		return nil, Server{}, fmt.Errorf("%w (read server_logs for details)", err)
	}
	s := g.withContainers(ctx, g.server(v))
	s.Outcome = outcome
	return nil, s, nil
}

func (t *tools) action(ctx context.Context, in nameIn, action string) (Server, error) {
	g, err := t.on(ctx, in.Computer)
	if err != nil {
		return Server{}, err
	}
	var v supervisor.View
	if err := g.c.Do(ctx, http.MethodPost, "/api/servers/"+url.PathEscape(in.Name)+"/"+action, nil, &v); err != nil {
		return Server{}, err
	}
	return g.withContainers(ctx, g.server(v)), nil
}

func (t *tools) restart(ctx context.Context, _ *mcp.CallToolRequest, in ensureIn) (*mcp.CallToolResult, Server, error) {
	g, err := t.on(ctx, in.Computer)
	if err != nil {
		return nil, Server{}, err
	}
	timeout := in.Timeout
	if timeout == "" {
		timeout = "90s"
	}
	var v supervisor.View
	if err := g.c.Do(ctx, http.MethodPost, "/api/servers/"+url.PathEscape(in.Name)+"/restart", map[string]string{"timeout": timeout}, &v); err != nil {
		return nil, Server{}, fmt.Errorf("%w (read server_logs for details)", err)
	}
	s := g.withContainers(ctx, g.server(v))
	s.Outcome = "restarted"
	return nil, s, nil
}

func (t *tools) stop(ctx context.Context, _ *mcp.CallToolRequest, in nameIn) (*mcp.CallToolResult, Server, error) {
	s, err := t.action(ctx, in, "stop")
	return nil, s, err
}

type logsIn struct {
	Name     string `json:"name" jsonschema:"server name as shown by list_servers"`
	Lines    int    `json:"lines,omitempty" jsonschema:"number of trailing lines (default 150, max 2000)"`
	Match    string `json:"match,omitempty" jsonschema:"case-insensitive regular expression; returns only matching lines, searched across the whole output"`
	Context  int    `json:"context,omitempty" jsonschema:"with match, lines to include before and after each match (max 20), e.g. for stack traces"`
	Since    string `json:"since,omitempty" jsonschema:"only lines since then: a duration such as 10m, or an RFC 3339 time"`
	Computer string `json:"computer,omitempty" jsonschema:"another computer this one is connected to, by name, when the server runs there"`
}

type containerLogsIn struct {
	Name     string `json:"name" jsonschema:"container name as shown by list_containers or list_servers"`
	Lines    int    `json:"lines,omitempty" jsonschema:"number of trailing lines (default 150, max 2000)"`
	Match    string `json:"match,omitempty" jsonschema:"case-insensitive regular expression; returns only matching lines among the last 20000"`
	Context  int    `json:"context,omitempty" jsonschema:"with match, lines to include before and after each match (max 20)"`
	Since    string `json:"since,omitempty" jsonschema:"only lines since then: a duration such as 10m, or an RFC 3339 time"`
	Computer string `json:"computer,omitempty" jsonschema:"another computer this one is connected to, by name, when the container runs there"`
}

func (t *tools) logs(ctx context.Context, _ *mcp.CallToolRequest, in logsIn) (*mcp.CallToolResult, any, error) {
	return t.text(ctx, in.Computer, "/api/servers/"+url.PathEscape(in.Name)+"/logs", in.Lines, in.Match, in.Context, in.Since)
}

func (t *tools) containerLogs(ctx context.Context, _ *mcp.CallToolRequest, in containerLogsIn) (*mcp.CallToolResult, any, error) {
	return t.text(ctx, in.Computer, "/api/containers/"+url.PathEscape(in.Name)+"/logs", in.Lines, in.Match, in.Context, in.Since)
}

// text fetches plain log lines from path, each with its local time, on
// computer ("" for this one).
func (t *tools) text(ctx context.Context, computer, path string, lines int, match string, around int, since string) (*mcp.CallToolResult, any, error) {
	g, err := t.on(ctx, computer)
	if err != nil {
		return nil, nil, err
	}
	n := lines
	if n <= 0 {
		n = 150
	}
	if n > 2000 {
		n = 2000
	}
	q := url.Values{"lines": {strconv.Itoa(n)}, "timestamps": {"1"}}
	if since != "" {
		q.Set("since", since)
	}
	if match != "" {
		q.Set("match", match)
		if around > 0 {
			q.Set("context", strconv.Itoa(around))
		}
	}
	var b strings.Builder
	if g.cn != nil {
		b.WriteString("[on " + g.computer() + "]\n")
	}
	if err := g.c.Do(ctx, http.MethodGet, path+"?"+q.Encode(), nil, &b); err != nil {
		return nil, nil, err
	}
	text := b.String()
	if onlyNotes(text) {
		switch {
		case match != "":
			text += "(no matching lines)"
		case since != "":
			text += "(no output in that time)"
		default:
			text += "(no output yet)"
		}
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}}, nil, nil
}

// onlyNotes reports whether a logs reply holds nothing but its bracketed
// notes.
func onlyNotes(text string) bool {
	for _, l := range strings.Split(text, "\n") {
		if l != "" && !(strings.HasPrefix(l, "[") && strings.HasSuffix(l, "]")) {
			return false
		}
	}
	return true
}

type containersOut struct {
	Containers []Container `json:"containers"`
	Docker     string      `json:"docker" jsonschema:"container engine: ready, connecting or unavailable, with the reason"`
	Computer   string      `json:"computer,omitempty" jsonschema:"the other computer these containers run on"`
}

type containersIn struct {
	Server   string `json:"server,omitempty" jsonschema:"only the containers of this server"`
	Computer string `json:"computer,omitempty" jsonschema:"another computer this one is connected to, by name, to list its containers instead"`
}

func (t *tools) containers(ctx context.Context, _ *mcp.CallToolRequest, in containersIn) (*mcp.CallToolResult, containersOut, error) {
	g, err := t.on(ctx, in.Computer)
	if err != nil {
		return nil, containersOut{}, err
	}
	st, err := g.state(ctx)
	if err != nil {
		return nil, containersOut{}, err
	}
	out := containersOut{Containers: []Container{}, Docker: engine(st.Docker), Computer: g.computer()}
	for _, c := range st.Docker.Containers {
		if in.Server == "" || slices.Contains(c.Servers(), in.Server) {
			out.Containers = append(out.Containers, brief(c))
		}
	}
	return nil, out, nil
}

type portsOut struct {
	Listeners []server.ListenerView `json:"listeners"`
}

func (t *tools) ports(ctx context.Context, _ *mcp.CallToolRequest, _ empty) (*mcp.CallToolResult, portsOut, error) {
	if err := t.ready(ctx); err != nil {
		return nil, portsOut{}, err
	}
	var out portsOut
	if err := t.c.Do(ctx, http.MethodGet, "/api/listeners", nil, &out); err != nil {
		return nil, portsOut{}, err
	}
	if out.Listeners == nil {
		out.Listeners = []server.ListenerView{}
	}
	return nil, out, nil
}
