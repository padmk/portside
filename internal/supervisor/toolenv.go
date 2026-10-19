package supervisor

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/padmk/portside/internal/app"
	"github.com/padmk/portside/internal/config"
	"github.com/padmk/portside/internal/paths"
)

// Servers start in their directory from an environment read once in the home
// directory, so per-project tool versions that a terminal would pick up on
// `cd` (direnv, devbox, mise, fnm or nvm) are applied by running the command
// through that tool.

// toolLaunch is how a server's shell is started inside its tool environment.
type toolLaunch struct {
	name   string            // tool shown in the view and log, "" for none
	prefix []string          // argv before the shell; the first is an absolute path
	script string            // shell code run before the command
	env    map[string]string // extra variables for the started process only
	note   string            // a warning for the server log
}

// used reports whether the command runs inside a tool environment.
func (t toolLaunch) used() bool { return len(t.prefix) > 0 || t.script != "" }

// toolChoice is the tool that detection picks from files and PATH alone,
// without running anything. Its key is compared for stale checks, so that,
// for example, a new .envrc marks a running server stale.
type toolChoice struct {
	name string // direnv, devbox, mise, fnm or nvm; "" for none
	bin  string // the tool's executable, or nvm.sh for nvm
	file string // the file that selected it
}

func (c toolChoice) key() string {
	if c.name == "" {
		return ""
	}
	return c.name + ":" + c.file
}

// detectTool picks, for automatic wrapping, the first tool in a fixed order
// that has a file in the server directory or a parent up to the repository
// root and an executable on pathEnv. Container commands are never wrapped,
// and Node version files only apply to a Node command or a server directory
// with a package.json.
func detectTool(sv config.Server, pathEnv, nvm string) toolChoice {
	if (sv.Wrap != "" && sv.Wrap != config.WrapAuto) || containerCommand(sv.Cmd) {
		return toolChoice{}
	}
	dirs := projectDirs(sv.Dir)
	if f := findUp(dirs, ".envrc"); f != "" {
		if bin := executableIn("direnv", pathEnv); bin != "" {
			return toolChoice{"direnv", bin, f}
		}
	}
	if f := findUp(dirs, "devbox.json"); f != "" {
		if bin := executableIn("devbox", pathEnv); bin != "" {
			return toolChoice{"devbox", bin, f}
		}
	}
	if f := findUp(dirs, "mise.toml", ".mise.toml", ".config/mise.toml", "mise/config.toml", ".tool-versions"); f != "" {
		if bin := executableIn("mise", pathEnv); bin != "" {
			return toolChoice{"mise", bin, f}
		}
	}
	if !nodeCommand(sv.Cmd) && findUp(dirs[:1], "package.json") == "" {
		return toolChoice{}
	}
	if f := findUp(dirs, ".nvmrc", ".node-version"); f != "" {
		if bin := executableIn("fnm", pathEnv); bin != "" {
			return toolChoice{"fnm", bin, f}
		}
		if nvm != "" && filepath.Base(f) == ".nvmrc" {
			return toolChoice{"nvm", nvm, f}
		}
	}
	return toolChoice{}
}

// projectTools decides how sv's shell starts: through its configured wrap,
// else through the detected tool when that works without installing
// anything. keep names the variables (PORT and the config's env) that the
// shell sets again once the tool has loaded the project's environment, so
// that an .envrc or a mise [env] cannot override them. Their values travel
// in the environment, never in argv.
func projectTools(sv config.Server, env []string, shellPath string, keep []string) (toolLaunch, toolChoice) {
	t, c := chooseTool(sv, env, shellPath)
	if !t.used() {
		return t, c
	}
	if script := keepScript(shellPath, keep); script != "" {
		t.script += script
		if t.env == nil {
			t.env = map[string]string{}
		}
		for _, k := range keep {
			if validName(k) {
				t.env[keepVar(k)] = lookupEnv(env, k)
			}
		}
	}
	return t, c
}

