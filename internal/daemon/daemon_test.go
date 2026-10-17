package daemon

import (
	"path/filepath"
	"testing"

	"github.com/padmk/portside/internal/app"
)

func TestServiceSkillRefreshIsolation(t *testing.T) {
	base := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", base)
	t.Setenv(app.Env("STATE_DIR"), "")
	path := filepath.Join(base, app.ID, app.ID+".toml")
	t.Setenv(app.Env("CONFIG"), path)
	if !refreshInstalledSkill(path) {
		t.Fatal("a service using the default config must refresh the installed skill")
	}
	if refreshInstalledSkill(filepath.Join(base, "isolated.toml")) {
		t.Fatal("a custom config must not refresh the installed skill")
	}
	t.Setenv(app.Env("STATE_DIR"), filepath.Join(base, "state"))
	if refreshInstalledSkill(path) {
		t.Fatal("an isolated state directory must not refresh the installed skill")
	}
}
