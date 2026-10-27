package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/coder/websocket"
	"github.com/padmk/portside/internal/config"

	"slices"
	"testing"
	"time"

	"github.com/padmk/portside/internal/docker"
	"github.com/padmk/portside/internal/supervisor"
)

func TestIssueFor(t *testing.T) {
	started := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	before, after := started.Add(-time.Minute), started.Add(time.Minute)
	views := []supervisor.View{
		{Name: "web", Status: supervisor.Running, StartedAt: &started},
		{Name: "docs", Status: supervisor.Stopped},
	}
	code := 1
	cases := []struct {
		name string
		c    docker.Container
		want []string
	}{
		{"healthy", docker.Container{Server: "web", State: "running", Health: "healthy"}, nil},
		{"unhealthy, for every server", docker.Container{Server: "web", SharedWith: []string{"docs"}, State: "running", Health: "unhealthy"}, []string{"web", "docs"}},
		{"restarting", docker.Container{Server: "docs", State: "restarting"}, []string{"docs"}},
		{"failed while the server ran", docker.Container{Server: "web", State: "exited", Failed: true, ExitCode: &code, FinishedAt: &after}, []string{"web"}},
		{"failed before the server started", docker.Container{Server: "web", State: "exited", Failed: true, ExitCode: &code, FinishedAt: &before}, nil},
		{"failed, server stopped", docker.Container{Server: "docs", State: "exited", Failed: true, ExitCode: &code, FinishedAt: &after}, nil},
		{"stopped on purpose", docker.Container{Server: "web", State: "exited", FinishedAt: &after}, nil},
		{"no server", docker.Container{State: "running", Health: "unhealthy"}, nil},
		{"unknown server", docker.Container{Server: "gone", State: "restarting"}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := issueFor(&c.c, views); !slices.Equal(got, c.want) {
				t.Errorf("issueFor = %v, want %v", got, c.want)
			}
		})
	}
}

func TestContainerOwners(t *testing.T) {
	servers := []config.Server{
		{Name: "root", Dir: "/work", Cmd: "pnpm dev"},
		{Name: "api", Dir: "/work/api", Cmd: "pnpm dev"},
		{Name: "db", Dir: "/work/api", Cmd: "docker compose up"},
		{Name: "explicit", Dir: "/elsewhere", Projects: []string{"special"}},
	}
	containers := []docker.Container{
		{Name: "nested", WorkingDir: "/work/api/infra"},
		{Name: "exact", WorkingDir: "/work/api"},
		{Name: "sibling", WorkingDir: "/work/site/infra"},
		{Name: "prefix", WorkingDir: "/worktree/api"},
		{Name: "override", WorkingDir: "/work/api", Project: "special"},
		{Name: "no-parent", WorkingDir: "/"},
	}
	attachContainers(containers, servers)
	wants := [][]string{{"db", "api"}, {"db", "api"}, {"root"}, nil, {"explicit"}, nil}
	for i, want := range wants {
		if got := containers[i].Servers(); !slices.Equal(got, want) {
			t.Errorf("%s owners = %v, want %v", containers[i].Name, got, want)
		}
	}
}

// A temporary server in a worktree nested in its origin's checkout owns the
// containers started from that worktree, also under a shared project name.
func TestContainerOwnersNestedWorktree(t *testing.T) {
	servers := []config.Server{
		{Name: "db", Dir: "/repo/app", Cmd: "docker compose up", Projects: []string{"shared"}},
		{Name: "db--wt", Dir: "/repo/.worktrees/wt/app", Cmd: "docker compose up", Projects: []string{"shared"}},
	}
	containers := []docker.Container{
		{Name: "worktree", WorkingDir: "/repo/.worktrees/wt/app"},
		{Name: "origin", WorkingDir: "/repo/app"},
		{Name: "worktree-project", WorkingDir: "/repo/.worktrees/wt/app", Project: "shared"},
		{Name: "origin-project", WorkingDir: "/repo/app", Project: "shared"},
	}
	attachContainers(containers, servers)
	for i, want := range []string{"db--wt", "db", "db--wt", "db"} {
		if containers[i].Server != want {
			t.Errorf("%s owner = %q, want %q", containers[i].Name, containers[i].Server, want)
		}
	}
}

