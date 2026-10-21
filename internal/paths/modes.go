package paths

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

// The config can hold secrets in server environments and the logs can hold
// whatever a server prints, so both stay readable by the owner only.
const (
	// DirMode is the mode of directories created for config and state.
	DirMode os.FileMode = 0o700
	// FileMode is the mode of files written into them.
	FileMode os.FileMode = 0o600
)

// stateEntries are the files the daemon and its clients keep at the top of
// the state directory. In a state directory chosen by the user only these,
// the logs directory and the directory itself are tightened.
var stateEntries = []string{
	TokenName, "auto-port", "container-stops.json", LockName, "daemon.log",
	PortName, DashboardHostName, "handoff.json", "last-good-config.json",
	"remote-tokens.json", "remote.json", "share-devices.json", "share-target",
	"remote-instance", "state.json", "stopped",
}

// WriteFile replaces path with data through a fresh private temporary file in
// the same directory, so readers never see a partial file and a planted
// symlink or a concurrent writer cannot take over a fixed temporary name.
// The directory must exist.
func WriteFile(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+"-*.tmp")
	if err != nil {
		return err
	}
	_, werr := tmp.Write(data)
	if err := errors.Join(werr, tmp.Close()); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	return nil
}

// Restrict removes group and other access from the product's config and state.
//
// The default config and state directories belong to the product, so they are
// tightened with everything in them. A config file elsewhere is tightened on
// its own, and a state directory elsewhere is tightened itself together with
// the product's files and logs in it; other files in such directories keep
// their modes. Parent directories such as ~/.config are left alone, and
// symlinks are not followed.
func Restrict(cfgPath, stateDir string) {
	if SamePath(stateDir, defaultStateDir()) {
		restrictTree(stateDir)
	} else {
		restrict(stateDir)
		for _, name := range stateEntries {
			restrict(filepath.Join(stateDir, name))
		}
		restrictTree(LogDir(stateDir))
	}
	if dir := filepath.Dir(cfgPath); SamePath(dir, defaultConfigDir()) {
		restrictTree(dir)
	}
	restrict(cfgPath)
	// An edited config follows its symlink, so the target is tightened too.
	if target, err := filepath.EvalSymlinks(cfgPath); err == nil && target != cfgPath {
		restrict(target)
	}
}

// SamePath reports whether a and b name the same location once symlinks in
// them are resolved.
func SamePath(a, b string) bool {
	return resolve(a) == resolve(b)
}

func resolve(p string) string {
	if abs, err := filepath.Abs(p); err == nil {
		p = abs
	}
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return filepath.Clean(p)
}

// restrictTree tightens root and everything below it. A root that is a
// symlink is left alone, like any symlink inside it.
func restrictTree(root string) {
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		restrict(path)
		return nil
	})
}

// restrict clears the group and other bits of a regular file or directory.
func restrict(path string) {
	st, err := os.Lstat(path)
	if err != nil || !(st.Mode().IsRegular() || st.IsDir()) {
		return
	}
	if mode := st.Mode().Perm(); mode&0o077 != 0 {
		_ = os.Chmod(path, mode&^0o077)
	}
}
