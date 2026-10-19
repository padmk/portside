package supervisor

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/padmk/portside/internal/config"
)

// Servers start in their directory from an environment read once in the home
// directory, so per-project tool versions that a terminal would pick up on
// `cd` (direnv, devbox, mise, fnm or nvm) are applied by running the command
// through that tool.

// toolLaunch is how a server's shell is started inside its tool environment.
type toolLaunch struct {
	name   string   // tool shown in the view and log, "" for none
	prefix []string // argv before the shell; the first is an absolute path
	script string   // shell code run before the command
	note   string   // a warning for the server log
}

// projectTools picks the tool environment for sv: its configured wrap, else
// the first tool, in a fixed order, that has a file in the server directory
// or a parent up to the repository root and an executable on env's PATH.
func projectTools(sv config.Server, env []string, shellPath string) toolLaunch {
	pathEnv := lookupEnv(env, "PATH")
	switch sv.Wrap {
	case config.WrapNone:
		return toolLaunch{}
	case "", "auto":
	default:
		words := strings.Fields(sv.Wrap)
		bin := executableIn(words[0], pathEnv)
		if bin == "" {
			return toolLaunch{note: "wrap: " + words[0] + " not found on PATH; running the command without it"}
		}
		return toolLaunch{name: "custom", prefix: append([]string{bin}, words[1:]...)}
	}
	dirs := projectDirs(sv.Dir)
	if bin := executableIn("direnv", pathEnv); bin != "" && findUp(dirs, ".envrc") != "" {
		if !direnvAllowed(bin, sv.Dir, env) {
			return toolLaunch{note: "direnv: .envrc is not allowed; run `direnv allow` in " + sv.Dir + " to use it"}
		}
		return toolLaunch{name: "direnv", prefix: []string{bin, "exec", sv.Dir}}
	}
	if bin := executableIn("devbox", pathEnv); bin != "" {
		if f := findUp(dirs, "devbox.json"); f != "" {
			return toolLaunch{name: "devbox", prefix: []string{bin, "run", "--config", filepath.Dir(f), "--"}}
		}
	}
	if bin := executableIn("mise", pathEnv); bin != "" && findUp(dirs, "mise.toml", ".mise.toml", ".config/mise.toml", "mise/config.toml", ".tool-versions") != "" {
		return toolLaunch{name: "mise", prefix: []string{bin, "exec", "--cd", sv.Dir, "--"}}
	}
	if f := findUp(dirs, ".nvmrc", ".node-version"); f != "" {
		if bin := executableIn("fnm", pathEnv); bin != "" {
			return toolLaunch{name: "fnm", prefix: []string{bin, "exec", "--using=" + f, "--"}}
		}
		// nvm is a shell function; load it into the server's shell.
		if nvm := nvmScript(env); nvm != "" && filepath.Base(f) == ".nvmrc" && posixShells[filepath.Base(shellPath)] && filepath.Base(shellPath) != "fish" {
			return toolLaunch{name: "nvm", script: ". " + shellQuote(nvm) + " --no-use && nvm use\n"}
		}
	}
	return toolLaunch{}
}

// projectDirs lists dir and its parents up to the repository root (the
// first one holding .git), or only dir outside a repository.
func projectDirs(dir string) []string {
	var out []string
	for d := filepath.Clean(dir); ; {
		out = append(out, d)
		if _, err := os.Lstat(filepath.Join(d, ".git")); err == nil {
			return out
		}
		parent := filepath.Dir(d)
		if parent == d {
			return out[:1]
		}
		d = parent
	}
}

// findUp returns the first of names found in dirs, nearest directory first.
func findUp(dirs []string, names ...string) string {
	for _, d := range dirs {
		for _, n := range names {
			p := filepath.Join(d, n)
			if st, err := os.Stat(p); err == nil && !st.IsDir() {
				return p
			}
		}
	}
	return ""
}

// executableIn finds name on pathEnv only, unlike findInPath, so tools the
// server's environment does not have are not used.
func executableIn(name, pathEnv string) string {
	if filepath.IsAbs(name) {
		if st, err := os.Stat(name); err == nil && !st.IsDir() && st.Mode()&0o111 != 0 {
			return name
		}
		return ""
	}
	for _, dir := range filepath.SplitList(pathEnv) {
		if dir == "" {
			continue
		}
		cand := filepath.Join(dir, name)
		if st, err := os.Stat(cand); err == nil && !st.IsDir() && st.Mode()&0o111 != 0 {
			return cand
		}
	}
	return ""
}

// direnvAllowed reports whether direnv would load the .envrc for dir.
// `direnv exec` fails on a blocked file, while a terminal still runs the
// command; an unknown answer (an older direnv) counts as allowed.
func direnvAllowed(bin, dir string, env []string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "status", "--json")
	cmd.Dir = dir
	cmd.Env = env
	out, err := cmd.Output()
	if err != nil {
		return true
	}
	var st struct {
		State struct {
			FoundRC *struct {
				Allowed int `json:"allowed"`
			} `json:"foundRC"`
		} `json:"state"`
	}
	if json.Unmarshal(out, &st) != nil || st.State.FoundRC == nil {
		return true
	}
	return st.State.FoundRC.Allowed == 0
}

func nvmScript(env []string) string {
	dir := lookupEnv(env, "NVM_DIR")
	if dir == "" {
		home := lookupEnv(env, "HOME")
		if home == "" {
			return ""
		}
		dir = filepath.Join(home, ".nvm")
	}
	p := filepath.Join(dir, "nvm.sh")
	if _, err := os.Stat(p); err != nil {
		return ""
	}
	return p
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
