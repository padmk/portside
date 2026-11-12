package containers

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/padmk/portside/internal/docker"
	"github.com/padmk/portside/internal/server"
	"github.com/padmk/portside/internal/supervisor"
	"github.com/padmk/portside/internal/tui/keymap"
	"github.com/padmk/portside/internal/tui/theme"
	"github.com/padmk/portside/internal/tui/ui"
	"github.com/padmk/portside/internal/viewfmt"
)

func code(n int) *int { return &n }

// state is a ready engine with a compose project of two services, an
// exited one-off container outside any project and a project whose job
// runs.
func state() *server.State {
	compose := func(name, project, service, st string) docker.Container {
		return docker.Container{ID: "id-" + name, Name: name, Project: project, Service: service, WorkingDir: "/srv/" + project, State: st}
	}
	db := compose("shop-db-1", "shop", "db", "running")
	db.Health, db.Server = "unhealthy", "web"
	db.Ports = []docker.Port{{Public: 5432, Private: 5432, Proto: "tcp"}}
	cache := compose("shop-cache-1", "shop", "cache", "exited")
	cache.Failed, cache.ExitCode, cache.Server = true, code(137), "web"
	cache.OOMKilled = true
	loose := docker.Container{ID: "id-tool", Name: "tool", State: "exited"}
	busy := compose("jobs-app-1", "jobs", "app", "running")
	busy.Job = &docker.JobView{Kind: "recreate", Running: true}
	return &server.State{Docker: docker.State{Status: docker.Ready, Containers: []docker.Container{busy, cache, db, loose}}}
}

func testEnv() *ui.Env {
	return &ui.Env{State: state(), Styles: theme.New(theme.TerminalFamily.Variant(true)), Glyphs: theme.Unicode,
		Pending: map[ui.Selection]string{}, Now: func() time.Time { return time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC) }}
}

func TestProjectsGroupByNameWithOtherContainersLast(t *testing.T) {
	var got []string
	for _, p := range Projects(state()) {
		var names []string
		for _, c := range p.Containers {
			names = append(names, c.Name)
		}
		got = append(got, p.Name+":"+strings.Join(names, ","))
	}
	want := "jobs:jobs-app-1 shop:shop-cache-1,shop-db-1 :tool"
	if strings.Join(got, " ") != want {
		t.Errorf("projects %q, want %q", strings.Join(got, " "), want)
	}
}

func TestStatusOfFollowsTheDashboard(t *testing.T) {
	for _, c := range []struct {
		c    docker.Container
		want string
	}{
		{docker.Container{State: "running", Health: "unhealthy"}, "crashed unhealthy"},
		{docker.Container{State: "running", Health: "starting"}, "starting checking health"},
		{docker.Container{State: "running"}, "running running"},
		{docker.Container{State: "exited", Failed: true}, "crashed crashed"},
		{docker.Container{State: "exited"}, "exited stopped"},
		{docker.Container{State: "dead"}, "crashed dead"},
	} {
		st := StatusOf(c.c)
		if got := string(st.Display) + " " + st.Label; got != c.want {
			t.Errorf("%+v: %q, want %q", c.c, got, c.want)
		}
	}
}

func TestOffSaysWhyAnActionDoesNotApply(t *testing.T) {
	env := testEnv()
	db, cache := ui.ContainerItem("id-shop-db-1", "shop-db-1"), ui.ContainerItem("id-shop-cache-1", "shop-cache-1")
	tool, busy := ui.ContainerItem("id-tool", "tool"), ui.ContainerItem("id-jobs-app-1", "jobs-app-1")
	for _, c := range []struct {
		id   string
		it   ui.Selection
		want string
	}{
		{"shell", cache, "not running; start it to open a shell"},
		{"shell", db, ""},
		{"stop", cache, "not running"},
		{"start", db, "already unhealthy"},
		{"toggle", db, ""},
		{"open", cache, "publishes no port"},
		{"open", db, ""},
		{"rebuild", db, "no known build; pull instead"},
		{"recreate", tool, "not a compose service"},
		{"recreate", busy, "a compose job is running for jobs"},
		{"down-project", tool, "not in a compose project"},
		{"stop-project", ui.ProjectItem("shop"), ""},
		{"restart", ui.ProjectItem("shop"), "a project has no restart; restart its containers"},
		{"shell", ui.ProjectItem("shop"), "needs a container, not a project header"},
		{"down-project", ui.ProjectItem(""), "not a compose project"},
		{"start-project", ui.ProjectItem("jobs"), "a compose job is running for jobs"},
		{"dismiss-job", busy, "the job is still running"},
		{"dismiss-job", db, "no compose job to dismiss"},
		{"start", ui.ContainerItem("gone", "gone"), "no container named gone"},
	} {
		if got := Off(env, c.id, c.it); got != c.want {
			t.Errorf("%s on %s: %q, want %q", c.id, c.it.Name, got, c.want)
		}
	}
	env.State.Docker.Status = docker.Unavailable
	if got := Off(env, "start", db); !strings.Contains(got, "Docker is not running") {
		t.Errorf("start with the engine down: %q", got)
	}
}

