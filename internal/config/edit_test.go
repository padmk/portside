package config

import (
	"slices"

	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The fixture is built from pieces so that expected results can be composed
// from the parts an edit must leave alone.
const (
	fixHead = "# top of file comment\n\nroot = \"/tmp\"\nport = 7999 # daemon port\n\n"
	fixWebC = "# web comment\n"
	fixWeb  = "[servers.web]\n# inside comment\ndir = \"web\" # trailing\ncmd = \"npm run dev\"\n"
	fixRest = "# between tables\n\n# api comment\n[servers.api]\ncmd = \"go run .\"\n\n[other]\nx = 1 # keep\n"
	fixture = fixHead + fixWebC + fixWeb + fixRest
)

func newConfig(t *testing.T, text string) string {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	path := filepath.Join(t.TempDir(), "portside.toml")
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func readConfig(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func mustLoad(t *testing.T, path string) *Config {
	t.Helper()
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("edited file does not load: %v\n%s", err, readConfig(t, path))
	}
	return cfg
}

func wantText(t *testing.T, path, want string) {
	t.Helper()
	if got := readConfig(t, path); got != want {
		t.Errorf("file text differs\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
	mustLoad(t, path)
}

func wantRejected(t *testing.T, path, before string, err error) {
	t.Helper()
	if err == nil {
		t.Error("edit was accepted, want an error")
	}
	if got := readConfig(t, path); got != before {
		t.Errorf("rejected edit changed the file\n--- got ---\n%s\n--- want ---\n%s", got, before)
	}
}

func TestAddServer(t *testing.T) {
	path := newConfig(t, fixture)
	if err := AddServer(path, Spec{Name: "db", Cmd: "postgres", Port: new(5432), Comment: "database"}); err != nil {
		t.Fatal(err)
	}
	wantText(t, path, fixture+"\n# database\n[servers.db]\ncmd = \"postgres\"\nport = 5432\n")
	if s, ok := mustLoad(t, path).Server("db"); !ok || s.Port != 5432 {
		t.Errorf("added server = %+v, %v", s, ok)
	}
}

func TestAddServerToEmptyFile(t *testing.T) {
	path := newConfig(t, "")
	if err := AddServer(path, Spec{Name: "web", Cmd: "x"}); err != nil {
		t.Fatal(err)
	}
	wantText(t, path, "[servers.web]\ncmd = \"x\"\n")
}

func TestAddServerAddsFinalNewline(t *testing.T) {
	path := newConfig(t, "[servers.a]\ncmd = \"a\"")
	if err := AddServer(path, Spec{Name: "b", Cmd: "b"}); err != nil {
		t.Fatal(err)
	}
	wantText(t, path, "[servers.a]\ncmd = \"a\"\n\n[servers.b]\ncmd = \"b\"\n")
}

func TestAddServerRejected(t *testing.T) {
	cases := map[string]Spec{
		"duplicate":   {Name: "web", Cmd: "x"},
		"bad name":    {Name: "Web App", Cmd: "x"},
		"empty cmd":   {Name: "new", Cmd: " "},
		"bad restart": {Name: "new", Cmd: "x", Restart: "sometimes"},
		"daemon port": {Name: "new", Cmd: "x", Port: new(7999)},
		"unknown dep": {Name: "new", Cmd: "x", DependsOn: []string{"ghost"}},
		"self cycle":  {Name: "new", Cmd: "x", DependsOn: []string{"new"}},
	}
	for name, spec := range cases {
		t.Run(name, func(t *testing.T) {
			path := newConfig(t, fixture)
			wantRejected(t, path, fixture, AddServer(path, spec))
		})
	}
}

func TestUpdateServerKeepsCommentAboveAndNeighbours(t *testing.T) {
	path := newConfig(t, fixture)
	err := UpdateServer(path, "web", Spec{Name: "web", Dir: "web", Cmd: "pnpm dev", Port: new(3100)})
	if err != nil {
		t.Fatal(err)
	}
	want := fixHead + fixWebC + "[servers.web]\n# inside comment\ndir = \"web\" # trailing\ncmd = \"pnpm dev\"\nport = 3100\n" + fixRest
	wantText(t, path, want)
}

func TestUpdateServerNewComment(t *testing.T) {
	path := newConfig(t, fixture)
	if err := UpdateServer(path, "web", Spec{Name: "web", Cmd: "x", Comment: "fresh"}); err != nil {
		t.Fatal(err)
	}
	wantText(t, path, fixHead+"# fresh\n[servers.web]\n# inside comment\n# trailing\ncmd = \"x\"\n"+fixRest)
}

func TestUpdateServerPatchesOnlyChangedValues(t *testing.T) {
	text := `[servers."web"] # service
dir = 'web' # working directory
cmd = """echo '# not a comment'
echo ready""" # command
depends_on = [
  "db", # keep this dependency note
]
custom = 'keep me'

[servers.web.env] # environment
KEEP = 'original' # untouched
CHANGE = "old" # changed
REMOVE = "old" # retain this note

[servers.db]
cmd = "db"
`
	path := newConfig(t, text)
	sv, _ := mustLoad(t, path).Server("web")
	sp := SpecOf(sv)
	sp.Name, sp.Cmd = "site", "echo changed"
	sp.Env = map[string]string{"KEEP": "original", "CHANGE": "new", "ADD": "value"}
	if err := UpdateServer(path, "web", sp); err != nil {
		t.Fatal(err)
	}
	got := readConfig(t, path)
	for _, keep := range []string{"[servers.site] # service", "# command", "dir = 'web' # working directory", "custom = 'keep me'", "[servers.site.env] # environment", "KEEP = 'original' # untouched", "CHANGE = \"new\" # changed", "# retain this note", "ADD = \"value\"", "  \"db\", # keep this dependency note"} {
		if !strings.Contains(got, keep) {
			t.Errorf("missing %q:\n%s", keep, got)
		}
	}
	loaded := mustLoad(t, path)
	site, ok := loaded.Server("site")
	if !ok || site.Cmd != "echo changed" || len(site.Env) != 3 {
		t.Fatalf("server = %+v", site)
	}
}

func TestUpdateServerNoChangeKeepsFormatting(t *testing.T) {
	text := `[servers.web]
cmd = '''echo ready
echo '# string content'
'''
enabled = true # explicit default
restart = 'no'
env.FOO = 'bar' # dotted environment
`
	path := newConfig(t, text)
	sv, _ := mustLoad(t, path).Server("web")
	if err := UpdateServer(path, "web", SpecOf(sv)); err != nil {
		t.Fatal(err)
	}
	wantText(t, path, text)
}

func TestUpdateServerDottedEnvironment(t *testing.T) {
	path := newConfig(t, "[servers.web]\ncmd = 'web'\nenv.FOO = 'old' # keep\nenv.'WITH SPACE' = 'old'\n")
	sv, _ := mustLoad(t, path).Server("web")
	sp := SpecOf(sv)
	sp.Env = map[string]string{"FOO": "new", "ADDED": "value"}
	if err := UpdateServer(path, "web", sp); err != nil {
		t.Fatal(err)
	}
	wantText(t, path, "[servers.web]\ncmd = 'web'\nenv.FOO = \"new\" # keep\nenv.ADDED = \"value\"\n")
}

func TestLoadAssignedPortsKeepsPortsAfterRemovalAndReorder(t *testing.T) {
	text := "# config\n[servers.a]\ncmd = 'a' # keep\n\n[servers.b]\ncmd = 'b'\n\n[servers.worker]\ncmd = 'worker'\nport = 0\n"
	path := newConfig(t, text)
	cfg, err := LoadAssignedPorts(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	wantText(t, path, strings.Replace(strings.Replace(text, "cmd = 'a' # keep\n", "cmd = 'a' # keep\nport = 3001\n", 1), "cmd = 'b'\n", "cmd = 'b'\nport = 3002\n", 1))
	if cfg.Servers[0].PortAuto || cfg.Servers[1].Port != 3002 {
		t.Fatalf("servers = %+v", cfg.Servers)
	}
	if err := RemoveServer(path, "a"); err != nil {
		t.Fatal(err)
	}
	if err := AddServer(path, Spec{Name: "c", Cmd: "c"}); err != nil {
		t.Fatal(err)
	}
	cfg, err = LoadAssignedPorts(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := cfg.Server("b")
	c, _ := cfg.Server("c")
	worker, _ := cfg.Server("worker")
	if b.Port != 3002 || c.Port != 3001 || worker.Port != 0 {
		t.Fatalf("ports: b=%d c=%d worker=%d", b.Port, c.Port, worker.Port)
	}
	before := readConfig(t, path)
	if _, err := LoadAssignedPorts(path, nil); err != nil {
		t.Fatal(err)
	}
	wantText(t, path, before)
	parts := strings.Split(before, "[servers.c]")
	if err := WriteAtomic(path, []byte("[servers.c]"+parts[1]+"\n"+parts[0])); err != nil {
		t.Fatal(err)
	}
	cfg, err = LoadAssignedPorts(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	b, _ = cfg.Server("b")
	if b.Port != 3002 {
		t.Fatalf("reordered port = %d", b.Port)
	}
}

// Automatic ports skip ports held outside the config, such as those of
// temporary servers.
func TestLoadAssignedPortsAvoidsPorts(t *testing.T) {
	path := newConfig(t, "[servers.a]\ncmd = 'a'\n")
	cfg, err := LoadAssignedPorts(path, func(p int) bool { return p == 3001 })
	if err != nil {
		t.Fatal(err)
	}
	wantText(t, path, "[servers.a]\ncmd = 'a'\nport = 3002\n")
	if a, _ := cfg.Server("a"); a.Port != 3002 {
		t.Fatalf("port %d", a.Port)
	}
}

// A server written with dotted keys at the top level gets its port there,
// not in the table that follows.
func TestLoadAssignedPortsDottedServer(t *testing.T) {
	text := "# config\nservers.web.cmd = 'web'\n\n# api\n[servers.api]\ncmd = 'api'\nport = 4000\n"
	path := newConfig(t, text)
	cfg, err := LoadAssignedPorts(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	wantText(t, path, strings.Replace(text, "servers.web.cmd = 'web'\n", "servers.web.cmd = 'web'\nservers.web.port = 3001\n", 1))
	web, _ := cfg.Server("web")
	api, _ := cfg.Server("api")
	if web.Port != 3001 || web.PortAuto || api.Port != 4000 {
		t.Fatalf("ports: web=%d auto=%v api=%d", web.Port, web.PortAuto, api.Port)
	}
}

func TestUpdateServerLastTable(t *testing.T) {
	path := newConfig(t, fixture)
	if err := UpdateServer(path, "api", Spec{Name: "api", Cmd: "go run ./cmd"}); err != nil {
		t.Fatal(err)
	}
	want := strings.Replace(fixture, "[servers.api]\ncmd = \"go run .\"\n", "[servers.api]\ncmd = \"go run ./cmd\"\n", 1)
	wantText(t, path, want)
}

func TestUpdateServerRenameFixesDependencies(t *testing.T) {
	text := `[servers.web]
cmd = "web"

[servers.db]
cmd = "db"

[servers.api]
cmd = "web"
depends_on = [ # keep the old name "web" and this bracket ]
  "web", # primary "web"
  'db',
]

[servers.worker]
cmd = "w"
depends_on = ["web-ui", "web"]

[servers.web-ui]
cmd = "ui"
`
	path := newConfig(t, text)
	if err := UpdateServer(path, "web", Spec{Name: "site", Cmd: "web"}); err != nil {
		t.Fatal(err)
	}
	want := strings.Replace(text, "[servers.web]\n", "[servers.site]\n", 1)
	want = strings.Replace(want, "  \"web\", # primary", "  \"site\", # primary", 1)
	want = strings.Replace(want, `["web-ui", "web"]`, `["web-ui", "site"]`, 1)
	wantText(t, path, want)
	cfg := mustLoad(t, path)
	if api, _ := cfg.Server("api"); api.Cmd != "web" || len(api.DependsOn) != 2 || api.DependsOn[0] != "site" {
		t.Errorf("api = %+v", api)
	}
}

func TestUpdateServerRejected(t *testing.T) {
	cases := map[string]struct {
		name string
		spec Spec
	}{
		"missing":     {"ghost", Spec{Name: "ghost", Cmd: "x"}},
		"rename dup":  {"web", Spec{Name: "api", Cmd: "x"}},
		"bad name":    {"web", Spec{Name: "Web", Cmd: "x"}},
		"empty cmd":   {"web", Spec{Name: "web"}},
		"bad restart": {"web", Spec{Name: "web", Cmd: "x", Restart: "x"}},
		"self cycle":  {"web", Spec{Name: "web", Cmd: "x", DependsOn: []string{"web"}}},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			path := newConfig(t, fixture)
			wantRejected(t, path, fixture, UpdateServer(path, c.name, c.spec))
		})
	}
}

func TestRemoveServer(t *testing.T) {
	path := newConfig(t, fixture)
	if err := RemoveServer(path, "web"); err != nil {
		t.Fatal(err)
	}
	wantText(t, path, fixHead+fixRest)
	if _, ok := mustLoad(t, path).Server("web"); ok {
		t.Error("web still loaded")
	}
}

func TestRemoveLastServerCollapsesBlankLine(t *testing.T) {
	path := newConfig(t, fixture)
	if err := RemoveServer(path, "api"); err != nil {
		t.Fatal(err)
	}
	want := fixHead + fixWebC + fixWeb + "# between tables\n\n[other]\nx = 1 # keep\n"
	wantText(t, path, want)
}

func TestAddThenRemoveRestoresFile(t *testing.T) {
	for name, text := range map[string]string{
		"settings only":  "port = 7980\n",
		"after a server": "port = 7980\n\n[servers.web]\ncmd = \"npm run dev\"\n",
	} {
		t.Run(name, func(t *testing.T) {
			path := newConfig(t, text)
			if err := AddServer(path, Spec{Name: "api", Cmd: "go run ."}); err != nil {
				t.Fatal(err)
			}
			if err := RemoveServer(path, "api"); err != nil {
				t.Fatal(err)
			}
			wantText(t, path, text)
		})
	}
}

func TestRemoveServerRejected(t *testing.T) {
	path := newConfig(t, fixture)
	wantRejected(t, path, fixture, RemoveServer(path, "ghost"))

	deps := "[servers.web]\ncmd = \"x\"\n\n[servers.api]\ncmd = \"y\"\ndepends_on = [\"web\"]\n"
	path = newConfig(t, deps)
	wantRejected(t, path, deps, RemoveServer(path, "web"))
}

func TestSetKeyReplacesAndInserts(t *testing.T) {
	path := newConfig(t, fixture)
	if err := SetKey(path, "web", "cmd", `"pnpm dev"`); err != nil {
		t.Fatal(err)
	}
	want := strings.Replace(fixture, "cmd = \"npm run dev\"\n", "cmd = \"pnpm dev\"\n", 1)
	wantText(t, path, want)

	if err := SetKey(path, "web", "port", "3100"); err != nil {
		t.Fatal(err)
	}
	want = strings.Replace(want, "cmd = \"pnpm dev\"\n", "cmd = \"pnpm dev\"\nport = 3100\n", 1)
	wantText(t, path, want)
	if s, _ := mustLoad(t, path).Server("web"); s.Port != 3100 || s.PortAuto {
		t.Errorf("web port = %d (auto %v)", s.Port, s.PortAuto)
	}
}

func TestSetKeyIgnoresOtherTablesAndPrefixes(t *testing.T) {
	text := "[servers.a]\ncmd = \"a\"\nportal = 1\n\n[servers.b]\ncmd = \"b\"\nport = 3500\n"
	path := newConfig(t, text)
	if err := SetKey(path, "a", "port", "3400"); err != nil {
		t.Fatal(err)
	}
	wantText(t, path, "[servers.a]\ncmd = \"a\"\nportal = 1\nport = 3400\n\n[servers.b]\ncmd = \"b\"\nport = 3500\n")
}

func TestSetKeyBeforeSubTable(t *testing.T) {
	text := "[servers.web]\ncmd = \"x\"\n\n[servers.web.env]\nport = \"keep\"\nA = \"1\"\n\n[servers.api]\ncmd = \"y\"\n"
	path := newConfig(t, text)
	if err := SetKey(path, "web", "port", "3100"); err != nil {
		t.Fatal(err)
	}
	cfg := mustLoad(t, path)
	web, _ := cfg.Server("web")
	if web.Port != 3100 || web.Env["port"] != "keep" || web.Env["A"] != "1" {
		t.Errorf("web = %+v", web)
	}
	if got := readConfig(t, path); !strings.Contains(got, "port = 3100\n\n[servers.web.env]") {
		t.Errorf("key was not placed before the sub-table:\n%s", got)
	}
}

func TestSetKeyRejected(t *testing.T) {
	cases := map[string]struct{ name, key, lit string }{
		"missing server": {"ghost", "port", "1"},
		"not toml":       {"web", "port", "not toml"},
		"bad value":      {"web", "restart", `"sometimes"`},
		"bad port":       {"web", "port", "99999"},
		"two lines":      {"web", "port", "1\n[servers.evil]"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			path := newConfig(t, fixture)
			wantRejected(t, path, fixture, SetKey(path, c.name, c.key, c.lit))
		})
	}
}

func TestSetEnabledTogglesOnlyThatLine(t *testing.T) {
	path := newConfig(t, fixture)
	if err := SetEnabled(path, "web", false); err != nil {
		t.Fatal(err)
	}
	off := strings.Replace(fixture, "cmd = \"npm run dev\"\n", "cmd = \"npm run dev\"\nenabled = false\n", 1)
	wantText(t, path, off)
	if s, _ := mustLoad(t, path).Server("web"); s.Enabled {
		t.Error("web still enabled")
	}

	if err := SetEnabled(path, "web", true); err != nil {
		t.Fatal(err)
	}
	wantText(t, path, strings.Replace(off, "enabled = false", "enabled = true", 1))
	if s, _ := mustLoad(t, path).Server("web"); !s.Enabled {
		t.Error("web not re-enabled")
	}
}

func TestSetEnabledMissingServer(t *testing.T) {
	path := newConfig(t, fixture)
	wantRejected(t, path, fixture, SetEnabled(path, "ghost", false))
}

func TestSetTopLevelReplace(t *testing.T) {
	path := newConfig(t, fixture)
	if err := SetTopLevel(path, "port", "7990"); err != nil {
		t.Fatal(err)
	}
	wantText(t, path, strings.Replace(fixture, "port = 7999 # daemon port\n", "port = 7990 # daemon port\n", 1))
	if mustLoad(t, path).Port != 7990 {
		t.Error("port not updated")
	}
}

func TestSetTopLevelInsertKeepsTableComment(t *testing.T) {
	path := newConfig(t, fixture)
	if err := SetTopLevel(path, "editor", `"vim"`); err != nil {
		t.Fatal(err)
	}
	wantText(t, path, strings.Replace(fixHead, "# daemon port\n", "# daemon port\neditor = \"vim\"\n", 1)+fixWebC+fixWeb+fixRest)
	if mustLoad(t, path).Editor != "vim" {
		t.Error("editor not set")
	}
}

func TestSetTopLevelDoesNotTouchTableKeys(t *testing.T) {
	text := "[servers.web]\ncmd = \"x\"\nport = 3100\n"
	path := newConfig(t, text)
	if err := SetTopLevel(path, "port", "7990"); err != nil {
		t.Fatal(err)
	}
	wantText(t, path, "port = 7990\n\n"+text)
	if s, _ := mustLoad(t, path).Server("web"); s.Port != 3100 {
		t.Errorf("web port = %d", s.Port)
	}
}

func TestSetTopLevelWithoutTables(t *testing.T) {
	path := newConfig(t, "root = \"/tmp\"\n")
	if err := SetTopLevel(path, "editor", `"vim"`); err != nil {
		t.Fatal(err)
	}
	wantText(t, path, "root = \"/tmp\"\neditor = \"vim\"\n")
}

func TestSetTopLevelRejected(t *testing.T) {
	path := newConfig(t, fixture)
	wantRejected(t, path, fixture, SetTopLevel(path, "port", "nope"))
}

// A command written as a multi-line string may contain text that looks like
// tables, comments and keys; none of it is structure.
const heredoc = "[servers.web]\ncmd = \"\"\"\necho hi\n[servers.ghost]\n# not a comment\nenabled = false\nport = 1\n\"\"\"\nport = 3100\n\n[servers.api]\ncmd = \"go run .\"\n"

func TestMultilineStringIsNotStructure(t *testing.T) {
	path := newConfig(t, heredoc)
	if err := SetEnabled(path, "web", false); err != nil {
		t.Fatal(err)
	}
	wantText(t, path, strings.Replace(heredoc, "port = 3100\n", "port = 3100\nenabled = false\n", 1))
	cfg := mustLoad(t, path)
	web, _ := cfg.Server("web")
	if web.Enabled || !strings.Contains(web.Cmd, "[servers.ghost]") || web.Port != 3100 {
		t.Errorf("web = %+v", web)
	}
	if _, ok := cfg.Server("ghost"); ok || len(cfg.Servers) != 2 {
		t.Errorf("servers = %+v", cfg.Servers)
	}

	if err := SetKey(path, "web", "port", "3200"); err != nil {
		t.Fatal(err)
	}
	if web, _ := mustLoad(t, path).Server("web"); web.Port != 3200 || !strings.Contains(web.Cmd, "port = 1") {
		t.Errorf("web = %+v", web)
	}
}

func TestRemoveServerWithMultilineString(t *testing.T) {
	path := newConfig(t, heredoc)
	if err := RemoveServer(path, "web"); err != nil {
		t.Fatal(err)
	}
	if got := readConfig(t, path); strings.TrimLeft(got, "\n") != "[servers.api]\ncmd = \"go run .\"\n" {
		t.Errorf("file text differs:\n%s", got)
	}
	mustLoad(t, path)
}

func TestRemoveFirstServerLeavesNoLeadingBlankLine(t *testing.T) {
	path := newConfig(t, heredoc)
	if err := RemoveServer(path, "web"); err != nil {
		t.Fatal(err)
	}
	wantText(t, path, "[servers.api]\ncmd = \"go run .\"\n")
}

func TestUpdateServerWithMultilineString(t *testing.T) {
	path := newConfig(t, heredoc)
	if err := UpdateServer(path, "web", Spec{Name: "web", Cmd: "x"}); err != nil {
		t.Fatal(err)
	}
	wantText(t, path, "[servers.web]\ncmd = \"x\"\n\n[servers.api]\ncmd = \"go run .\"\n")
}

func TestReplaceIfUnchanged(t *testing.T) {
	path := newConfig(t, fixture)
	next := fixture + "\n[servers.db]\ncmd = \"postgres\"\n"
	if err := ReplaceIfUnchanged(path, []byte(next), Revision([]byte(fixture))); err != nil {
		t.Fatal(err)
	}
	wantText(t, path, next)
}

func TestReplaceIfUnchangedStale(t *testing.T) {
	path := newConfig(t, fixture)
	stale := Revision([]byte(fixture))
	if err := SetEnabled(path, "web", false); err != nil {
		t.Fatal(err)
	}
	before := readConfig(t, path)
	err := ReplaceIfUnchanged(path, []byte(fixture), stale)
	if !errors.Is(err, ErrConflict) {
		t.Errorf("err = %v, want ErrConflict", err)
	}
	wantRejected(t, path, before, err)
}

func TestReplaceIfUnchangedInvalidText(t *testing.T) {
	path := newConfig(t, fixture)
	err := ReplaceIfUnchanged(path, []byte("[servers.a]\n"), Revision([]byte(fixture)))
	if errors.Is(err, ErrConflict) {
		t.Errorf("err = %v, want a parse error", err)
	}
	wantRejected(t, path, fixture, err)
}

func TestUpdateServerIfUnchanged(t *testing.T) {
	path := newConfig(t, fixture)
	rev := Revision([]byte(fixture))
	if err := SetEnabled(path, "web", false); err != nil {
		t.Fatal(err)
	}
	changed, _ := os.ReadFile(path)
	err := UpdateServerIfUnchanged(path, "web", Spec{Name: "web", Cmd: "x"}, rev)
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("changed file: err = %v, want ErrConflict", err)
	}
	wantRejected(t, path, string(changed), err)
	if err := UpdateServerIfUnchanged(path, "web", Spec{Name: "web", Cmd: "x"}, Revision(changed)); err != nil {
		t.Fatalf("unchanged file: %v", err)
	}
}

func TestRevision(t *testing.T) {
	a, b := Revision([]byte("a")), Revision([]byte("b"))
	if a == b || a != Revision([]byte("a")) || a == "" {
		t.Errorf("revisions: %q %q", a, b)
	}
}

func TestEditFollowsSymlinkAndKeepsMode(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	target := filepath.Join(dir, "dotfiles", "portside.toml")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte(fixture), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "portside.toml")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := SetEnabled(link, "web", false); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Lstat(link); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Errorf("config is no longer a symlink: %v %v", fi, err)
	}
	if got := readConfig(t, target); !strings.Contains(got, "enabled = false") {
		t.Errorf("target not edited:\n%s", got)
	}
	if fi, err := os.Stat(target); err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v %v, want 0600", fi.Mode().Perm(), err)
	}
}

func TestQuoteRoundTrip(t *testing.T) {
	cases := []string{
		"plain",
		`with "quotes"`,
		`back\slash and \n literal`,
		"line\nbreak\ttab\rreturn",
		"control\x01\x1f\x7f",
		"unicode é 世 \U0001f600",
		"hash # and = and [brackets]",
		"'single'",
		`"""triple"""`,
		"trailing backslash\\",
	}
	for _, in := range cases {
		text := "[servers.a]\ncmd = " + Quote(in) + "\n"
		cfg, err := Parse("/tmp/x.toml", []byte(text))
		if err != nil {
			t.Errorf("Quote(%q) = %s, rejected: %v", in, Quote(in), err)
			continue
		}
		if got := cfg.Servers[0].Cmd; got != in {
			t.Errorf("Quote round trip: got %q, want %q", got, in)
		}
	}
}

func TestQuoteEmpty(t *testing.T) {
	if got := Quote(""); got != `""` {
		t.Errorf("Quote(\"\") = %s", got)
	}
}

func TestQuoteInvalidUTF8(t *testing.T) {
	if got, want := Quote("a\xffb"), "\"a�b\""; got != want {
		t.Errorf("Quote = %q, want %q", got, want)
	}
	text := "[servers.a]\ncmd = " + Quote("a\xffb") + "\n"
	cfg, err := Parse("/tmp/x.toml", []byte(text))
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Servers[0].Cmd; got != "a�b" {
		t.Errorf("cmd = %q", got)
	}
}

func TestRenderRoundTrip(t *testing.T) {
	off := false
	spec := Spec{
		Name: "web", Dir: "/srv/web", Cmd: "npm run dev -- --host \"0.0.0.0\"", Port: new(3100),
		Group: "front", DependsOn: []string{"db"}, Restart: RestartOnFailure, Health: "/healthz",
		Icon: "/srv/web/icon.png", Open: "/admin", Stop: "30s", Enabled: &off,
		Env:     map[string]string{"A": "1", "with space": "x y", "Q": `"q"`},
		Comment: "line one\nline two",
	}
	text := Render(spec) + "\n[servers.db]\ncmd = \"db\"\n"
	cfg, err := Parse("/tmp/x.toml", []byte(text))
	if err != nil {
		t.Fatalf("Render output rejected: %v\n%s", err, text)
	}
	s, _ := cfg.Server("web")
	back := SpecOf(s)
	if back.Name != spec.Name || back.Dir != spec.Dir || back.Cmd != spec.Cmd || *back.Port != 3100 ||
		back.Group != "front" || len(back.DependsOn) != 1 || back.Restart != RestartOnFailure ||
		back.Health != "/healthz" || back.Icon != spec.Icon || back.Open != "/admin" || back.Stop != "30s" ||
		back.Enabled == nil || *back.Enabled || len(back.Env) != 3 || back.Env["with space"] != "x y" || back.Env["Q"] != `"q"` {
		t.Errorf("round trip lost data:\n%+v\n%+v", spec, back)
	}
	if !strings.HasPrefix(text, "# line one\n# line two\n[servers.web]\n") {
		t.Errorf("comment not rendered above the header:\n%s", text)
	}
}

func TestRenderOmitsDefaults(t *testing.T) {
	on := true
	got := Render(Spec{Name: "a", Cmd: "x", Restart: RestartOnFailure, Enabled: &on})
	if want := "[servers.a]\ncmd = \"x\"\n"; got != want {
		t.Errorf("Render = %q, want %q", got, want)
	}
	// "no" is no longer the default, so a form that chose it keeps it.
	if got := Render(Spec{Name: "a", Cmd: "x", Restart: RestartNo}); !strings.Contains(got, "restart = \"no\"\n") {
		t.Errorf("Render dropped restart = no: %q", got)
	}
}

func TestProjectMappingEdits(t *testing.T) {
	path := newConfig(t, fixture)
	sv, _ := mustLoad(t, path).Server("web")
	spec := SpecOf(sv)
	spec.Projects = []string{"demo_db", "demo-api"}
	if err := UpdateServer(path, "web", spec); err != nil {
		t.Fatal(err)
	}
	sv, _ = mustLoad(t, path).Server("web")
	if !slices.Equal(SpecOf(sv).Projects, spec.Projects) {
		t.Fatal("project mappings lost on round trip")
	}
	before := readConfig(t, path)
	for _, projects := range [][]string{{""}, {"UPPER"}, {"../other"}, {"same", "same"}} {
		spec.Projects = projects
		wantRejected(t, path, before, UpdateServer(path, "web", spec))
	}
	if !strings.Contains(before, "# web comment") || !strings.Contains(before, fixRest) {
		t.Fatal("mapping edit lost neighboring comments")
	}
}

// Toggling enabled keeps a comment on its line.
func TestSetEnabledKeepsComment(t *testing.T) {
	path := newConfig(t, "[servers.web]\ncmd = \"x\"\nenabled = true # paused for now\n")
	if err := SetEnabled(path, "web", false); err != nil {
		t.Fatal(err)
	}
	wantText(t, path, "[servers.web]\ncmd = \"x\"\nenabled = false # paused for now\n")
}

// An edit keeps the blank lines that end the file.
func TestEditKeepsTrailingBlankLines(t *testing.T) {
	path := newConfig(t, "[servers.web]\ncmd = \"old\"\n\n\n")
	if err := SetKey(path, "web", "cmd", `"new"`); err != nil {
		t.Fatal(err)
	}
	wantText(t, path, "[servers.web]\ncmd = \"new\"\n\n\n")
}

// Keys that differ only in case set the same field, so a config with both
// is refused instead of loading one of them at random.
func TestCaseDuplicateKeysRefused(t *testing.T) {
	path := newConfig(t, "[servers.web]\nCmd = \"a\"\ncmd = \"b\"\n")
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "same setting") {
		t.Fatalf("Load = %v, want a refusal", err)
	}
	// Environment variables keep their case.
	path = newConfig(t, "[servers.web]\ncmd = \"a\"\n[servers.web.env]\nPath = \"1\"\nPATH = \"2\"\n")
	if _, err := Load(path); err != nil {
		t.Fatalf("env keys differing in case: %v", err)
	}
}

