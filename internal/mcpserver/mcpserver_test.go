package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/padmk/portside/internal/app"
	"github.com/padmk/portside/internal/client"
	"github.com/padmk/portside/internal/docker"
	"github.com/padmk/portside/internal/paths"
	"github.com/padmk/portside/internal/ports"
	"github.com/padmk/portside/internal/server"
	"github.com/padmk/portside/internal/signing"
	"github.com/padmk/portside/internal/supervisor"
)

// TestMain keeps this test binary from acting as a daemon: a client that
// wrongly starts one runs `<executable> daemon`, which is this binary under
// test. It then exits at once, and the test that caused it sees the files
// the start left behind.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "daemon" {
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// isolate points the config, state directory, home and daemon port at
// temporary values, so nothing reaches the account's real daemon.
func isolate(t *testing.T) (state string, port int) {
	t.Helper()
	dir := t.TempDir()
	state = filepath.Join(dir, "state")
	if err := os.MkdirAll(state, 0o700); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port = ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	t.Setenv("HOME", dir)
	t.Setenv(app.Env("CONFIG"), filepath.Join(dir, app.ID+".toml"))
	t.Setenv(app.Env("STATE_DIR"), state)
	t.Setenv(app.Env("PORT"), strconv.Itoa(port))
	return state, port
}

// connect runs the MCP server for opts against the daemon on port and
// returns a client session connected to it in memory.
func connect(t *testing.T, port int, opts Options) *mcp.ClientSession {
	t.Helper()
	s, err := newServer(client.New(port), opts)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	st, ct := mcp.NewInMemoryTransports()
	ss, err := s.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cs.Close()
		ss.Wait()
		cancel()
	})
	return cs
}

func toolNames(t *testing.T, cs *mcp.ClientSession) map[string]*mcp.Tool {
	t.Helper()
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]*mcp.Tool{}
	for _, tool := range res.Tools {
		out[tool.Name] = tool
	}
	return out
}

func TestModeTools(t *testing.T) {
	_, port := isolate(t)
	reading := []string{"list_servers", "server_logs", "list_containers", "container_logs", "list_ports"}
	tests := []struct {
		mode Mode
		want []string
	}{
		{ReadOnly, reading},
		{Start, append(slices.Clone(reading), "ensure_server")},
		{Full, append(slices.Clone(reading), "ensure_server", "restart_server", "stop_server", "container_action")},
		{"", append(slices.Clone(reading), "ensure_server", "restart_server", "stop_server", "container_action")},
	}
	for _, tt := range tests {
		t.Run(string(tt.mode), func(t *testing.T) {
			tools := toolNames(t, connect(t, port, Options{Mode: tt.mode}))
			var got []string
			for name := range tools {
				got = append(got, name)
			}
			slices.Sort(got)
			want := slices.Sorted(slices.Values(tt.want))
			if !slices.Equal(got, want) {
				t.Fatalf("tools = %v, want %v", got, want)
			}
			for _, name := range reading {
				if a := tools[name].Annotations; a == nil || !a.ReadOnlyHint {
					t.Errorf("%s: annotations %+v, want read-only", name, a)
				}
			}
			for _, name := range []string{"restart_server", "stop_server", "container_action"} {
				if tool, ok := tools[name]; ok {
					if a := tool.Annotations; a == nil || a.ReadOnlyHint || a.DestructiveHint == nil || !*a.DestructiveHint {
						t.Errorf("%s: annotations %+v, want destructive", name, a)
					}
				}
			}
			if tool, ok := tools["ensure_server"]; ok {
				if a := tool.Annotations; a == nil || a.ReadOnlyHint || a.DestructiveHint == nil || *a.DestructiveHint || !a.IdempotentHint {
					t.Errorf("ensure_server: annotations %+v, want non-destructive and idempotent", a)
				}
			}
		})
	}
}

// call runs a tool and returns its text and whether it failed.
func call(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) (string, bool) {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String(), res.IsError
}

func TestReadOnlyNeverStartsDaemon(t *testing.T) {
	state, port := isolate(t)
	cs := connect(t, port, Options{Mode: ReadOnly})
	text, failed := call(t, cs, "list_servers", map[string]any{})
	// A regression that started a daemon could leave one listening; stop it
	// through its own state before failing.
	defer func() {
		if c, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), time.Second); err == nil {
			c.Close()
			_ = client.New(port).Do(context.Background(), http.MethodPost, "/api/shutdown", nil, nil)
			t.Errorf("a daemon listens on port %d after a read-only call", port)
		}
	}()
	if !failed || !strings.Contains(text, "is not running") {
		t.Fatalf("list_servers = %q (error %v), want the not-running error", text, failed)
	}
	entries, err := os.ReadDir(state)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		t.Errorf("state directory holds %s after a read-only call", e.Name())
	}
}