func TestRowsShowProblemsPortsAndOwner(t *testing.T) {
	env := testEnv()
	p := New()
	out := ansi.Strip(p.View(env, 60, 12, true))
	for _, want := range []string{"▾ jobs", "▾ shop [web]", "other containers", "db unhealthy", ":5432", "cache crashed (137) OOM", "tool stopped"} {
		if !strings.Contains(out, want) {
			t.Errorf("list lacks %q:\n%s", want, out)
		}
	}
	// The cursor opens on the first container, not a header.
	if it := p.Cursor(env); it != ui.ContainerItem("id-jobs-app-1", "jobs-app-1") {
		t.Errorf("first cursor %+v", it)
	}
}

func TestShownNeedsContainersOrAnEngineProblem(t *testing.T) {
	st := &server.State{Docker: docker.State{Status: docker.Unavailable}}
	if Shown(st) {
		t.Error("shown with the engine down and no server using it")
	}
	st.Servers = []supervisor.View{{Name: "db", Cmd: "docker compose up"}}
	if !Shown(st) {
		t.Error("hidden with the engine down and a compose server")
	}
	st.Docker.Status, st.Servers = docker.Ready, nil
	if Shown(st) {
		t.Error("shown with no containers")
	}
	if !Shown(state()) {
		t.Error("hidden with containers")
	}
}

func TestContainerEnvironmentMasksSecrets(t *testing.T) {
	got := envLines([]server.EnvVar{
		{Key: "PATH", Value: "/usr/bin"},
		{Key: "POSTGRES_PASSWORD", Value: "hunter2"},
		{Key: "DATABASE_URL", Value: "postgres://app:pw@db/app"},
	})
	want := "PATH=/usr/bin\nPOSTGRES_PASSWORD=" + viewfmt.SecretMask + "\nDATABASE_URL=" + viewfmt.SecretMask + "\n(2 secrets hidden)"
	if got != want {
		t.Errorf("env lines %q, want %q", got, want)
	}
}

// TestDetailTabsAreTheServersStrip: the container's tabs are drawn and
// clicked as a server's: every title when wide, arrows when narrow, and a
// click on a tab or an arrow opens that tab.
func TestDetailTabsAreTheServersStrip(t *testing.T) {
	env := testEnv()
	env.Selected = ui.ContainerItem("id-shop-db-1", "shop-db-1")
	d := NewDetail()
	d.shown = "id-shop-db-1" // no stream in a test
	click := func(top, text string) {
		t.Helper()
		plain := ansi.Strip(top)
		i := strings.Index(plain, text)
		if i < 0 {
			t.Fatalf("%q lacks %q", plain, text)
		}
		x := ansi.StringWidth(plain[:i])
		d.Update(env, tea.MouseClickMsg(tea.Mouse{X: x, Y: 0, Button: tea.MouseLeft}))
	}
	top := strings.Split(d.View(env, 100, 12, true), "\n")[0]
	if plain := ansi.Strip(top); strings.Contains(plain, "[") || !strings.Contains(plain, "Details") || !strings.Contains(plain, "unhealthy") {
		t.Errorf("wide strip = %q", plain)
	}
	click(top, "Shell")
	if d.tab != tabShell {
		t.Fatalf("a click on Shell opened tab %d", d.tab)
	}
	top = strings.Split(d.View(env, 26, 12, true), "\n")[0]
	if got := ansi.StringWidth(top); got != 26 {
		t.Errorf("narrow top border is %d cells", got)
	}
	click(top, "‹")
	if d.tab != tabDetails {
		t.Fatalf("the left arrow opened tab %d, want Details", d.tab)
	}
}

// TestDetailFactsWrapAndPosition: long facts wrap, an overflowing tab
// shows its position in the bottom border, and the cursor shows only
// while the pane is focused.
func TestDetailFactsWrapAndPosition(t *testing.T) {
	env := testEnv()
	env.State.Docker.Containers[2].Image = strings.Repeat("registry/", 8) + "postgres:17"
	env.Selected = ui.ContainerItem("id-shop-db-1", "shop-db-1")
	d := NewDetail()
	d.shown, d.tab = "id-shop-db-1", tabDetails
	out := strings.Split(ansi.Strip(d.View(env, 40, 8, true)), "\n")
	if bottom := out[len(out)-1]; !strings.Contains(bottom, "1/") {
		t.Errorf("bottom border = %q, want the position", bottom)
	}
	if !strings.Contains(strings.Join(out, "\n"), env.Glyphs.Cursor) {
		t.Error("no cursor while focused")
	}
	if strings.Contains(ansi.Strip(d.View(env, 40, 8, false)), env.Glyphs.Cursor) {
		t.Error("cursor drawn while not focused")
	}
	d.Do(env, keymap.Action{ID: "page-down"})
	moved := strings.Split(ansi.Strip(d.View(env, 40, 8, true)), "\n")
	if moved[1] == out[1] {
		t.Error("page-down did not scroll")
	}
	wide := strings.Join(strings.Split(ansi.Strip(d.View(env, 40, 30, true)), "\n"), "\n")
	if !strings.Contains(wide, "postgres:17") {
		t.Errorf("the long image is cut instead of wrapped:\n%s", wide)
	}
}
