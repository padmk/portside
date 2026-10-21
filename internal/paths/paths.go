// Package paths resolves where configuration, state and logs live.
//
// The same XDG-style layout is used on macOS and Linux so that the config file
// is easy to find and edit by hand: ~/.config/<id>/ and ~/.local/state/<id>/.
// On Linux XDG_CONFIG_HOME and XDG_STATE_HOME move them. On macOS they do not:
// the menu bar app and launchd never see a shell's XDG variables, so honoring
// them there would give the CLI another config, state directory, token and
// lock than the app. Earlier versions did honor them on macOS; MoveLegacy
// moves what they left there.
package paths

import (
	"os"
	"path/filepath"
	"runtime"
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

// ConfigLockName is the file next to the config file named config that
// serialises edits to it; it stays after an edit.
func ConfigLockName(config string) string {
	return "." + filepath.Base(config) + ".lock"
}

// ConfigFile returns the path of the main configuration file.
func ConfigFile() string {
	if p := os.Getenv(app.Env("CONFIG")); p != "" {
		return ExpandHome(p)
	}
	return DefaultConfigFile()
}

// DefaultConfigFile is the config file used unless the config path is overridden.
func DefaultConfigFile() string { return filepath.Join(defaultConfigDir(), app.ID+".toml") }

// defaultConfigDir is the product's own config directory, used unless the
// config path is overridden.
func defaultConfigDir() string {
	return filepath.Join(xdgBase("XDG_CONFIG_HOME", ".config"), app.ID)
}

// xdgBase is the XDG base directory named by env, or ~/<fallback>. Only Linux
// reads the variable (see the package comment).
func xdgBase(env, fallback string) string {
	if runtime.GOOS != "darwin" {
		if base := os.Getenv(env); base != "" {
			return base
		}
	}
	return filepath.Join(home(), fallback)
}

// StateDir returns the directory for runtime state, logs and the lock file.
func StateDir() string {
	if p := os.Getenv(app.Env("STATE_DIR")); p != "" {
		return ExpandHome(p)
	}
	return defaultStateDir()
}

// defaultStateDir is the product's own state directory, used unless the state
// path is overridden.
func defaultStateDir() string {
	return filepath.Join(xdgBase("XDG_STATE_HOME", filepath.Join(".local", "state")), app.ID)
}

// Default reports whether cfgPath and stateDir are the default config file
// and state directory, as for the account's main daemon rather than an
// isolated instance.
func Default(cfgPath, stateDir string) bool {
	return SamePath(ExpandHome(cfgPath), DefaultConfigFile()) && SamePath(stateDir, defaultStateDir())
}

// Files in the state directory that the daemon writes and its clients read.
const (
	TokenName         = "auth-token"     // the API token
	DashboardHostName = "dashboard-host" // the dashboard's host name
	PortName          = "daemon.port"    // the port a running daemon serves
	LockName          = "daemon.lock"    // held by the running daemon
)

// LogDir returns the directory in stateDir holding one log file per server.
func LogDir(stateDir string) string { return filepath.Join(stateDir, "logs") }

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
