package paths

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"syscall"

	"github.com/padmk/portside/internal/app"
)

// MoveLegacy moves, on macOS, a config file and state directory that an
// earlier version put under XDG_CONFIG_HOME or XDG_STATE_HOME to the
// default locations, which macOS uses regardless of those variables (see the
// package comment). It acts only when the variable is set, points elsewhere
// than the default, the old location holds the product's files and nothing
// else (see legacyLocation), the default one does not exist, and the
// location is not overridden. While a daemon still runs from the old state
// directory, the login service names an old location, or a move fails, this
// process uses the old locations instead. It returns one note per change,
// for the user. Commands run it before they read any location.
func MoveLegacy() []string { return moveLegacy(false) }

// MoveLegacyForService is MoveLegacy for `service install`, run once any
// daemon has stopped: it moves the old locations even when the login service
// names them, because the service is written anew with the default ones
// next. It returns the config file to use instead of cfgPath, and the notes.
func MoveLegacyForService(cfgPath string) (string, []string) {
	if runtime.GOOS != "darwin" || len(legacyEnv) == 0 {
		return cfgPath, nil
	}
	legacyCfg := ""
	for _, env := range legacyEnv {
		if env == app.Env("CONFIG") {
			legacyCfg = os.Getenv(env)
		}
		_ = os.Unsetenv(env)
	}
	legacyEnv = nil
	notes := moveLegacy(true)
	if legacyCfg != "" && filepath.Clean(cfgPath) == legacyCfg {
		cfgPath = ConfigFile()
	}
	return cfgPath, notes
}

// legacyEnv lists the location variables this process set to old locations.
var legacyEnv []string

func moveLegacy(forService bool) []string {
	if runtime.GOOS != "darwin" {
		return nil
	}
	cfgFrom, cfgNote := legacyLocation("XDG_CONFIG_HOME", app.Env("CONFIG"), defaultConfigDir(), legacyConfigEntry)
	stateFrom, stateNote := legacyLocation("XDG_STATE_HOME", app.Env("STATE_DIR"), defaultStateDir(), legacyStateEntry)
	var notes []string
	for _, n := range []string{cfgNote, stateNote} {
		if n != "" {
			notes = append(notes, n)
		}
	}
	if cfgFrom == "" && stateFrom == "" {
		return notes
	}
	cfgFile := ""
	if cfgFrom != "" {
		cfgFile = filepath.Join(cfgFrom, app.ID+".toml")
	}
	useOld := func() {
		if cfgFrom != "" {
			useLegacy(app.Env("CONFIG"), cfgFile)
		}
		if stateFrom != "" {
			useLegacy(app.Env("STATE_DIR"), stateFrom)
		}
	}
	if stateFrom != "" && lockHeld(filepath.Join(stateFrom, LockName)) {
		useOld()
		return append(notes, fmt.Sprintf("a daemon runs from %s, where an earlier version kept its state; using it until that daemon stops, then it moves to %s", ShortenHome(stateFrom), ShortenHome(defaultStateDir())))
	}
	if !forService && serviceNames(cfgFile, stateFrom) {
		// launchd would start its next daemon on the emptied old locations.
		useOld()
		return append(notes, fmt.Sprintf("the login service uses the locations an earlier version kept under XDG variables, so they stay in use; `%s service install` moves them to %s and %s and updates the service", app.ID, ShortenHome(defaultConfigDir()), ShortenHome(defaultStateDir())))
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

// legacyMarkers are the files of which at least one must be present for a
// directory under the XDG base named by the key to be the product's own.
var legacyMarkers = map[string][]string{
	"XDG_CONFIG_HOME": {app.ID + ".toml"},
	"XDG_STATE_HOME":  {LockName, "state.json"},
}

// legacyConfigEntry reports whether name is a file the product keeps in its
// config directory: the config and leftovers of an interrupted edit.
func legacyConfigEntry(name string) bool {
	return name == app.ID+".toml" || strings.HasPrefix(name, ".tmp-") || name == ".DS_Store"
}

// legacyStateEntry is legacyConfigEntry for the state directory, counting
// temporary files left by WriteFile or by the fixed names earlier versions used.
func legacyStateEntry(name string) bool {
	if target, ok := tempTarget(name); ok {
		name = target
	}
	name = strings.TrimSuffix(name, tempSuffix)
	return name == "logs" || name == ".DS_Store" || slices.Contains(stateEntries, name)
}

// legacyLocation returns the product's directory under the XDG base
// directory named by env when an earlier version would have used it and it
// still holds the product's files: the location is not overridden by
// override, def, the location used now, differs and does not exist yet, the
// directory holds one of the env's legacyMarkers, and every entry in it is
// one the product writes (known). A directory holding other files too, such
// as a repository named like the product, is left alone, with a note when
// it has the markers.
func legacyLocation(env, override, def string, known func(string) bool) (string, string) {
	base := os.Getenv(env)
	if base == "" || os.Getenv(override) != "" {
		return "", ""
	}
	dir := filepath.Join(ExpandHome(base), app.ID)
	if SamePath(dir, def) {
		return "", ""
	}
	if st, err := os.Lstat(dir); err != nil || !st.IsDir() {
		return "", ""
	}
	if _, err := os.Lstat(def); !errors.Is(err, os.ErrNotExist) {
		return "", ""
	}
	marked := false
	for _, name := range legacyMarkers[env] {
		if st, err := os.Lstat(filepath.Join(dir, name)); err == nil && st.Mode().IsRegular() {
			marked = true
		}
	}
	if !marked {
		return "", ""
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", ""
	}
	var other []string
	for _, e := range entries {
		if !known(e.Name()) {
			other = append(other, e.Name())
		}
	}
	if len(other) > 0 {
		if len(other) > 3 {
			other = append(other[:3], "...")
		}
		return "", fmt.Sprintf("left %s, where an earlier version may have kept its files, because it holds other files too (%s); move %s's own files to %s yourself if they are needed", ShortenHome(dir), strings.Join(other, ", "), app.Name, ShortenHome(def))
	}
	return dir, ""
}

// serviceNames reports whether the installed launchd agent names cfgFile or
// stateDir (either may be empty).
func serviceNames(cfgFile, stateDir string) bool {
	data, err := os.ReadFile(filepath.Join(home(), "Library", "LaunchAgents", app.ServiceLabel+".plist"))
	if err != nil {
		return false
	}
	esc := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")
	for _, p := range []string{cfgFile, stateDir} {
		if p != "" && strings.Contains(string(data), "<string>"+esc.Replace(filepath.Clean(p))+"</string>") {
			return true
		}
	}
	return false
}

// useLegacy points this process, and the daemons it starts, at an old location.
func useLegacy(env, path string) {
	_ = os.Setenv(env, filepath.Clean(path))
	legacyEnv = append(legacyEnv, env)
}

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
