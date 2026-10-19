package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/padmk/portside/internal/app"
	"github.com/padmk/portside/internal/paths"
)

// StarterRoot picks the directory most likely to hold the user's projects.
func StarterRoot() string {
	for _, d := range []string{"~/dev", "~/code", "~/Developer", "~/projects", "~/src"} {
		if st, err := os.Stat(paths.ExpandHome(d)); err == nil && st.IsDir() {
			return d
		}
	}
	return "~"
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

# Command used by "Open in editor"; defaults to the first of cursor, code, zed, subl.
# editor = "code"

# App name (macOS) or command used by "Open in Terminal". A command receives
# the directory as its last argument, or substitutes {DIR} in its arguments.
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
