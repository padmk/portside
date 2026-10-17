// Package paths resolves where configuration, state and logs live.
//
// The same XDG-style layout is used on macOS and Linux so that the config file
// is easy to find and edit by hand: ~/.config/<id>/ and ~/.local/state/<id>/.
package paths

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/padmk/portside/internal/app"
)

func home() string {
	h, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return h
}

// ConfigFile returns the path of the main configuration file.
func ConfigFile() string {
	if p := os.Getenv(app.Env("CONFIG")); p != "" {
		return ExpandHome(p)
	}
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		base = filepath.Join(home(), ".config")
	}
	return filepath.Join(base, app.ID, app.ID+".toml")
}

// StateDir returns the directory for runtime state, logs and the lock file.
func StateDir() string {
	if p := os.Getenv(app.Env("STATE_DIR")); p != "" {
		return ExpandHome(p)
	}
	base := os.Getenv("XDG_STATE_HOME")
	if base == "" {
		base = filepath.Join(home(), ".local", "state")
	}
	return filepath.Join(base, app.ID)
}

// LogDir returns the directory holding one log file per server.
func LogDir() string { return filepath.Join(StateDir(), "logs") }

// DaemonLog returns the daemon's own log file.
func DaemonLog() string { return filepath.Join(StateDir(), "daemon.log") }

// StopMarker returns the file a deliberate daemon stop leaves behind; while it
// exists, background clients such as the menu bar app do not start a daemon.
func StopMarker() string { return filepath.Join(StateDir(), "stopped") }

// ExpandHome replaces a leading ~ and expands $VARS.
func ExpandHome(p string) string {
	p = os.ExpandEnv(p)
	if p == "~" {
		return home()
	}
	if strings.HasPrefix(p, "~/") {
		return filepath.Join(home(), p[2:])
	}
	return p
}

// ShortenHome renders a path with the home directory replaced by ~.
func ShortenHome(p string) string {
	h := home()
	if p == h {
		return "~"
	}
	if strings.HasPrefix(p, h+string(filepath.Separator)) {
		return "~" + p[len(h):]
	}
	return p
}