// fakeDaemon proves itself with the token in the isolated state directory,
// answers the routes the tools use from a fixed state, and records the
// paths it is asked for.
type fakeDaemon struct {
	mu        sync.Mutex
	state     server.State
	listeners []server.ListenerView
	asked     []string
}

func (f *fakeDaemon) paths() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.asked)
}

func startFake(t *testing.T, state string, f *fakeDaemon) int {
	t.Helper()
	token := signing.RandomHex(32)
	if err := os.WriteFile(filepath.Join(state, paths.TokenName), []byte(token+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	epoch := signing.NewEpoch()
	var v signing.Verifier
	reply := func(w http.ResponseWriter, r *http.Request, status int, body []byte) {
		signing.ProveReply(w.Header(), r.Header, token, epoch, status, body)
		w.WriteHeader(status)
		_, _ = w.Write(body)
	}
	replyJSON := func(w http.ResponseWriter, r *http.Request, status int, x any) {
		b, _ := json.Marshal(x)
		reply(w, r, status, b)
	}
	views := func() map[string]supervisor.View {
		out := map[string]supervisor.View{}
		for _, sv := range f.state.Servers {
			out[sv.Name] = sv
		}
		return out
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/health" {
			replyJSON(w, r, http.StatusOK, client.Health{App: app.ID, Version: "test", PID: 1})
			return
		}
		body, _ := io.ReadAll(r.Body)
		if v.Verify(r, token, r.RequestURI, body) != nil || signing.CheckEpoch(r.Header, epoch) != nil {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		f.mu.Lock()
		f.asked = append(f.asked, r.Method+" "+r.URL.Path)
		f.mu.Unlock()
		parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/"), "/")
		switch {
		case r.URL.Path == "/api/state":
			replyJSON(w, r, http.StatusOK, f.state)
		case r.URL.Path == "/api/listeners":
			replyJSON(w, r, http.StatusOK, map[string]any{"listeners": f.listeners})
		case len(parts) == 3 && parts[0] == "servers":
			sv, ok := views()[parts[1]]
			if !ok {
				replyJSON(w, r, http.StatusNotFound, map[string]string{"error": fmt.Sprintf("unknown server %q", parts[1])})
			} else if parts[2] == "logs" {
				reply(w, r, http.StatusOK, []byte("log of "+sv.Name+"\n"))
			} else {
				replyJSON(w, r, http.StatusOK, sv)
			}
		case len(parts) == 3 && parts[0] == "containers" && parts[2] == "logs":
			reply(w, r, http.StatusOK, []byte("log of "+parts[1]+"\n"))
		default:
			replyJSON(w, r, http.StatusNotFound, map[string]string{"error": "not found"})
		}
	}))
	t.Cleanup(srv.Close)
	return srv.Listener.Addr().(*net.TCPAddr).Port
}

