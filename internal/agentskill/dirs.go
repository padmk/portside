package agentskill

import (
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/padmk/portside/internal/supervisor"
)

// Dirs are the configuration directories of the coding agents.
type Dirs struct {
	Home   string
	Claude string // CLAUDE_CONFIG_DIR, else ~/.claude
	Codex  string // CODEX_HOME, else ~/.codex
	Gemini string // GEMINI_CLI_HOME/.gemini, else ~/.gemini
}

// Locate resolves the agents' directories from their environment
// variables (see Getenv); a variable that is unset or not an absolute path
// leaves the default.
func Locate() (Dirs, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Dirs{}, err
	}
	dir := func(key, def string) string {
		v := Getenv(key)
		if rest, ok := strings.CutPrefix(v, "~/"); ok {
			v = filepath.Join(home, rest)
		}
		if !filepath.IsAbs(v) {
			return def
		}
		return filepath.Clean(v)
	}
	return Dirs{
		Home:   home,
		Claude: dir("CLAUDE_CONFIG_DIR", filepath.Join(home, ".claude")),
		Codex:  dir("CODEX_HOME", filepath.Join(home, ".codex")),
		Gemini: filepath.Join(dir("GEMINI_CLI_HOME", home), ".gemini"),
	}, nil
}

// Getenv returns key from this process's environment. A process started
// outside a terminal (the daemon under launchd or systemd, the app's
// helper) lacks the variables set in shell startup files, so there it
// falls back to the login shell's environment, read once.
func Getenv(key string) string {
	if v, ok := os.LookupEnv(key); ok || os.Getenv("TERM") != "" {
		return v
	}
	for _, kv := range ShellEnv() {
		if v, ok := strings.CutPrefix(kv, key+"="); ok {
			return v
		}
	}
	return ""
}

// ShellEnv is the login shell's environment, read once (nil when it could
// not be read).
func ShellEnv() []string {
	shell.once.Do(func() { shell.env = supervisor.LoginShellEnv() })
	return shell.env
}

var shell struct {
	once sync.Once
	env  []string
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
