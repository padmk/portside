package agentskill

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/padmk/portside/internal/app"
)

// isolate points HOME and the agents' directory variables at a fresh
// directory and sets TERM, so Getenv never falls back to the login shell.
func isolate(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("TERM", "dumb")
	for _, k := range []string{"CLAUDE_CONFIG_DIR", "CODEX_HOME", "GEMINI_CLI_HOME"} {
		t.Setenv(k, "")
	}
	return home
}

func mkdir(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
}

func write(t *testing.T, path, content string) {
	t.Helper()
	mkdir(t, filepath.Dir(path))
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestRenderFillsTemplateFromApp(t *testing.T) {
	home := isolate(t)
	got := Render(17123, filepath.Join(home, "cfg", app.ID+".toml"))
	for _, leftover := range []string{"{{", "}}", "<no value>"} {
		if strings.Contains(got, leftover) {
			t.Errorf("rendered skill contains template residue %q", leftover)
		}
	}
	for _, want := range []string{
		"name: " + app.ID + "\n",
		"# " + app.Name + "\n",
		"http://127.0.0.1:17123",
		"`~/cfg/" + app.ID + ".toml`",
		marker,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("rendered skill lacks %q", want)
		}
	}
}

func TestLocateHonoursAgentVariables(t *testing.T) {
	home := isolate(t)
	custom := t.TempDir()
	tests := []struct {
		name                string
		claude, codex, gem  string // variable values
		wantClaude, wantCdx string
		wantGemini          string
	}{
		{"defaults", "", "", "",
			filepath.Join(home, ".claude"), filepath.Join(home, ".codex"), filepath.Join(home, ".gemini")},
		{"absolute", filepath.Join(custom, "c"), filepath.Join(custom, "x"), filepath.Join(custom, "g"),
			filepath.Join(custom, "c"), filepath.Join(custom, "x"), filepath.Join(custom, "g", ".gemini")},
		{"tilde", "~/cc", "~/cx", "~/cg",
			filepath.Join(home, "cc"), filepath.Join(home, "cx"), filepath.Join(home, "cg", ".gemini")},
		{"relative ignored", "rel/c", "rel/x", "rel/g",
			filepath.Join(home, ".claude"), filepath.Join(home, ".codex"), filepath.Join(home, ".gemini")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("CLAUDE_CONFIG_DIR", tt.claude)
			t.Setenv("CODEX_HOME", tt.codex)
			t.Setenv("GEMINI_CLI_HOME", tt.gem)
			d, err := Locate()
			if err != nil {
				t.Fatal(err)
			}
			if d.Home != home || d.Claude != tt.wantClaude || d.Codex != tt.wantCdx || d.Gemini != tt.wantGemini {
				t.Errorf("Locate = %+v, want home %s claude %s codex %s gemini %s", d, home, tt.wantClaude, tt.wantCdx, tt.wantGemini)
			}
		})
	}
}

func TestAgentTargets(t *testing.T) {
	skill := filepath.Join(app.ID, "SKILL.md")
	tests := []struct {
		name  string
		setup func(t *testing.T, home string)
		want  func(home string) []Target
	}{
		{"no agents", func(*testing.T, string) {}, func(string) []Target { return nil }},
		{"claude default dir", func(t *testing.T, home string) { mkdir(t, filepath.Join(home, ".claude")) },
			func(home string) []Target {
				return []Target{{"claude", filepath.Join(home, ".claude", "skills", skill)}}
			}},
		{"claude config dir", func(t *testing.T, home string) {
			mkdir(t, filepath.Join(home, ".claude")) // ignored: the variable wins
			mkdir(t, filepath.Join(home, "cfg-claude"))
			t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, "cfg-claude"))
		}, func(home string) []Target {
			return []Target{{"claude", filepath.Join(home, "cfg-claude", "skills", skill)}}
		}},
		{"codex gets agents skills", func(t *testing.T, home string) { mkdir(t, filepath.Join(home, ".codex")) },
			func(home string) []Target {
				return []Target{{"codex", filepath.Join(home, ".agents", "skills", skill)}}
			}},
		{"codex home without earlier copy", func(t *testing.T, home string) {
			mkdir(t, filepath.Join(home, "cx", "skills", "other"))
			t.Setenv("CODEX_HOME", filepath.Join(home, "cx"))
		}, func(home string) []Target {
			return []Target{{"codex", filepath.Join(home, ".agents", "skills", skill)}}
		}},
		{"codex home with earlier copy", func(t *testing.T, home string) {
			write(t, filepath.Join(home, "cx", "skills", skill), "old")
			t.Setenv("CODEX_HOME", filepath.Join(home, "cx"))
		}, func(home string) []Target {
			return []Target{{"codex", filepath.Join(home, "cx", "skills", skill)}}
		}},
		{"linked skill dirs count once", func(t *testing.T, home string) {
			mkdir(t, filepath.Join(home, ".claude"))
			mkdir(t, filepath.Join(home, ".codex"))
			mkdir(t, filepath.Join(home, ".agents", "skills"))
			if err := os.Symlink(filepath.Join(home, ".agents", "skills"), filepath.Join(home, ".claude", "skills")); err != nil {
				t.Fatal(err)
			}
		}, func(home string) []Target {
			return []Target{{"claude", filepath.Join(home, ".claude", "skills", skill)}}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := isolate(t)
			tt.setup(t, home)
			got := AgentTargets()
			want := tt.want(home)
			if len(got) != len(want) {
				t.Fatalf("AgentTargets = %v, want %v", got, want)
			}
			for i := range want {
				if got[i] != want[i] {
					t.Errorf("target %d = %v, want %v", i, got[i], want[i])
				}
			}
		})
	}
}

