package agentsetup

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/padmk/portside/internal/agentskill"
	"github.com/padmk/portside/internal/app"
)

// fakeCLI is a shell script standing in for an agent's CLI: it appends its
// name and arguments to $FAKE_LOG and the agents' directory variables to
// $FAKE_ENV_LOG, then exits with code.
func fakeCLI(t *testing.T, bin, name string, code int) {
	t.Helper()
	script := `#!/bin/sh
{ printf '%s' "${0##*/}"; for a in "$@"; do printf ' %s' "$a"; done; printf '\n'; } >> "$FAKE_LOG"
printf '%s CLAUDE_CONFIG_DIR=%s CODEX_HOME=%s\n' "${0##*/}" "$CLAUDE_CONFIG_DIR" "$CODEX_HOME" >> "$FAKE_ENV_LOG"
`
	if code != 0 {
		script += "echo 'fake failure' >&2\nexit " + strconv.Itoa(code) + "\n"
	}
	if err := os.WriteFile(filepath.Join(bin, name), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

type sandbox struct {
	home, bin, log, envLog string
	exe                    string // the stable link to this binary on PATH
}

// newSandbox isolates HOME, PATH and the agents' directory variables, puts
// fake agent CLIs and a link to this test binary (as the product's command)
// on PATH, and sets TERM so nothing falls back to the login shell: with
// every CLI and the product's command on PATH, the login shell is never
// read.
func newSandbox(t *testing.T, clis map[string]int) sandbox {
	t.Helper()
	root := t.TempDir()
	s := sandbox{
		home:   filepath.Join(root, "home"),
		bin:    filepath.Join(root, "bin"),
		log:    filepath.Join(root, "argv.log"),
		envLog: filepath.Join(root, "env.log"),
	}
	for _, d := range []string{s.home, s.bin} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", s.home)
	t.Setenv("PATH", s.bin)
	t.Setenv("TERM", "dumb")
	t.Setenv("FAKE_LOG", s.log)
	t.Setenv("FAKE_ENV_LOG", s.envLog)
	for _, k := range []string{"CLAUDE_CONFIG_DIR", "CODEX_HOME"} {
		t.Setenv(k, "")
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	s.exe = filepath.Join(s.bin, app.ID)
	if err := os.Symlink(self, s.exe); err != nil {
		t.Fatal(err)
	}
	for name, code := range clis {
		fakeCLI(t, s.bin, name, code)
	}
	return s
}

func (s sandbox) mkdir(t *testing.T, rel ...string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(append([]string{s.home}, rel...)...), 0o755); err != nil {
		t.Fatal(err)
	}
}

func (s sandbox) write(t *testing.T, rel, content string) string {
	t.Helper()
	path := filepath.Join(s.home, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func lines(t *testing.T, path string) []string {
	t.Helper()
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSuffix(string(b), "\n"), "\n")
}

func byID(items []Item) map[string]Item {
	m := map[string]Item{}
	for _, it := range items {
		m[it.ID] = it
	}
	return m
}

var allCLIs = map[string]int{"claude": 0, "codex": 0}

// agentConfigs are the files the agents keep their MCP servers in; only
// their own CLIs may write them.
var agentConfigs = []string{".claude.json", ".claude/.claude.json", ".codex/config.toml"}

func TestCheckStatesAndApplyRunsAgentCLIs(t *testing.T) {
	s := newSandbox(t, allCLIs)
	s.mkdir(t, ".claude")
	s.mkdir(t, ".codex")
	cfg := filepath.Join(s.home, "cfg.toml")

	items := Check(7123, cfg)
	want := map[string]struct {
		state   State
		command string
	}{
		"claude-mcp":   {Missing, "claude mcp add --scope user " + app.ID + " -- " + s.exe + " mcp"},
		"codex-mcp":    {Missing, "codex mcp add " + app.ID + " -- " + s.exe + " mcp"},
		"claude-skill": {Missing, app.ID + " skill install"},
		"codex-skill":  {Missing, app.ID + " skill install"},
	}
	got := byID(items)
	if len(got) != len(want) {
		t.Fatalf("items %v, want %d", items, len(want))
	}
	for id, w := range want {
		it := got[id]
		if it.State != w.state || !it.Applicable || it.Command != w.command || !it.Fixable() {
			t.Errorf("%s = %+v, want state %s, applicable, fixable, command %q", id, it, w.state, w.command)
		}
	}

	results := Apply(context.Background(), items)
	if Failed(results) {
		t.Fatalf("Apply failed: %+v", results)
	}
	for _, r := range results {
		if !r.OK || !r.Changed || r.State != OK {
			t.Errorf("result %+v, want ok and changed", r)
		}
	}
	wantArgv := []string{
		"claude mcp add --scope user " + app.ID + " -- " + s.exe + " mcp",
		"codex mcp add " + app.ID + " -- " + s.exe + " mcp",
	}
	if argv := lines(t, s.log); strings.Join(argv, "\n") != strings.Join(wantArgv, "\n") {
		t.Errorf("agent CLIs ran\n%s\nwant\n%s", strings.Join(argv, "\n"), strings.Join(wantArgv, "\n"))
	}
	// The MCP servers were registered only through the CLIs.
	for _, rel := range agentConfigs {
		if _, err := os.Stat(filepath.Join(s.home, rel)); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s was written by setup itself (%v)", rel, err)
		}
	}
	content := agentskill.Render(7123, cfg)
	for _, rel := range []string{".claude/skills", ".agents/skills"} {
		path := filepath.Join(s.home, rel, app.ID, "SKILL.md")
		if agentskill.State(path, content) != agentskill.Current {
			t.Errorf("%s not installed", path)
		}
	}

	// The fake CLIs wrote nothing, so the MCP items are still missing;
	// once the agents list the server, they are ok.
	got = byID(Check(7123, cfg))
	if got["claude-mcp"].State != Missing || got["claude-skill"].State != OK || got["codex-skill"].State != OK {
		t.Errorf("after Apply: %+v", got)
	}
	s.write(t, ".claude.json", `{"mcpServers":{"`+app.ID+`":{"command":"x"}}}`)
	s.write(t, ".codex/config.toml", "[mcp_servers."+app.ID+"]\ncommand = \"x\"\n")
	for id, it := range byID(Check(7123, cfg)) {
		if it.State != OK {
			t.Errorf("%s = %s with the server registered, want ok", id, it.State)
		}
	}
}

func TestSkillStates(t *testing.T) {
	cfg := "/cfg.toml"
	tests := []struct {
		name      string
		existing  string // "" leaves no file
		wantState State
		fixable   bool
		command   string
	}{
		{"missing", "", Missing, true, app.ID + " skill install"},
		{"outdated own copy", "old <!-- Installed by `" + app.ID + " skill install` -->\n", Outdated, true, app.ID + " skill install"},
		{"foreign file", "a user's skill\n", Foreign, false, app.ID + " skill install --force"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newSandbox(t, allCLIs)
			s.mkdir(t, ".claude")
			path := filepath.Join(s.home, ".claude", "skills", app.ID, "SKILL.md")
			if tt.existing != "" {
				s.write(t, ".claude/skills/"+app.ID+"/SKILL.md", tt.existing)
			}
			it := byID(Check(7123, cfg))["claude-skill"]
			if it.State != tt.wantState || it.Fixable() != tt.fixable || it.Command != tt.command {
				t.Fatalf("claude-skill = %+v, want %s fixable %v command %q", it, tt.wantState, tt.fixable, tt.command)
			}
			r := Apply(context.Background(), []Item{it})[0]
			if r.OK != tt.fixable {
				t.Errorf("Apply = %+v, want ok %v", r, tt.fixable)
			}
			b, _ := os.ReadFile(path)
			if tt.fixable && string(b) != agentskill.Render(7123, cfg) {
				t.Errorf("skill not written")
			}
			if !tt.fixable {
				if string(b) != tt.existing {
					t.Errorf("foreign skill replaced: %q", b)
				}
				if r.Command != tt.command || r.Error == "" {
					t.Errorf("failed result %+v lacks the manual command or reason", r)
				}
			}
		})
	}
}

func TestMCPItemWithoutCLI(t *testing.T) {
	s := newSandbox(t, nil)
	// The login shell is never read: its PATH could hold the real CLIs.
	noShell := &tools{}
	noShell.once.Do(func() {})
	dirs, err := agentskill.Locate()
	if err != nil {
		t.Fatal(err)
	}
	claude := agents[0]

	it := mcpItem(claude, dirs, s.exe, noShell)
	if it.Applicable || it.State != Unavailable {
		t.Errorf("no CLI, no directory: %+v, want not applicable and unavailable", it)
	}
	if r := Apply(context.Background(), []Item{it})[0]; r.OK || r.Error == "" {
		t.Errorf("Apply of an item for an agent that is not installed: %+v", r)
	}

	s.mkdir(t, ".claude")
	it = mcpItem(claude, dirs, s.exe, noShell)
	if !it.Applicable || it.State != Unavailable || it.Fixable() {
		t.Errorf("directory without CLI: %+v, want applicable, unavailable, not fixable", it)
	}
	r := Apply(context.Background(), []Item{it})[0]
	if r.OK || r.Command != "claude mcp add --scope user "+app.ID+" -- "+s.exe+" mcp" {
		t.Errorf("Apply = %+v, want a failure with the manual command", r)
	}
	if argv := lines(t, s.log); len(argv) != 0 {
		t.Errorf("something ran: %v", argv)
	}
}

func TestApplyReportsCLIFailure(t *testing.T) {
	s := newSandbox(t, map[string]int{"claude": 0, "codex": 3})
	s.mkdir(t, ".codex")
	it := byID(Check(7123, "/cfg.toml"))["codex-mcp"]
	r := Apply(context.Background(), []Item{it})[0]
	if r.OK || r.Changed || !strings.Contains(r.Error, "fake failure") || r.Command != it.Command {
		t.Errorf("Apply = %+v, want a failure with the CLI's message and the manual command", r)
	}
}

func TestApplyPassesMovedAgentDirectory(t *testing.T) {
	s := newSandbox(t, allCLIs)
	moved := filepath.Join(s.home, "elsewhere", "claude")
	s.mkdir(t, "elsewhere", "claude")
	t.Setenv("CLAUDE_CONFIG_DIR", moved)
	it := byID(Check(7123, "/cfg.toml"))["claude-mcp"]
	if r := Apply(context.Background(), []Item{it})[0]; !r.OK {
		t.Fatalf("Apply = %+v", r)
	}
	env := lines(t, s.envLog)
	if len(env) != 1 || !strings.Contains(env[0], "CLAUDE_CONFIG_DIR="+moved+" ") {
		t.Errorf("claude ran with %v, want CLAUDE_CONFIG_DIR=%s", env, moved)
	}
	// It registers in that directory's file, not the home one.
	if _, err := os.Stat(filepath.Join(s.home, ".claude.json")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("~/.claude.json written: %v", err)
	}
	s.write(t, "elsewhere/claude/.claude.json", `{"mcpServers":{"`+app.ID+`":{}}}`)
	if st := byID(Check(7123, "/cfg.toml"))["claude-mcp"].State; st != OK {
		t.Errorf("registered in CLAUDE_CONFIG_DIR: %s, want ok", st)
	}
}

func TestSelect(t *testing.T) {
	items := []Item{{ID: "a"}, {ID: "b"}, {ID: "c"}}
	got, err := Select(items, []string{"c", "a"})
	if err != nil || len(got) != 2 || got[0].ID != "c" || got[1].ID != "a" {
		t.Errorf("Select = %v, %v; want c, a", got, err)
	}
	if _, err := Select(items, []string{"a", "zz"}); err == nil || !strings.Contains(err.Error(), "zz") {
		t.Errorf("unknown id: %v", err)
	}
}

func TestShellJoin(t *testing.T) {
	tests := []struct {
		args []string
		want string
	}{
		{[]string{"claude", "mcp", "add", "--", "/usr/local/bin/x", "mcp"}, "claude mcp add -- /usr/local/bin/x mcp"},
		{[]string{"/Applications/My App/x"}, "'/Applications/My App/x'"},
		{[]string{"it's"}, `'it'\''s'`},
		{[]string{""}, "''"},
	}
	for _, tt := range tests {
		if got := shellJoin(tt.args); got != tt.want {
			t.Errorf("shellJoin(%q) = %s, want %s", tt.args, got, tt.want)
		}
	}
}
