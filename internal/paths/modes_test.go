package paths

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/padmk/portside/internal/app"
)

func writeMode(t *testing.T, path string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, nil, mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

func wantMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	st, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := st.Mode().Perm(); got != want {
		t.Errorf("%s: mode %o, want %o", path, got, want)
	}
}

func TestRestrictDefaultLocations(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(root, "state"))
	cfgDir, stateDir := defaultConfigDir(), defaultStateDir()
	cfg := filepath.Join(cfgDir, app.ID+".toml")
	writeMode(t, cfg, 0o644)
	writeMode(t, filepath.Join(cfgDir, "notes.txt"), 0o644)
	writeMode(t, filepath.Join(stateDir, "other", "file"), 0o644)
	if err := os.Chmod(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}

	Restrict(cfg, stateDir)

	wantMode(t, cfg, 0o600)
	wantMode(t, filepath.Join(cfgDir, "notes.txt"), 0o600)
	wantMode(t, cfgDir, 0o700)
	wantMode(t, filepath.Join(stateDir, "other", "file"), 0o600)
	wantMode(t, filepath.Join(root, "config"), 0o755)
}

func TestRestrictCustomLocations(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(root, "state"))
	// A config inside a repository named like the product.
	repo := filepath.Join(root, app.ID)
	cfg := filepath.Join(repo, app.ID+".toml")
	writeMode(t, cfg, 0o644)
	writeMode(t, filepath.Join(repo, "main.go"), 0o644)
	writeMode(t, filepath.Join(repo, "sub", "file"), 0o644)
	if err := os.Chmod(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	stateDir := filepath.Join(root, "custom-state")
	writeMode(t, filepath.Join(stateDir, "state.json"), 0o644)
	writeMode(t, filepath.Join(stateDir, "logs", "web.log"), 0o644)
	writeMode(t, filepath.Join(stateDir, "unrelated.txt"), 0o644)
	writeMode(t, filepath.Join(stateDir, "sub", "file"), 0o644)

	Restrict(cfg, stateDir)

	wantMode(t, cfg, 0o600)
	wantMode(t, filepath.Join(repo, "main.go"), 0o644)
	wantMode(t, filepath.Join(repo, "sub", "file"), 0o644)
	wantMode(t, repo, 0o755)
	wantMode(t, stateDir, 0o700)
	wantMode(t, filepath.Join(stateDir, "state.json"), 0o600)
	wantMode(t, filepath.Join(stateDir, "logs", "web.log"), 0o600)
	wantMode(t, filepath.Join(stateDir, "logs"), 0o700)
	wantMode(t, filepath.Join(stateDir, "unrelated.txt"), 0o644)
	wantMode(t, filepath.Join(stateDir, "sub", "file"), 0o644)
}