func TestLocalProjectOwner(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	app := filepath.Join(repo, "apps", "web")
	for _, dir := range []string{app, filepath.Join(repo, ".git"), filepath.Join(repo, "supabase"), filepath.Join(root, "supabase")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for dir, id := range map[string]string{root: "outside", repo: "local-stack"} {
		if err := os.WriteFile(filepath.Join(dir, "supabase", "config.toml"), []byte("project_id = "+strconv.Quote(id)), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	containers := []docker.Container{{LocalProject: "local-stack"}, {LocalProject: "outside"}}
	attachContainers(containers, []config.Server{{Name: "web", Dir: app}})
	if containers[0].Server != "web" || containers[1].Server != "" {
		t.Fatalf("unexpected owners: %+v", containers)
	}
}

// A project file claiming another project's local CLI project ID shares
// that project's stack on the dashboard, but never gives the trusted
// ownership a scoped session follows; an ID named in a server's `projects`
// belongs to that server alone.
func TestLocalProjectClaims(t *testing.T) {
	root := t.TempDir()
	a, b := filepath.Join(root, "a"), filepath.Join(root, "b")
	for _, dir := range []string{a, b} {
		for _, sub := range []string{".git", "supabase"} {
			if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.WriteFile(filepath.Join(dir, "supabase", "config.toml"), []byte(`project_id = "b"`), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	all := func(string) bool { return true }
	onlyA := func(n string) bool { return n == "a" }

	containers := []docker.Container{{Name: "supabase_db_b", LocalProject: "b"}}
	attachContainers(containers, []config.Server{{Name: "a", Dir: a}, {Name: "b", Dir: b}})
	c := containers[0]
	if !slices.Equal(c.Servers(), []string{"a", "b"}) || c.OwnedBy != docker.OwnedByLocalProject {
		t.Fatalf("claimed by both project files: %v by %q", c.Servers(), c.OwnedBy)
	}
	if TrustedOwner(c, onlyA, all) {
		t.Error("a project file's ID gave trusted ownership")
	}

	attachContainers(containers, []config.Server{{Name: "a", Dir: a}, {Name: "b", Dir: b, Projects: []string{"b"}}})
	c = containers[0]
	if !slices.Equal(c.Servers(), []string{"b"}) || c.OwnedBy != docker.OwnedByProject || TrustedOwner(c, onlyA, all) || !TrustedOwner(c, func(n string) bool { return n == "b" }, all) {
		t.Errorf("configured project: %v by %q", c.Servers(), c.OwnedBy)
	}

	containers = []docker.Container{{Name: "a-db", WorkingDir: a}}
	attachContainers(containers, []config.Server{{Name: "a", Dir: a}})
	c = containers[0]
	if c.OwnedBy != docker.OwnedByDir || !TrustedOwner(c, onlyA, func(d string) bool { return d == a }) || TrustedOwner(c, onlyA, func(string) bool { return false }) {
		t.Errorf("working directory: %v by %q", c.Servers(), c.OwnedBy)
	}
}

type containerTestHost struct{ endpoint string }

func (h containerTestHost) Environ() []string    { return []string{"DOCKER_HOST=" + h.endpoint} }
func (containerTestHost) LookPath(string) string { return "" }

func TestContainerLogProtocol(t *testing.T) {
	for _, refused := range []bool{false, true} {
		t.Run(strconv.FormatBool(refused), func(t *testing.T) {
			tail := make(chan string, 4)
			sinceQuery := make(chan string, 4)
			engine := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/version":
					_, _ = io.WriteString(w, `{"Version":"test"}`)
				case "/events":
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(200)
					w.(http.Flusher).Flush()
					<-r.Context().Done()
				case "/containers/json":
					_, _ = io.WriteString(w, `[{"Id":"abc"}]`)
				case "/containers/abc/json":
					_, _ = io.WriteString(w, `{"Id":"abc","Name":"/demo","State":{"Status":"running","Running":true},"Config":{"Tty":true}}`)
				case "/containers/abc/stats":
					w.WriteHeader(200)
					w.(http.Flusher).Flush()
					<-r.Context().Done()
				case "/containers/abc/logs":
					tail <- r.URL.Query().Get("tail")
					sinceQuery <- r.URL.Query().Get("since")
					if refused {
						w.WriteHeader(http.StatusBadRequest)
						_, _ = io.WriteString(w, `{"message":"logging driver does not support reading"}`)
						return
					}
					_, _ = io.WriteString(w, "kept output\n")
					w.(http.Flusher).Flush()
					<-r.Context().Done()
				default:
					http.NotFound(w, r)
				}
			}))
			defer engine.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			m := docker.NewMonitor(containerTestHost{endpoint: "tcp://" + engine.Listener.Addr().String()}, time.Second, func() string { return "" })
			go m.Run(ctx)
			deadline := time.Now().Add(3 * time.Second)
			for {
				if _, ok := m.Find("demo"); ok {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("monitor did not find fake container")
				}
				time.Sleep(10 * time.Millisecond)
			}
			s := &Server{opt: Options{Docker: m}}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { r.SetPathValue("ref", "demo"); s.containerLogs(w, r) }))
			defer srv.Close()
			rctx, rcancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer rcancel()
			ws, _, err := websocket.Dial(rctx, "ws"+strings.TrimPrefix(srv.URL, "http")+"?lines=7&since=2026-01-01T00:00:00Z&id=previous", nil)
			if err != nil {
				t.Fatal(err)
			}
			defer ws.CloseNow()
			kind, data, err := ws.Read(rctx)
			if err != nil {
				t.Fatal(err)
			}
			if kind != websocket.MessageText {
				t.Fatalf("first message is not a status: %s", data)
			}
			var msg struct {
				T string `json:"t"`
				D string `json:"d"`
			}
			if err := json.Unmarshal(data, &msg); err != nil {
				t.Fatal(err)
			}
			if refused {
				if msg.T != "error" || !strings.Contains(msg.D, "logging driver") {
					t.Fatalf("missing persistent error: %s", data)
				}
			} else {
				if msg.T != "status" {
					t.Fatalf("unexpected status: %s", data)
				}
				kind, data, err = ws.Read(rctx)
				if err != nil || kind != websocket.MessageBinary || string(data) != "kept output\n" {
					t.Fatalf("output = %s, %v", data, err)
				}

				kind, data, err = ws.Read(rctx)
				var cursor struct {
					T  string `json:"t"`
					D  string `json:"d"`
					ID string `json:"id"`
				}
				if err != nil || kind != websocket.MessageText || json.Unmarshal(data, &cursor) != nil || cursor.T != "cursor" || cursor.ID != "abc" {
					t.Fatalf("missing daemon-side cursor: %s, %v", data, err)
				}
				if _, err := time.Parse(time.RFC3339Nano, cursor.D); err != nil {
					t.Fatal(err)
				}
			}
			if got := <-sinceQuery; got != "" {
				t.Fatalf("recreated container retained an old cursor: %q", got)
			}
			if got := <-tail; got != "7" {
				t.Fatalf("tail = %q, want 7", got)
			}
		})
	}
}