func TestInstallAndState(t *testing.T) {
	const content = "new " + marker + " -->\n"
	ownOld := "old " + marker + " -->\n"
	tests := []struct {
		name        string
		existing    *string // nil: no file
		force       bool
		wantChanged bool
		wantForeign bool
		wantState   string // before Install
		wantContent string
	}{
		{"missing is written", nil, false, true, false, Missing, content},
		{"current is left", ptr(content), false, false, false, Current, content},
		{"own outdated copy is refreshed", ptr(ownOld), false, true, false, Outdated, content},
		{"foreign file is kept", ptr("my own skill\n"), false, false, true, Foreign, "my own skill\n"},
		{"foreign file with force is replaced", ptr("my own skill\n"), true, true, false, Foreign, content},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), app.ID, "SKILL.md")
			if tt.existing != nil {
				write(t, path, *tt.existing)
			}
			if got := State(path, content); got != tt.wantState {
				t.Errorf("State = %s, want %s", got, tt.wantState)
			}
			changed, err := Install(path, content, tt.force)
			if tt.wantForeign != errors.Is(err, ErrForeign) || (!tt.wantForeign && err != nil) {
				t.Fatalf("Install error = %v, want foreign %v", err, tt.wantForeign)
			}
			if changed != tt.wantChanged {
				t.Errorf("changed = %v, want %v", changed, tt.wantChanged)
			}
			if got := read(t, path); got != tt.wantContent {
				t.Errorf("file = %q, want %q", got, tt.wantContent)
			}
			if !tt.wantForeign {
				if got := State(path, content); got != Current {
					t.Errorf("State after Install = %s, want current", got)
				}
			}
			ents, _ := os.ReadDir(filepath.Dir(path))
			if len(ents) != 1 {
				t.Errorf("directory holds %d entries, want only SKILL.md (no temporary files)", len(ents))
			}
		})
	}
}

func TestInstallUpdatesSymlinkTarget(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "real", "SKILL.md")
	write(t, real, "old "+marker+" -->\n")
	link := filepath.Join(dir, "link", "SKILL.md")
	mkdir(t, filepath.Dir(link))
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	if _, err := Install(link, "new "+marker+" -->\n", false); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Lstat(link); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Errorf("link replaced by a file: %v, %v", fi, err)
	}
	if got := read(t, real); !strings.HasPrefix(got, "new ") {
		t.Errorf("link target = %q, want the new content", got)
	}
}

func TestRefreshOnlyRewritesOwnCopies(t *testing.T) {
	home := isolate(t)
	mkdir(t, filepath.Join(home, ".claude"))
	mkdir(t, filepath.Join(home, ".codex"))
	cfg := filepath.Join(home, "portside.toml")
	claude := filepath.Join(home, ".claude", "skills", app.ID, "SKILL.md")
	codex := filepath.Join(home, ".agents", "skills", app.ID, "SKILL.md")
	write(t, claude, "outdated "+marker+" -->\n")
	write(t, codex, "a user's skill\n")
	if err := Refresh(7123, cfg); err != nil {
		t.Fatal(err)
	}
	if got, want := read(t, claude), Render(7123, cfg); got != want {
		t.Errorf("own outdated copy not refreshed")
	}
	if got := read(t, codex); got != "a user's skill\n" {
		t.Errorf("foreign skill rewritten: %q", got)
	}

	// An agent without the skill installed gets none.
	home = isolate(t)
	mkdir(t, filepath.Join(home, ".claude"))
	if err := Refresh(7123, cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude", "skills")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("Refresh installed a skill that was not there: %v", err)
	}
}

func ptr(s string) *string { return &s }
