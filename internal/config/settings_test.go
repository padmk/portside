package config

import (
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

const settingsFile = `# settings head

root = "/tmp" # projects
restore = false # keep stopped
env.KEEP = "a" # first

# web comment
[servers.web]
cmd = "npm run dev" # command
port = 3001
`

func readSettings(t *testing.T, path string) (Settings, string) {
	t.Helper()
	s, rev, err := ReadSettings(path)
	if err != nil {
		t.Fatal(err)
	}
	return s, rev
}

// Every key the dashboard edits is written, read back as written, and loads;
// comments around the keys and the server table stay.
func TestSettingsRoundTrip(t *testing.T) {
	path := newConfig(t, settingsFile)
	s, rev := readSettings(t, path)
	if s.Root != "/tmp" || s.Restore || !s.Notify || !s.LoginEnv || s.Env["KEEP"] != "a" {
		t.Fatalf("read settings = %+v", s)
	}
	want := Settings{
		Root: "/srv", Path: []string{"~/bin", "/opt/x"}, Env: map[string]string{"KEEP": "b", "NEW": "c"},
		Restore: true, Notify: false, StopTimeout: "30s", Shell: "zsh", DockerStart: "colima start",
		OnCrash: "~/bin/report", OnUnready: "~/bin/report unready", OnRecover: "~/bin/ok",
		UnreadyAfter: "2m", RetryAfter: "5m", MemoryLimit: "2GB", LoginEnv: false, Umask: "0007",
	}
	if err := UpdateSettingsIfUnchanged(path, want, rev); err != nil {
		t.Fatal(err)
	}
	got, _ := readSettings(t, path)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("read back\n%+v\nwant\n%+v", got, want)
	}
	text := readConfig(t, path)
	for _, keep := range []string{"# settings head\n", `root = "/srv" # projects`, `env.KEEP = "b" # first`, "# web comment\n[servers.web]\ncmd = \"npm run dev\" # command\nport = 3001\n"} {
		if !strings.Contains(text, keep) {
			t.Errorf("missing %q:\n%s", keep, text)
		}
	}
	if strings.Contains(text, "restore") {
		t.Errorf("restore = true is the default but stays written:\n%s", text)
	}
	cfg := mustLoad(t, path)
	if cfg.Shell != "zsh" || cfg.StopTimeout != 30*time.Second || cfg.Notify || !cfg.OwnEnv || cfg.Env["NEW"] != "c" || len(cfg.PathPrepend) != 2 {
		t.Errorf("loaded config = %+v", cfg)
	}
	if web, _ := cfg.Server("web"); web.OnCrash != "~/bin/report" || web.Umask != "0007" || web.MemoryLimit.String() != "2 GB" || web.RetryAfter != 5*time.Minute {
		t.Errorf("web inherits %+v", web)
	}
}

// Saving the settings as read changes nothing, also for values written
// in another form than the dashboard's.
func TestSettingsUnchangedKeepsText(t *testing.T) {
	text := "root = '/tmp'\nNotify = false # case\nstop_timeout = \"1m0s\"\nshell = \"bash\" # explicit default\n[env]\nA = '1'\n"
	path := newConfig(t, text)
	s, rev := readSettings(t, path)
	if err := UpdateSettingsIfUnchanged(path, s, rev); err != nil {
		t.Fatal(err)
	}
	wantText(t, path, text)
	s, rev = readSettings(t, path)
	s.Notify = true
	s.Env = map[string]string{"A": "1", "B": "2"}
	if err := UpdateSettingsIfUnchanged(path, s, rev); err != nil {
		t.Fatal(err)
	}
	wantText(t, path, "root = '/tmp'\n# case\nstop_timeout = \"1m0s\"\nshell = \"bash\" # explicit default\n[env]\nA = '1'\nB = \"2\"\n")
}

// A value equal to its default is not written, and resetting a key removes it.
func TestSettingsDefaultsNotWritten(t *testing.T) {
	path := newConfig(t, settingsFile)
	s, rev := readSettings(t, path)
	def := DefaultSettings(path)
	s.Restore, s.Env, s.Root = true, nil, ""
	s.StopTimeout, s.Shell, s.UnreadyAfter = def.StopTimeout, def.Shell, def.UnreadyAfter
	if err := UpdateSettingsIfUnchanged(path, s, rev); err != nil {
		t.Fatal(err)
	}
	wantText(t, path, "# settings head\n\n# projects\n# keep stopped\n# first\n\n# web comment\n[servers.web]\ncmd = \"npm run dev\" # command\nport = 3001\n")
}

// In the starter file a new key goes below its commented-out example.
func TestSettingsBelowExample(t *testing.T) {
	path := newConfig(t, "")
	if err := WriteStarter(path); err != nil {
		t.Fatal(err)
	}
	s, rev := readSettings(t, path)
	s.Restore, s.Path = false, []string{"/opt/bin"}
	if err := UpdateSettingsIfUnchanged(path, s, rev); err != nil {
		t.Fatal(err)
	}
	text := readConfig(t, path)
	for _, want := range []string{"# restore = true\nrestore = false\n", "# path = [\"~/.nvm/versions/node/v24.11.0/bin\"]\npath = [\"/opt/bin\"]\n"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q:\n%s", want, text)
		}
	}
	mustLoad(t, path)
}

func TestSettingsInvalidRefused(t *testing.T) {
	path := newConfig(t, settingsFile)
	for _, edit := range []func(*Settings){
		func(s *Settings) { s.UnreadyAfter = "soon" },
		func(s *Settings) { s.UnreadyAfter = "0s" },
		func(s *Settings) { s.RetryAfter = "-1m" },
		func(s *Settings) { s.StopTimeout = "10" },
		func(s *Settings) { s.MemoryLimit = "2" },
		func(s *Settings) { s.Umask = "999" },
	} {
		s, rev := readSettings(t, path)
		edit(&s)
		wantRejected(t, path, settingsFile, UpdateSettingsIfUnchanged(path, s, rev))
	}
}

func TestSettingsConflict(t *testing.T) {
	path := newConfig(t, settingsFile)
	s, rev := readSettings(t, path)
	changed := settingsFile + "\n[servers.api]\ncmd = \"go run .\"\n"
	if err := os.WriteFile(path, []byte(changed), 0o600); err != nil {
		t.Fatal(err)
	}
	s.Shell = "zsh"
	err := UpdateSettingsIfUnchanged(path, s, rev)
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("err = %v, want ErrConflict", err)
	}
	wantText(t, path, changed)
}

// Clearing a field whose default is written in the file removes the key.
func TestSettingsClearWrittenDefault(t *testing.T) {
	path := newConfig(t, "stop_timeout = \"10s\"\nshell = \"bash\"\nunready_after = \"1m\"\n\n[servers.web]\ncmd = \"x\"\n")
	s, rev := readSettings(t, path)
	s.StopTimeout, s.Shell, s.UnreadyAfter = "", "", ""
	if err := UpdateSettingsIfUnchanged(path, s, rev); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"stop_timeout", "shell", "unready_after"} {
		if text := readConfig(t, path); strings.Contains(text, k) {
			t.Errorf("%s kept after clearing:\n%s", k, text)
		}
	}
}