func TestProjectRemovalNeedsConfirmation(t *testing.T) {
	s := &Server{}
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{}`))
	r.SetPathValue("action", "down")
	w := httptest.NewRecorder()
	s.containerProjectAction(w, r)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "confirm removal") {
		t.Fatalf("unconfirmed removal: %d %s", w.Code, w.Body.String())
	}
}

// A published port reachable from the network is flagged unless the
// container's owning server sets lan = true; loopback ports never are.
func TestNetworkPortsSilencedByOwnerLAN(t *testing.T) {
	servers := []config.Server{
		{Name: "db", Dir: "/work/db", Cmd: "docker compose up"},
		{Name: "lab", Dir: "/work/lab", Cmd: "docker compose up", LAN: true},
	}
	network := []docker.Port{{Public: 5432, Proto: "tcp", Exposure: "network"}, {Public: 6379, Proto: "tcp", Exposure: "loopback"}}
	containers := []docker.Container{
		{Name: "db-1", WorkingDir: "/work/db", Ports: slices.Clone(network)},
		{Name: "lab-1", WorkingDir: "/work/lab", Ports: slices.Clone(network)},
		{Name: "loose", WorkingDir: "/elsewhere", Ports: slices.Clone(network)},
	}
	attachContainers(containers, servers)
	markNetworkPorts(containers, servers)
	for i, want := range []bool{true, false, true} {
		c := containers[i]
		if c.Ports[0].NetworkWarning != want || c.Ports[1].NetworkWarning {
			t.Errorf("%s (server %q): warnings = %v, %v; want %v, false", c.Name, c.Server, c.Ports[0].NetworkWarning, c.Ports[1].NetworkWarning, want)
		}
	}
}
