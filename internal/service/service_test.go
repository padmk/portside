package service

import (
	"encoding/xml"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/padmk/portside/internal/app"
	"github.com/padmk/portside/internal/paths"
)

// render is the service definition for this platform.
func render(exe, configPath string) string {
	if runtime.GOOS == "darwin" {
		return launchdPlist(exe, configPath)
	}
	return systemdUnit(exe, configPath)
}

func isolate(t *testing.T) (state string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	state = filepath.Join(home, "state & <dir>")
	t.Setenv(app.Env("STATE_DIR"), state)
	t.Setenv(app.Env("PORT"), "")
	return state
}

func TestLaunchdPlistPinsLocations(t *testing.T) {
	state := isolate(t)
	exe := "/Applications/My App.app/Contents/Helpers/" + app.ID
	cfg := "/Users/me/cfg & <x>.toml"
	plist := launchdPlist(exe, cfg)

	// Well-formed XML, so launchd loads it.
	dec := xml.NewDecoder(strings.NewReader(plist))
	dec.Strict = true
	var strs []string
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("plist is not well-formed XML: %v\n%s", err, plist)
		}
		if cd, ok := tok.(xml.CharData); ok && strings.TrimSpace(string(cd)) != "" {
			strs = append(strs, string(cd))
		}
	}
	text := strings.Join(strs, "\n")
	for _, want := range []string{
		"Label\n" + app.ServiceLabel,
		"ProgramArguments\n" + exe + "\ndaemon",
		app.Env("CONFIG") + "\n" + cfg,
		app.Env("STATE_DIR") + "\n" + state,
		"StandardOutPath\n" + paths.DaemonLog(),
	} {
		if !strings.Contains(text, want) {
			t.Errorf("plist values lack %q:\n%s", want, text)
		}
	}
}

func TestSystemdUnitPinsLocations(t *testing.T) {
	state := isolate(t)
	exe := "/home/me/bin dir/" + app.ID
	cfg := `/home/me/my "cfg".toml`
	unit := systemdUnit(exe, cfg)
	for _, want := range []string{
		"ExecStart=" + strconv.Quote(exe) + " daemon\n",
		"Environment=" + app.Env("CONFIG") + "=" + strconv.Quote(cfg) + "\n",
		"Environment=" + app.Env("STATE_DIR") + "=" + strconv.Quote(state) + "\n",
		"Description=" + app.Name + " ",
		"Restart=on-failure\n",
		"WantedBy=default.target\n",
	} {
		if !strings.Contains(unit, want) {
			t.Errorf("unit lacks %q:\n%s", want, unit)
		}
	}
}

func TestFileNamedFromApp(t *testing.T) {
	isolate(t)
	home, _ := os.UserHomeDir()
	want := filepath.Join(home, ".config", "systemd", "user", app.ID+".service")
	if runtime.GOOS == "darwin" {
		want = filepath.Join(home, "Library", "LaunchAgents", app.ServiceLabel+".plist")
	}
	if got := File(); got != want {
		t.Errorf("File = %s, want %s", got, want)
	}
}

// Owns reads back what the definition pins, so clients start the service
// only when it runs the daemon they talk to.
func TestOwnsReadsPinnedLocations(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("no service definition on " + runtime.GOOS)
	}
	cfg := "/cfg dir/a & <b> \"c\".toml"
	tests := []struct {
		name  string
		setup func(t *testing.T, state string)
		cfg   string
		want  bool
	}{
		{"same config and state", func(*testing.T, string) {}, cfg, true},
		{"other config", func(*testing.T, string) {}, "/other.toml", false},
		{"other state dir", func(t *testing.T, state string) {
			t.Setenv(app.Env("STATE_DIR"), state+"-other")
		}, cfg, false},
		{"port override", func(t *testing.T, _ string) { t.Setenv(app.Env("PORT"), "7999") }, cfg, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state := isolate(t)
			file := File()
			if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(file, []byte(render("/bin/"+app.ID, cfg)), 0o644); err != nil {
				t.Fatal(err)
			}
			tt.setup(t, state)
			if got := Owns(tt.cfg); got != tt.want {
				t.Errorf("Owns(%q) = %v, want %v", tt.cfg, got, tt.want)
			}
		})
	}
}
