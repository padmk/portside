package supervisor

import (
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/padmk/portside/internal/config"
)

func lookPath(name, pathEnv string) string {
	if p := findInPath(name, pathEnv); p != "" {
		return p
	}
	return "/bin/sh"
}

func findInPath(name, pathEnv string) string {
	if filepath.IsAbs(name) {
		return name
	}
	for _, dir := range filepath.SplitList(pathEnv) {
		cand := filepath.Join(dir, name)
		if st, err := os.Stat(cand); err == nil && !st.IsDir() && st.Mode()&0o111 != 0 {
			return cand
		}
	}
	if p, err := exec.LookPath(name); err == nil {
		return p
	}
	return ""
}

// LookPath finds an executable on the user's login-shell PATH plus the
// configured path entries. It returns "" when nothing matches.
func (s *Supervisor) LookPath(name string) string {
	s.mu.Lock()
	prepend := s.cfg.PathPrepend
	s.mu.Unlock()
	pathEnv := lookupEnv(s.env.get(), "PATH")
	if len(prepend) > 0 {
		pathEnv = strings.Join(prepend, string(os.PathListSeparator)) + string(os.PathListSeparator) + pathEnv
	}
	return findInPath(name, pathEnv)
}

// ServerLookPath finds name the way LookPath does for a process without a
// daemon (the CLI): the caller's PATH first, then prepend and the login
// shell's PATH, read only when needed. It returns "" when nothing matches.
func ServerLookPath(name string, prepend []string) string {
	if p, err := exec.LookPath(name); err == nil {
		return p
	}
	pathEnv := strings.Join(append(slices.Clone(prepend), lookupEnv(LoginShellEnv(), "PATH")), string(os.PathListSeparator))
	return findInPath(name, pathEnv)
}

// Environ is the login-shell environment with the configured path entries,
// for helper commands the daemon runs on the user's behalf.
func (s *Supervisor) Environ() []string {
	s.mu.Lock()
	prepend := s.cfg.PathPrepend
	s.mu.Unlock()
	base := slices.Clone(s.env.get())
	if len(prepend) == 0 {
		return base
	}
	path := strings.Join(prepend, string(os.PathListSeparator)) + string(os.PathListSeparator) + lookupEnv(base, "PATH")
	return mergeEnv(base, map[string]string{"PATH": path})
}

func (s *Supervisor) buildEnv(sv config.Server, prepend []string, global map[string]string) []string {
	return launchEnv(s.env.get(), sv, prepend, global)
}

// launchInputs are the parts a server's environment was built from, kept so
// that stale checks compare the config's parts directly and ignore volatile
// values only in the login-shell part.
type launchInputs struct {
	Base   []string          `json:"base"`             // login-shell environment
	Path   []string          `json:"path,omitempty"`   // configured path entries
	Env    map[string]string `json:"env,omitempty"`    // global env
	Detect string            `json:"detect,omitempty"` // toolChoice key
}

// keptKeys are the variables a tool environment must not override: PORT and
// the config's env except PATH, sorted. The tool builds its PATH on top of
// the configured one, so setting that again would drop the tool's entries.
func keptKeys(sv config.Server, global map[string]string) []string {
	set := map[string]bool{}
	if sv.Port > 0 {
		set["PORT"] = true
	}
	for k := range global {
		set[k] = true
	}
	for k := range sv.Env {
		set[k] = true
	}
	delete(set, "PATH")
	return slices.Sorted(maps.Keys(set))
}

func launchEnv(base []string, sv config.Server, prepend []string, global map[string]string) []string {
	over := map[string]string{
		"FORCE_COLOR": "1",
		"TERM":        "xterm-256color",
		"COLORTERM":   "truecolor",
	}
	if sv.Port > 0 {
		over["PORT"] = strconv.Itoa(sv.Port)
	}
	if len(prepend) > 0 {
		over["PATH"] = strings.Join(prepend, string(os.PathListSeparator)) + string(os.PathListSeparator) + lookupEnv(base, "PATH")
	}
	expand := func(v string) string { return strings.ReplaceAll(v, "{PORT}", strconv.Itoa(sv.Port)) }
	for k, v := range global {
		over[k] = expand(v)
	}
	for k, v := range sv.Env {
		over[k] = expand(v)
	}
	if config.IsComposeCommand(sv.Cmd) {
		over["COMPOSE_MENU"] = "false"
	}
	return mergeEnv(base, over)
}
