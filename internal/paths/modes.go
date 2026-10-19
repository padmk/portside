package paths

import (
	"io/fs"
	"os"
	"path/filepath"

	"github.com/padmk/portside/internal/app"
)

// The config can hold secrets in server environments and the logs can hold
// whatever a server prints, so both stay readable by the owner only.
const (
	// DirMode is the mode of directories created for config and state.
	DirMode os.FileMode = 0o700
	// FileMode is the mode of files written into them.
	FileMode os.FileMode = 0o600
)

// Restrict removes group and other access from the state directory and
// everything in it, from the config file and, when it is the product's own
// directory, from the config directory and its contents. Parent directories
// such as ~/.config are left alone, and symlinks are not followed.
func Restrict(cfgPath, stateDir string) {
	restrictTree(stateDir)
	if dir := filepath.Dir(cfgPath); filepath.Base(dir) == app.ID {
		restrictTree(dir)
	}
	restrict(cfgPath)
	// An edited config follows its symlink, so the target is tightened too.
	if target, err := filepath.EvalSymlinks(cfgPath); err == nil && target != cfgPath {
		restrict(target)
	}
}

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