func chooseTool(sv config.Server, env []string, shellPath string) (toolLaunch, toolChoice) {
	pathEnv := lookupEnv(env, "PATH")
	switch sv.Wrap {
	case config.WrapNone:
		return toolLaunch{}, toolChoice{}
	case "", config.WrapAuto:
	default:
		words := strings.Fields(sv.Wrap)
		bin := executableIn(words[0], pathEnv)
		if bin == "" {
			return toolLaunch{note: "wrap: " + words[0] + " not found on PATH; running the command without it"}, toolChoice{}
		}
		return toolLaunch{name: "custom", prefix: append([]string{bin}, words[1:]...)}, toolChoice{}
	}
	c := detectTool(sv, pathEnv, nvmScript(env))
	shell := filepath.Base(shellPath)
	posix := posixShells[shell] && shell != "fish"
	file := paths.ShortenHome(c.file)
	switch c.name {
	case "direnv":
		if !direnvAllowed(c.bin, sv.Dir, env) {
			return toolLaunch{note: "direnv: .envrc is not allowed; run `direnv allow` in " + sv.Dir + " to use it"}, c
		}
		return toolLaunch{name: "direnv", prefix: []string{c.bin, "exec", sv.Dir}}, c
	case "devbox":
		// Loaded into the server's shell, so the command text and the
		// working directory stay as they are.
		if !posix {
			return toolLaunch{note: "devbox: " + shell + " cannot load `devbox shellenv`; running the command without devbox"}, c
		}
		failed := shellQuote("devbox: `devbox shellenv` failed; running the command without it")
		return toolLaunch{name: "devbox", script: "eval \"$(" + shellQuote(c.bin) + " shellenv --config " + shellQuote(filepath.Dir(c.file)) + ")\" || echo " + failed + " >&2\n"}, c
	case "mise":
		// A start never installs tools; mise reports missing ones and runs
		// the command with what is installed.
		return toolLaunch{name: "mise", prefix: []string{c.bin, "exec", "--cd", sv.Dir, "--"}, env: map[string]string{"MISE_EXEC_AUTO_INSTALL": "false"}}, c
	case "fnm":
		using := "--using=" + c.file
		if !commandSucceeds(env, sv.Dir, c.bin, "exec", using, "--", "true") {
			return toolLaunch{note: "fnm: the Node version in " + file + " is not installed (`fnm install` there installs it); running the command without fnm"}, c
		}
		return toolLaunch{name: "fnm", prefix: []string{c.bin, "exec", using, "--"}}, c
	case "nvm":
		// nvm is a shell function; load it into the server's shell.
		if !posix {
			return toolLaunch{}, c
		}
		missing := shellQuote("nvm: the Node version in " + file + " is not installed (`nvm install` there installs it); running with the default node")
		return toolLaunch{name: "nvm", script: ". " + shellQuote(c.bin) + " --no-use && { nvm use || echo " + missing + " >&2; }\n"}, c
	}
	return toolLaunch{}, c
}

// containerCommand reports whether cmd runs containers, which bring their
// own tools.
func containerCommand(cmd string) bool {
	if IsComposeCommand(cmd) {
		return true
	}
	for _, w := range commandWords(cmd) {
		switch w {
		case "docker", "podman", "docker-compose", "podman-compose", "nerdctl":
			return true
		}
	}
	return false
}

// nodeTools are commands that run on Node and so follow .nvmrc.
var nodeTools = map[string]bool{
	"node": true, "npm": true, "npx": true, "pnpm": true, "pnpx": true, "yarn": true, "corepack": true,
	"vite": true, "next": true, "nuxt": true, "nuxi": true, "astro": true, "remix": true, "tsx": true,
	"ts-node": true, "nodemon": true, "ng": true, "webpack": true, "turbo": true, "nx": true,
	"vitepress": true, "storybook": true, "expo": true,
}

func nodeCommand(cmd string) bool {
	for _, w := range commandWords(cmd) {
		if nodeTools[w] {
			return true
		}
	}
	return false
}

// commandWords splits a shell command, roughly, into the base names of its
// words: enough to recognize the programs it runs.
func commandWords(cmd string) []string {
	fields := strings.FieldsFunc(cmd, func(r rune) bool {
		return strings.ContainsRune(" \t\r\n;&|()`\"'", r)
	})
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		out = append(out, filepath.Base(f))
	}
	return out
}

// keepVar carries k's value past the tool into the server's shell.
func keepVar(k string) string { return "__" + app.Env("KEEP_"+k) }

// keepScript sets keep again from their carriers and removes the carriers,
// in the syntax of the server's shell; "" for a shell it cannot write.
func keepScript(shellPath string, keep []string) string {
	shell := filepath.Base(shellPath)
	if !posixShells[shell] {
		return ""
	}
	var b strings.Builder
	for _, k := range keep {
		if !validName(k) {
			continue
		}
		v := keepVar(k)
		if shell == "fish" {
			fmt.Fprintf(&b, "set -gx %s $%s; set -e %s\n", k, v, v)
		} else {
			fmt.Fprintf(&b, "export %s=\"$%s\"; unset %s\n", k, v, v)
		}
	}
	return b.String()
}

// validName reports whether k can be named in shell code as a variable.
func validName(k string) bool {
	if k == "" || (k[0] >= '0' && k[0] <= '9') {
		return false
	}
	for _, r := range k {
		if r != '_' && (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') {
			return false
		}
	}
	return true
}

func commandSucceeds(env []string, dir, bin string, args ...string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = dir
	cmd.Env = env
	return cmd.Run() == nil
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

// nvmScript is nvm.sh of env's nvm installation, "" without one.
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
