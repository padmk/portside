package config

import (
	"reflect"
	"strings"
	"testing"
)

// fullSpec sets every server key the form edits.
func fullSpec() Spec {
	off, crash, none := false, "~/bin/report", ""
	return Spec{
		Name: "web", Dir: "web", Cmd: "pnpm dev --port {PORT}", Port: new(3100), Group: "front",
		DependsOn: []string{"db"}, Projects: []string{"web_db"}, Env: map[string]string{"A": "1"},
		Restart: RestartAlways, Health: "/healthz", ReadyLog: `ready in \d+`, ReadyAfter: "2s",
		Icon: "icon.png", Open: "/admin", Stop: "30s", Enabled: &off, LAN: true,
		Schedule: "0 9 * * mon-fri", Watch: []string{"src/**/*.py", "pyproject.toml"}, Wrap: "nix develop -c",
		OnCrash: &crash, OnUnready: &none, OnRecover: &crash, UnreadyAfter: "2m", RetryAfter: "5m",
		Umask: "0077", MemoryLimit: "0",
	}
}

// Every field the form sends is written by AddServer and UpdateServer and
// read back as written.
func TestSpecEveryFieldRoundTrips(t *testing.T) {
	base := "[servers.db]\ncmd = \"db\"\n"
	want := fullSpec()
	path := newConfig(t, base)
	if err := AddServer(path, want); err != nil {
		t.Fatal(err)
	}
	web, _ := mustLoad(t, path).Server("web")
	if got := SpecOf(web); !reflect.DeepEqual(got, want) {
		t.Errorf("AddServer read back\n%+v\nwant\n%+v", got, want)
	}

	path = newConfig(t, base+"\n# web\n[servers.web]\ncmd = \"x\" # command\n")
	if err := UpdateServer(path, "web", want); err != nil {
		t.Fatal(err)
	}
	web, _ = mustLoad(t, path).Server("web")
	if got := SpecOf(web); !reflect.DeepEqual(got, want) {
		t.Errorf("UpdateServer read back\n%+v\nwant\n%+v", got, want)
	}
	if text := readConfig(t, path); !strings.Contains(text, "# web\n[servers.web]\ncmd = \"pnpm dev --port {PORT}\" # command\n") {
		t.Errorf("comments lost:\n%s", text)
	}
	if web.OnCrash != "~/bin/report" || web.OnUnready != "" || web.MemoryLimit.Bytes != 0 || !web.LAN || web.Schedule == nil || web.Watch == nil {
		t.Errorf("loaded %+v", web)
	}

	// Clearing every optional field removes its key.
	if err := UpdateServer(path, "web", Spec{Name: "web", Cmd: "x", Restart: RestartOnFailure}); err != nil {
		t.Fatal(err)
	}
	wantText(t, path, base+"\n# web\n[servers.web]\ncmd = \"x\" # command\n")
}

// Saving a form unchanged leaves the table byte for byte, also with values
// written in another form than Render's.
func TestSpecUnchangedFieldsKeepText(t *testing.T) {
	text := "[servers.web]\ncmd = 'x'\nready_after = \"1m0s\" # wait\nstop_timeout = '90s'\nwrap = \"auto\"\nlan = false\non_crash = '' # none\nwatch = [\n  'src/**', # sources\n]\nmemory_limit = \"1.5gb\"\nenabled = true\n"
	path := newConfig(t, text)
	web, _ := mustLoad(t, path).Server("web")
	sp := SpecOf(web)
	if sp.ReadyAfter != "1m0s" || sp.Stop != "90s" || sp.MemoryLimit != "1.5gb" || sp.OnCrash == nil || *sp.OnCrash != "" {
		t.Errorf("spec = %+v", sp)
	}
	if err := UpdateServer(path, "web", sp); err != nil {
		t.Fatal(err)
	}
	wantText(t, path, text)
	sp.Group = "g"
	if err := UpdateServer(path, "web", sp); err != nil {
		t.Fatal(err)
	}
	wantText(t, path, strings.Replace(text, "enabled = true\n", "enabled = true\ngroup = \"g\"\n", 1))
}

// A task stays a task across an edit with its default restart policy
// unwritten, turning it into a server writes only what differs, and a
// setting a task cannot have is refused.
func TestSpecTaskEdits(t *testing.T) {
	path := newConfig(t, "[servers.migrate]\ncmd = \"x\"\noneshot = true\n")
	m, _ := mustLoad(t, path).Server("migrate")
	sp := SpecOf(m)
	if !sp.Oneshot || sp.Restart != RestartNo || sp.Port != nil {
		t.Fatalf("spec = %+v", sp)
	}
	sp.Cmd = "y"
	if err := UpdateServer(path, "migrate", sp); err != nil {
		t.Fatal(err)
	}
	wantText(t, path, "[servers.migrate]\ncmd = \"y\"\noneshot = true\n")
	if m, _ = mustLoad(t, path).Server("migrate"); !m.Oneshot {
		t.Fatal("no longer a task")
	}
	sp.Oneshot, sp.Restart = false, RestartOnFailure
	if err := UpdateServer(path, "migrate", sp); err != nil {
		t.Fatal(err)
	}
	wantText(t, path, "[servers.migrate]\ncmd = \"y\"\n")
	before := readConfig(t, path)
	sp.Oneshot, sp.Health = true, "/"
	wantRejected(t, path, before, UpdateServer(path, "migrate", sp))
}

// Turning a task with an explicit restart into a server, or back, writes the
// restart the form chose, whatever each side leaves unwritten.
func TestSpecTaskConversionRestart(t *testing.T) {
	path := newConfig(t, "[servers.job]\ncmd = \"x\"\noneshot = true\nrestart = \"no\"\n")
	sp := SpecOf(func() Server { s, _ := mustLoad(t, path).Server("job"); return s }())
	sp.Oneshot, sp.Restart = false, RestartOnFailure
	if err := UpdateServer(path, "job", sp); err != nil {
		t.Fatal(err)
	}
	if s, _ := mustLoad(t, path).Server("job"); s.Oneshot || s.Restart != RestartOnFailure {
		t.Fatalf("task to server: oneshot %v, restart %q\n%s", s.Oneshot, s.Restart, readConfig(t, path))
	}
	// A server keeping on-failure as it becomes a task gets it written.
	sp.Oneshot, sp.Restart, sp.Port = true, RestartOnFailure, nil
	if err := UpdateServer(path, "job", sp); err != nil {
		t.Fatal(err)
	}
	if s, _ := mustLoad(t, path).Server("job"); !s.Oneshot || s.Restart != RestartOnFailure {
		t.Fatalf("server to task: oneshot %v, restart %q\n%s", s.Oneshot, s.Restart, readConfig(t, path))
	}
}
