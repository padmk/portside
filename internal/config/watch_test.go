package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWatchMatch(t *testing.T) {
	for _, c := range []struct {
		patterns []string
		path     string
		want     bool
	}{
		{[]string{"pyproject.toml"}, "pyproject.toml", true},
		{[]string{"pyproject.toml"}, "sub/pyproject.toml", false},
		{[]string{"*.py"}, "app.py", true},
		{[]string{"*.py"}, "src/app.py", false},
		{[]string{"src/*.py"}, "src/app.py", true},
		{[]string{"src/*.py"}, "src/sub/app.py", false},
		{[]string{"./src/*.py"}, "src/app.py", true},
		{[]string{"s?c/[a-c]pp.py"}, "src/app.py", true},
		// ** at the start, in the middle and at the end, also for none.
		{[]string{"**/*.py"}, "app.py", true},
		{[]string{"**/*.py"}, "a/b/c/app.py", true},
		{[]string{"src/**/*.py"}, "src/app.py", true},
		{[]string{"src/**/*.py"}, "src/a/b/app.py", true},
		{[]string{"src/**/*.py"}, "lib/app.py", false},
		{[]string{"src/**/*.py"}, "src/a/app.js", false},
		{[]string{"src/**"}, "src/a/b/app.js", true},
		{[]string{"src/**"}, "srcs/app.js", false},
		{[]string{"**"}, "README.md", true},
		{[]string{"a/**/b/**/c.txt"}, "a/x/b/y/z/c.txt", true},
		// Skipped directories only when a pattern names them.
		{[]string{"**/*.py"}, ".venv/lib/x.py", false},
		{[]string{"**/*.py"}, "pkg/__pycache__/x.py", false},
		{[]string{"**/*.js"}, "node_modules/x/index.js", false},
		{[]string{"**/*.js"}, "web/dist/app.js", false},
		{[]string{"src/**"}, "src/.git/HEAD", false},
		{[]string{"node_modules/lib/**/*.js"}, "node_modules/lib/a/index.js", true},
		{[]string{"**/build/*.txt"}, "a/build/x.txt", true},
		{[]string{"dist/*.js"}, "dist/app.js", true},
		{[]string{"**"}, "build", true}, // a file of that name
		{[]string{"*.toml", "src/**/*.py"}, "src/x.py", true},
	} {
		w, err := ParseWatch(c.patterns)
		if err != nil {
			t.Fatalf("%q: %v", c.patterns, err)
		}
		if got := w.Match(c.path); got != c.want {
			t.Errorf("%q matching %s = %v, want %v", c.patterns, c.path, got, c.want)
		}
	}
}

func TestWatchEnter(t *testing.T) {
	for _, c := range []struct {
		pattern string
		dir     string
		want    bool
	}{
		{"pyproject.toml", "src", false},
		{"*.py", "src", false},
		{"src/*.py", "src", true},
		{"src/*.py", "src/sub", false},
		{"src/*.py", "lib", false},
		{"src/**/*.py", "src/a/b", true},
		{"**/*.py", "a", true},
		{"**/*.py", "node_modules", false},
		{"**/*.py", "a/.venv", false},
		{"**/*.py", ".git", false},
		{"node_modules/x/*.js", "node_modules", true},
		{"node_modules/x/*.js", "node_modules/x", true},
		{"node_modules/x/*.js", "node_modules/y", false},
		{"src/**", "src", true},
	} {
		w, err := ParseWatch([]string{c.pattern})
		if err != nil {
			t.Fatalf("%q: %v", c.pattern, err)
		}
		if got := w.Enter(c.dir); got != c.want {
			t.Errorf("%q entering %s = %v, want %v", c.pattern, c.dir, got, c.want)
		}
	}
}

func TestParseWatchRefuses(t *testing.T) {
	for _, bad := range []string{"", "  ", "/etc/*.conf", "~/x.py", "../x.py", "src/../../x.py", "src/[a-.py", "src/**.py", "a**/x", "src/", ".", "./"} {
		if w, err := ParseWatch([]string{"ok.py", bad}); err == nil {
			t.Errorf("ParseWatch(%q) = %v, want an error", bad, w.Patterns())
		}
	}
	if w, err := ParseWatch(nil); w != nil || err != nil {
		t.Errorf("no patterns = %v, %v", w, err)
	}
}

// An invalid pattern fails the config, naming the server and the pattern.
func TestWatchConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	write := func(watch string) (*Config, error) {
		t.Helper()
		body := "[servers.api]\ncmd = \"uv run api\"\nwatch = " + watch + "\n"
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return Load(path)
	}
	cfg, err := write(`["src/**/*.py", "pyproject.toml"]`)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Servers[0].Watch.Patterns(); len(got) != 2 || got[0] != "src/**/*.py" || got[1] != "pyproject.toml" {
		t.Errorf("patterns = %q", got)
	}
	if _, err := write(`["../shared/*.py"]`); err == nil || !strings.Contains(err.Error(), "servers.api") || !strings.Contains(err.Error(), "../shared/*.py") {
		t.Errorf("error = %v", err)
	}
}
