// Package mcpserver exposes the daemon to coding agents over the Model
// Context Protocol (stdio). Agents can reuse running servers instead of
// launching duplicates, start what they need, and read logs.
package mcpserver

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/padmk/portside/internal/app"
	"github.com/padmk/portside/internal/client"
	"github.com/padmk/portside/internal/server"
	"github.com/padmk/portside/internal/supervisor"
)

const instructions = `This machine runs local dev servers under ` + app.Name + `.
Before starting a dev server yourself, call list_servers: reuse a server that is running,
or call ensure_server to start it and get its URL. Never launch a second copy of a managed
server in your own shell. Read server_logs before restarting a crashed server. Do not stop
or restart servers the user did not ask about.`

// Run serves MCP on stdin/stdout until the client disconnects.
func Run(ctx context.Context, c *client.Client) error {
	s := mcp.NewServer(&mcp.Implementation{Name: app.ID, Version: app.Version}, &mcp.ServerOptions{Instructions: instructions})
	t := &tools{c: c}

	mcp.AddTool(s, &mcp.Tool{Name: "list_servers", Description: "List managed dev servers with status, URL, port and resource usage.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true}}, t.list)
	mcp.AddTool(s, &mcp.Tool{Name: "ensure_server", Description: "Start a server (and its dependencies) if it is not running, wait until it accepts connections, and return its URL. Safe to call repeatedly."}, t.ensure)
	mcp.AddTool(s, &mcp.Tool{Name: "restart_server", Description: "Restart a server. Use after a change that hot reload cannot pick up, or to recover from a crash after reading its logs."}, t.restart)
	mcp.AddTool(s, &mcp.Tool{Name: "stop_server", Description: "Stop a server gracefully."}, t.stop)
	mcp.AddTool(s, &mcp.Tool{Name: "server_logs", Description: "Return the last lines of a server's terminal output as plain text.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true}}, t.logs)
	mcp.AddTool(s, &mcp.Tool{Name: "list_ports", Description: "List every process listening on a TCP port, with its working directory and whether " + app.Name + " manages it.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true}}, t.ports)

	return s.Run(ctx, &mcp.StdioTransport{})
}

type tools struct{ c *client.Client }

func (t *tools) ready(ctx context.Context) error {
	_, err := t.c.Ensure(ctx)
	return err
}

// Server is the compact view returned to agents.
type Server struct {
	Name     string  `json:"name"`
	Group    string  `json:"group,omitempty"`
	Status   string  `json:"status" jsonschema:"stopped, starting, running, stopping, crashed, exited or blocked"`
	URL      string  `json:"url,omitempty" jsonschema:"direct URL on 127.0.0.1"`
	Alias    string  `json:"alias,omitempty" jsonschema:"same server through http://<name>.localhost"`
	Port     int     `json:"port,omitempty"`
	Dir      string  `json:"dir"`
	Cmd      string  `json:"cmd"`
	Note     string  `json:"note,omitempty"`
	External string  `json:"external,omitempty" jsonschema:"set when another process holds the port"`
	CPU      float64 `json:"cpuPercent,omitempty"`
	MemMB    int64   `json:"memMB,omitempty"`
}

func compact(v supervisor.View) Server {
	s := Server{Name: v.Name, Group: v.Group, Status: string(v.Status), URL: v.URL, Alias: v.Alias, Port: v.Port,
		Dir: v.Dir, Cmd: v.Cmd, Note: v.Note, CPU: v.CPU, MemMB: v.Mem >> 20}
	if v.External != nil && v.PID == 0 {
		s.External = fmt.Sprintf("%s (pid %d) in %s", v.External.Command, v.External.PID, v.External.Cwd)
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
}

func (t *tools) list(ctx context.Context, _ *mcp.CallToolRequest, _ empty) (*mcp.CallToolResult, listOut, error) {
	if err := t.ready(ctx); err != nil {
		return nil, listOut{}, err
	}
	var st server.State
	if err := t.c.Do(ctx, http.MethodGet, "/api/state", nil, &st); err != nil {
		return nil, listOut{}, err
	}
	out := listOut{Dashboard: t.c.Base, Servers: []Server{}}
	for _, v := range st.Servers {
		out.Servers = append(out.Servers, compact(v))
	}
	return nil, out, nil
}

type nameIn struct {
	Name string `json:"name" jsonschema:"server name as shown by list_servers"`
}

type ensureIn struct {
	Name    string `json:"name" jsonschema:"server name as shown by list_servers"`
	Timeout string `json:"timeout,omitempty" jsonschema:"how long to wait for readiness, e.g. 90s (default)"`
}

func (t *tools) ensure(ctx context.Context, _ *mcp.CallToolRequest, in ensureIn) (*mcp.CallToolResult, Server, error) {
	if err := t.ready(ctx); err != nil {
		return nil, Server{}, err
	}
	timeout := in.Timeout
	if timeout == "" {
		timeout = "90s"
	}
	var v supervisor.View
	if err := t.c.Do(ctx, http.MethodPost, "/api/servers/"+url.PathEscape(in.Name)+"/ensure", map[string]string{"timeout": timeout}, &v); err != nil {
		return nil, Server{}, fmt.Errorf("%w (read server_logs for details)", err)
	}
	return nil, compact(v), nil
}

func (t *tools) action(ctx context.Context, name, action string) (Server, error) {
	if err := t.ready(ctx); err != nil {
		return Server{}, err
	}
	var v supervisor.View
	if err := t.c.Do(ctx, http.MethodPost, "/api/servers/"+url.PathEscape(name)+"/"+action, nil, &v); err != nil {
		return Server{}, err
	}
	return compact(v), nil
}

func (t *tools) restart(ctx context.Context, _ *mcp.CallToolRequest, in nameIn) (*mcp.CallToolResult, Server, error) {
	s, err := t.action(ctx, in.Name, "restart")
	return nil, s, err
}

func (t *tools) stop(ctx context.Context, _ *mcp.CallToolRequest, in nameIn) (*mcp.CallToolResult, Server, error) {
	s, err := t.action(ctx, in.Name, "stop")
	return nil, s, err
}

type logsIn struct {
	Name  string `json:"name" jsonschema:"server name as shown by list_servers"`
	Lines int    `json:"lines,omitempty" jsonschema:"number of trailing lines (default 150, max 2000)"`
}

func (t *tools) logs(ctx context.Context, _ *mcp.CallToolRequest, in logsIn) (*mcp.CallToolResult, any, error) {
	if err := t.ready(ctx); err != nil {
		return nil, nil, err
	}
	n := in.Lines
	if n <= 0 {
		n = 150
	}
	if n > 2000 {
		n = 2000
	}
	var b strings.Builder
	if err := t.c.Do(ctx, http.MethodGet, fmt.Sprintf("/api/servers/%s/logs?lines=%d", url.PathEscape(in.Name), n), nil, &b); err != nil {
		return nil, nil, err
	}
	text := b.String()
	if strings.TrimSpace(text) == "" {
		text = "(no output yet)"
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}}, nil, nil
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