// scoped is a daemon with servers in two projects: web (in scope, reported
// at its resolved path), its temporary copy in a worktree elsewhere, api in
// another project and api's temporary copy.
func scoped(t *testing.T) (f *fakeDaemon, port int, scopeDir string) {
	t.Helper()
	state, _ := isolate(t)
	base := t.TempDir()
	web, api := filepath.Join(base, "web"), filepath.Join(base, "api")
	for _, d := range []string{web, api, filepath.Join(web, "src")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	realWeb, err := filepath.EvalSymlinks(web)
	if err != nil {
		t.Fatal(err)
	}
	// The agent names the project through a symlink, as /tmp names
	// /private/tmp on macOS; the daemon reports resolved directories.
	scopeDir = filepath.Join(base, "link")
	if err := os.Symlink(web, scopeDir); err != nil {
		t.Fatal(err)
	}
	f = &fakeDaemon{
		state: server.State{
			Servers: []supervisor.View{
				{Name: "web", Dir: realWeb},
				{Name: "web-wt", Dir: filepath.Join(base, "worktrees", "web-feature"), Temporary: true, Origin: "web"},
				{Name: "api", Dir: api},
				{Name: "api-wt", Dir: filepath.Join(base, "worktrees", "api-feature"), Temporary: true, Origin: "api"},
			},
			Docker: docker.State{Status: docker.Ready, Containers: []docker.Container{
				{ID: "c1", Name: "web-db", Server: "web", SharedWith: []string{"api"}, IssueFor: []string{"web", "api"}, State: "running"},
				{ID: "c2", Name: "api-cache", Server: "api", State: "running"},
			}},
		},
		listeners: []server.ListenerView{
			{Listener: ports.Listener{Port: 3000, PID: 10, Command: "node"}, Server: "web"},
			{Listener: ports.Listener{Port: 4000, PID: 11, Command: "node"}, Server: "api"},
			{Listener: ports.Listener{Port: 5000, PID: 12, Command: "vite", Cwd: filepath.Join(realWeb, "src")}},
			{Listener: ports.Listener{Port: 6000, PID: 13, Command: "vite", Cwd: api}},
		},
	}
	return f, startFake(t, state, f), scopeDir
}

// structured decodes a tool's structured result.
func structured[T any](t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) T {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil || res.IsError {
		t.Fatalf("%s: %v %+v", name, err, res)
	}
	var out T
	b, _ := json.Marshal(res.StructuredContent)
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestScopeServers(t *testing.T) {
	f, port, dir := scoped(t)
	cs := connect(t, port, Options{Mode: Full, Dirs: []string{dir}})

	list := structured[listOut](t, cs, "list_servers", map[string]any{})
	var names []string
	for _, s := range list.Servers {
		names = append(names, s.Name)
		if s.Name == "web" {
			if len(s.Containers) != 1 || s.Containers[0].Name != "web-db" || len(s.Containers[0].Shared) != 0 || !slices.Equal(s.Containers[0].IssueFor, []string{"web"}) {
				t.Errorf("web containers = %+v, want web-db without api", s.Containers)
			}
		}
	}
	if !slices.Equal(names, []string{"web", "web-wt"}) {
		t.Errorf("list_servers = %v, want web and its worktree copy", names)
	}

	for _, name := range []string{"web", "web-wt"} {
		if text, failed := call(t, cs, "server_logs", map[string]any{"name": name}); failed || !strings.Contains(text, "log of "+name) {
			t.Errorf("server_logs %s = %q (error %v)", name, text, failed)
		}
	}
	for _, tool := range []string{"server_logs", "ensure_server", "stop_server", "restart_server"} {
		unknown, _ := call(t, cs, tool, map[string]any{"name": "nope"})
		for _, name := range []string{"api", "api-wt"} {
			text, failed := call(t, cs, tool, map[string]any{"name": name})
			want := strings.ReplaceAll(unknown, `"nope"`, strconv.Quote(name))
			if !failed || text != want || !strings.Contains(text, "unknown server") {
				t.Errorf("%s %s = %q (error %v), want %q", tool, name, text, failed, want)
			}
		}
	}
	for _, p := range f.paths() {
		if p != "GET /api/state" && !strings.HasPrefix(p, "GET /api/servers/web") {
			t.Errorf("the daemon was asked for %s", p)
		}
	}

	if text, failed := call(t, cs, "list_servers", map[string]any{"computer": "laptop"}); !failed || !strings.Contains(text, "computer is not available") {
		t.Errorf("list_servers on another computer = %q (error %v), want a refusal", text, failed)
	}
}

func TestScopeContainersAndPorts(t *testing.T) {
	f, port, dir := scoped(t)
	cs := connect(t, port, Options{Mode: Full, Dirs: []string{dir}})

	out := structured[containersOut](t, cs, "list_containers", map[string]any{})
	if len(out.Containers) != 1 || out.Containers[0].Name != "web-db" || len(out.Containers[0].Shared) != 0 {
		t.Errorf("list_containers = %+v, want only web-db without api", out.Containers)
	}
	if text, failed := call(t, cs, "container_logs", map[string]any{"name": "web-db"}); failed || !strings.Contains(text, "log of web-db") {
		t.Errorf("container_logs web-db = %q (error %v)", text, failed)
	}
	for _, ref := range []string{"api-cache", "c2"} {
		for _, tool := range []string{"container_logs", "container_action"} {
			args := map[string]any{"name": ref}
			if tool == "container_action" {
				args["action"] = "restart"
			}
			text, failed := call(t, cs, tool, args)
			if want := fmt.Sprintf("no container %q", ref); !failed || text != want {
				t.Errorf("%s %s = %q (error %v), want %q", tool, ref, text, failed, want)
			}
		}
	}
	for _, p := range f.paths() {
		if strings.HasPrefix(p, "POST") || strings.Contains(p, "api-cache") || strings.Contains(p, "c2") {
			t.Errorf("the daemon was asked for %s", p)
		}
	}

	lp := structured[portsOut](t, cs, "list_ports", map[string]any{})
	var got []int
	for _, l := range lp.Listeners {
		got = append(got, l.Port)
	}
	if !slices.Equal(got, []int{3000, 5000}) {
		t.Errorf("list_ports = %v, want web's port and the listener in its directory", got)
	}

	if text, failed := call(t, cs, "container_logs", map[string]any{"name": "web-db", "computer": "laptop"}); !failed || !strings.Contains(text, "computer is not available") {
		t.Errorf("container_logs on another computer = %q (error %v), want a refusal", text, failed)
	}
}

func TestScopeNeedsDirectory(t *testing.T) {
	_, port := isolate(t)
	if _, err := newServer(client.New(port), Options{Dirs: []string{filepath.Join(t.TempDir(), "missing")}}); err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Errorf("missing --dir: %v", err)
	}
	if _, err := newServer(client.New(port), Options{Dirs: []string{" "}}); err == nil {
		t.Error("empty --dir accepted")
	}
}

// TestScopeDependencies checks that a scoped session never makes the daemon
// start a server outside the scope through depends_on: a dependency outside
// it that does not run refuses the start before any request reaches the
// daemon.
func TestScopeDependencies(t *testing.T) {
	const refusal = "web needs shared-db, which is outside this scope and not running; ask the user to start it"
	tests := []struct {
		name     string
		unscoped bool
		dbStatus supervisor.Status
		web      []string // web's dependencies
		tool     string
		server   string
		refusal  string // "" when the request must reach the daemon
	}{
		{name: "stopped outside", dbStatus: supervisor.Stopped, web: []string{"shared-db"}, tool: "ensure_server", server: "web", refusal: refusal},
		{name: "running outside", dbStatus: supervisor.Running, web: []string{"shared-db"}, tool: "ensure_server", server: "web"},
		{name: "starting outside", dbStatus: supervisor.Starting, web: []string{"shared-db"}, tool: "ensure_server", server: "web"},
		{name: "through a dependency in scope", dbStatus: supervisor.Crashed, web: []string{"lib"}, tool: "ensure_server", server: "web",
			refusal: "web needs shared-db (through lib), which is outside this scope and not running; ask the user to start it"},
		{name: "worktree copy", dbStatus: supervisor.Stopped, web: []string{"shared-db"}, tool: "ensure_server", server: "web-wt",
			refusal: strings.Replace(refusal, "web", "web-wt", 1)},
		{name: "restart", dbStatus: supervisor.Stopped, web: []string{"shared-db"}, tool: "restart_server", server: "web", refusal: refusal},
		{name: "unscoped", unscoped: true, dbStatus: supervisor.Stopped, web: []string{"lib"}, tool: "ensure_server", server: "web"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, port, dir := scoped(t)
			sv := f.state.Servers
			sv[0].DependsOn = tt.web // web
			sv[1].DependsOn = tt.web // web-wt carries its origin's dependencies
			f.state.Servers = append(sv,
				supervisor.View{Name: "lib", Dir: filepath.Join(sv[0].Dir, "lib"), Status: supervisor.Stopped, DependsOn: []string{"shared-db"}},
				supervisor.View{Name: "shared-db", Dir: filepath.Join(filepath.Dir(sv[2].Dir), "db"), Status: tt.dbStatus})
			opts := Options{Mode: Full, Dirs: []string{dir}}
			if tt.unscoped {
				opts.Dirs = nil
			}
			cs := connect(t, port, opts)
			text, failed := call(t, cs, tt.tool, map[string]any{"name": tt.server})
			action := "ensure"
			if tt.tool == "restart_server" {
				action = "restart"
			}
			asked := slices.Contains(f.paths(), "POST /api/servers/"+tt.server+"/"+action)
			if tt.refusal != "" {
				if !failed || text != tt.refusal {
					t.Errorf("%s %s = %q (error %v), want %q", tt.tool, tt.server, text, failed, tt.refusal)
				}
				if asked {
					t.Errorf("the daemon received the %s request: %v", action, f.paths())
				}
				return
			}
			if failed || !asked {
				t.Errorf("%s %s = %q (error %v), asked %v; want the request to reach the daemon", tt.tool, tt.server, text, failed, f.paths())
			}
		})
	}
}