// A server key written in another case is edited in place, keeping its
// spelling, instead of getting a second key that the config would refuse.
func TestSetKeyMatchesKeyCase(t *testing.T) {
	path := newConfig(t, "[servers.web]\ncmd = \"x\"\nEnabled = true # note\n")
	if err := SetEnabled(path, "web", false); err != nil {
		t.Fatal(err)
	}
	wantText(t, path, "[servers.web]\ncmd = \"x\"\nEnabled = false # note\n")
	if web, _ := mustLoad(t, path).Server("web"); web.Enabled {
		t.Error("web is still enabled")
	}
}

// A task's default is "no", so choosing on-failure for it in the form is written, and the
// edit round trip of a task with no restart key writes nothing.
func TestUpdateTaskRestart(t *testing.T) {
	path := newConfig(t, "[servers.migrate]\ncmd = \"x\"\noneshot = true\nport = 0\n")
	if err := UpdateServer(path, "migrate", Spec{Name: "migrate", Cmd: "x", Oneshot: true, Port: new(0), Restart: RestartNo}); err != nil {
		t.Fatal(err)
	}
	if got := readConfig(t, path); strings.Contains(got, "restart") {
		t.Errorf("unchanged task gained a restart key:\n%s", got)
	}
	if err := UpdateServer(path, "migrate", Spec{Name: "migrate", Cmd: "x", Oneshot: true, Port: new(0), Restart: RestartOnFailure}); err != nil {
		t.Fatal(err)
	}
	if got := readConfig(t, path); !strings.Contains(got, `restart = "on-failure"`) {
		t.Errorf("task's on-failure was not written:\n%s", got)
	}
}
