package paths

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"syscall"

	"github.com/padmk/portside/internal/app"
)

// MoveLegacy moves, on macOS, a config file and state directory that an
// earlier version put under XDG_CONFIG_HOME or XDG_STATE_HOME to the
// default locations, which macOS uses regardless of those variables (see the
// package comment). It acts only when the variable is set, points elsewhere
// than the default, the old location exists and the default one does not,
// and the location is not overridden. While a daemon still runs from the old
// state directory, or a move fails, this process uses the old locations
// instead. It returns one note per change, for the user. Commands run it
// before they read any location.
func MoveLegacy() []string {
	if runtime.GOOS != "darwin" {
		return nil
	}
	cfgFrom := legacyLocation("XDG_CONFIG_HOME", app.Env("CONFIG"), app.ID, defaultConfigDir())
	stateFrom := legacyLocation("XDG_STATE_HOME", app.Env("STATE_DIR"), app.ID, defaultStateDir())
	if cfgFrom == "" && stateFrom == "" {
		return nil
	}
	var notes []string
	if stateFrom != "" && lockHeld(filepath.Join(stateFrom, "daemon.lock")) {
		if cfgFrom != "" {
			useLegacy(app.Env("CONFIG"), filepath.Join(cfgFrom, app.ID+".toml"))
		}
		useLegacy(app.Env("STATE_DIR"), stateFrom)
		return []string{fmt.Sprintf("a daemon runs from %s, where an earlier version kept its state; using it until that daemon stops, then it moves to %s", ShortenHome(stateFrom), ShortenHome(defaultStateDir()))}
	}
	for _, m := range []struct{ from, to, env, file string }{
		{cfgFrom, defaultConfigDir(), app.Env("CONFIG"), app.ID + ".toml"},
		{stateFrom, defaultStateDir(), app.Env("STATE_DIR"), ""},
	} {
		if m.from == "" {
			continue
		}
		err := os.MkdirAll(filepath.Dir(m.to), DirMode)
		if err == nil {
			err = os.Rename(m.from, m.to)
		}
		if err != nil {
			useLegacy(m.env, filepath.Join(m.from, m.file))
			notes = append(notes, fmt.Sprintf("could not move %s to %s (%v); using it where it is", ShortenHome(m.from), ShortenHome(m.to), err))
			continue
		}
		notes = append(notes, fmt.Sprintf("moved %s to %s: on macOS the app, the CLI and the login service use one location regardless of XDG variables", ShortenHome(m.from), ShortenHome(m.to)))
	}
	return notes
}

// legacyLocation returns the product's directory under the XDG base
// directory named by env when an earlier version would have used it and it
// still holds the product's files: the location is not overridden by
// override, the directory exists, and def, the location used now, differs
// and does not exist yet. Otherwise it returns "".
func legacyLocation(env, override, name, def string) string {
	base := os.Getenv(env)
	if base == "" || os.Getenv(override) != "" {
		return ""
	}
	dir := filepath.Join(ExpandHome(base), name)
	if SamePath(dir, def) {
		return ""
	}
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return ""
	}
	if _, err := os.Lstat(def); !errors.Is(err, os.ErrNotExist) {
		return ""
	}
	return dir
}

// useLegacy points this process, and the daemons it starts, at an old location.
func useLegacy(env, path string) { _ = os.Setenv(env, filepath.Clean(path)) }

// lockHeld reports whether a daemon holds the lock file at path.
func lockHeld(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_SH|syscall.LOCK_NB); err != nil {
		return errors.Is(err, syscall.EWOULDBLOCK)
	}
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return false
}
