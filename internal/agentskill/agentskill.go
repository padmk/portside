// Package agentskill renders the agent skill that teaches coding agents to
// work with the daemon, and installs it where Claude Code and Codex look for
// skills (dirs.go locates their directories). An installed copy is refreshed when the daemon starts, so it
// follows the binary that serves it.
package agentskill

import (
	_ "embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/padmk/portside/internal/app"
	"github.com/padmk/portside/internal/paths"
)

//go:embed skill.md
var source string

var tmpl = template.Must(template.New("skill").Parse(source))

// marker identifies a file this package wrote, so user-written skills are
// never overwritten.
const marker = "<!-- Installed by `" + app.ID + " skill install`"

// Render returns the skill for a daemon on port with its config at cfgPath.
func Render(port int, cfgPath string) string {
	var b strings.Builder
	_ = tmpl.Execute(&b, map[string]any{
		"Name":   app.Name,
		"ID":     app.ID,
		"Port":   port,
		"Config": paths.ShortenHome(cfgPath),
	})
	return b.String()
}

// Target is the skill file one agent reads. Agent is "claude" or "codex".
type Target struct {
	Agent string
	Path  string
}

// Targets are the paths of AgentTargets.
func Targets() []string {
	var out []string
	for _, t := range AgentTargets() {
		out = append(out, t.Path)
	}
	return out
}

// AgentTargets are the skill files of the agents installed for this
// account: Claude Code (<config dir>/skills) and Codex (~/.agents/skills,
// or <CODEX_HOME>/skills where an earlier install put the skill, which
// Codex still reads). Directories linked to one another count once, for
// the first agent.
func AgentTargets() []Target {
	d, err := Locate()
	if err != nil {
		return nil
	}
	var out []Target
	seen := map[string]bool{}
	add := func(agent, dir string) {
		real := dir
		if r, err := filepath.EvalSymlinks(dir); err == nil {
			real = r
		}
		if seen[real] {
			return
		}
		seen[real] = true
		out = append(out, Target{Agent: agent, Path: filepath.Join(dir, app.ID, "SKILL.md")})
	}
	if exists(d.Claude) {
		add("claude", filepath.Join(d.Claude, "skills"))
	}
	if exists(d.Codex) {
		dir := filepath.Join(d.Home, ".agents", "skills")
		if legacy := filepath.Join(d.Codex, "skills"); exists(filepath.Join(legacy, app.ID, "SKILL.md")) {
			dir = legacy
		}
		add("codex", dir)
	}
	return out
}

// ErrForeign means a skill with the same name exists that this package did
// not write.
var ErrForeign = errors.New("a skill with this name exists and was not installed by " + app.Name)

// Install writes the skill to path. An existing file is replaced only if
// this package wrote it, unless force is set; a symlinked file is updated
// where it points. It reports whether the file changed.
func Install(path, content string, force bool) (bool, error) {
	if real, err := filepath.EvalSymlinks(path); err == nil {
		path = real
	}
	old, err := os.ReadFile(path)
	switch {
	case err == nil && string(old) == content:
		return false, nil
	case err == nil && !force && !strings.Contains(string(old), marker):
		return false, fmt.Errorf("%s: %w (use --force to replace it)", paths.ShortenHome(path), ErrForeign)
	case err != nil && !errors.Is(err, os.ErrNotExist):
		return false, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, err
	}
	// A fresh temporary file per write: a fixed name could be a planted
	// symlink or collide with a concurrent refresh.
	f, err := os.CreateTemp(filepath.Dir(path), ".SKILL-*.md")
	if err != nil {
		return false, err
	}
	_, werr := f.WriteString(content)
	cerr := f.Close()
	if err := errors.Join(werr, cerr, os.Chmod(f.Name(), 0o644)); err != nil {
		_ = os.Remove(f.Name())
		return false, err
	}
	if err := os.Rename(f.Name(), path); err != nil {
		_ = os.Remove(f.Name())
		return false, err
	}
	return true, nil
}

// Installed states of a skill file.
const (
	Missing  = "missing"
	Current  = "current"
	Outdated = "outdated"
	Foreign  = "foreign" // a file with this name this package did not write
)

// State tells whether path holds content.
func State(path, content string) string {
	old, err := os.ReadFile(path)
	switch {
	case err != nil:
		return Missing
	case string(old) == content:
		return Current
	case strings.Contains(string(old), marker):
		return Outdated
	}
	return Foreign
}

// Refresh rewrites installed copies that are out of date. Agents without
// the skill installed are left alone.
func Refresh(port int, cfgPath string) error {
	content := Render(port, cfgPath)
	var errs []error
	for _, path := range Targets() {
		old, err := os.ReadFile(path)
		if err != nil || !strings.Contains(string(old), marker) {
			continue
		}
		if _, err := Install(path, content, false); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
