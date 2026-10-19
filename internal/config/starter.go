package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/padmk/portside/internal/app"
	"github.com/padmk/portside/internal/paths"
)

// rootNames are folder names people and tools commonly keep projects in,
// most conventional first; they are matched case-insensitively, directly in
// the home directory and in ~/Documents (where GitHub Desktop clones).
var rootNames = []string{
	"Developer", "dev", "Development", "code", "projects", "src", "source",
	"repos", "git", "GitHub", "workspace", "work", "programming", "programs",
	"coding", "devel", "hack", "Sites", "IdeaProjects", "WebstormProjects",
	"PycharmProjects", "GolandProjects", "AndroidStudioProjects", "StudioProjects",
}

// projectMarkers are files or folders whose presence makes a directory a project.
var projectMarkers = []string{
	".git", "package.json", "go.mod", "Cargo.toml", "pyproject.toml", "requirements.txt",
	"Gemfile", "composer.json", "pom.xml", "build.gradle", "mix.exs", "deno.json",
	"compose.yaml", "docker-compose.yml",
}

// StarterRoot picks the directory most likely to hold the user's projects:
// the candidate with the most projects one or two levels down, the more
// conventional name winning a tie, else the home directory.
func StarterRoot() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "~"
	}
	best, bestScore := "", 0
	for _, dir := range rootCandidates(home) {
		if n := countProjects(dir); n > bestScore {
			best, bestScore = dir, n
		}
	}
	if best == "" {
		return "~"
	}
	return paths.ShortenHome(best)
}

// rootCandidates lists the existing folders named like a projects folder in
// the home directory and in ~/Documents, in rootNames order.
func rootCandidates(home string) []string {
	var out []string
	seen := map[string]bool{}
	for _, parent := range []string{home, filepath.Join(home, "Documents")} {
		entries, err := os.ReadDir(parent)
		if err != nil {
			continue
		}
		byName := map[string]string{}
		for _, e := range entries {
			if e.IsDir() {
				byName[strings.ToLower(e.Name())] = filepath.Join(parent, e.Name())
			}
		}
		for _, name := range rootNames {
			dir, ok := byName[strings.ToLower(name)]
			if !ok {
				continue
			}
			// A folder reached through a link counts once.
			key := dir
			if real, err := filepath.EvalSymlinks(dir); err == nil {
				key = real
			}
			if !seen[key] {
				seen[key] = true
				out = append(out, dir)
			}
		}
	}
	return out
}

// countProjects counts projects among dir's children and grandchildren
// (for layouts such as ~/dev/<client>/<project>), looking at a bounded
// number of entries so a huge folder stays quick.
func countProjects(dir string) int {
	const maxEntries = 400
	n := 0
	children, _ := os.ReadDir(dir)
	if len(children) > maxEntries {
		children = children[:maxEntries]
	}
	for _, c := range children {
		if !c.IsDir() || strings.HasPrefix(c.Name(), ".") {
			continue
		}
		child := filepath.Join(dir, c.Name())
		if isProject(child) {
			n++
			continue
		}
		grand, _ := os.ReadDir(child)
		if len(grand) > maxEntries {
			grand = grand[:maxEntries]
		}
		for _, g := range grand {
			if g.IsDir() && !strings.HasPrefix(g.Name(), ".") && isProject(filepath.Join(child, g.Name())) {
				n++
			}
		}
	}
	return n
}

func isProject(dir string) bool {
	for _, m := range projectMarkers {
		if _, err := os.Stat(filepath.Join(dir, m)); err == nil {
			return true
		}
	}
	return false
}

// WriteStarter creates a commented configuration file with no servers.
func WriteStarter(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), paths.DirMode); err != nil {
		return err
	}
	text := fmt.Sprintf(`# %[1]s configuration. Edits are picked up automatically.
#
# Each [servers.<name>] table is one dev server. A server is reachable at
# http://127.0.0.1:<port> and at http://<name>.localhost:%[3]d via the proxy.

# Base directory for relative "dir" values.
root = %[2]s

# Port of the dashboard, API and *.localhost proxy.
# port = %[3]d

# Directories prepended to PATH for every server, e.g. to pin a Node version.
# path = ["/opt/homebrew/opt/node@24/bin"]

# Start the servers that were running when the daemon last stopped.
# restore = true

# Show a desktop notification when a server crashes.
# notify = true

# Shell that runs each server's cmd as <shell> -c <cmd>.
# shell = "bash"

# Editor used by "Open in editor": an app name (macOS), a command, or empty for
# $VISUAL or $EDITOR when it is a GUI editor, else the first one installed.
# The dashboard's Settings list the editors found.
# editor = "Visual Studio Code"

# Terminal used by "Open in Terminal": an app name (macOS) or a command. A
# command receives the directory as its last argument, or substitutes {DIR}
# in its arguments. Empty picks the first terminal installed.
# terminal = "Terminal"

# Command that starts the container engine; by default the engine behind the
# active Docker context (OrbStack, Docker Desktop, Colima, Rancher) is started.
# docker_start = "colima start"

# Let your other devices signed in to the same Tailscale account connect to
# this computer from their %[1]s dashboard and manage its servers.
# share = false

# Example server. {PORT} is replaced with the port; $PORT is set as well.
#
# [servers.web]
# dir = "my-app"                 # relative to root, or absolute
# cmd = "pnpm dev --port {PORT}"
# port = 3001                    # omit to assign one, 0 if it listens on none
# group = "my-app"               # start and stop related servers together
# depends_on = ["api"]           # started (and ready) before this one
# restart = "on-failure"         # no | on-failure | always
# health = "/"                   # HTTP path that must answer before "running"
# env = { NODE_ENV = "development" }
`, app.Name, Quote(StarterRoot()), app.DefaultPort)
	return WriteAtomic(path, []byte(text))
}
