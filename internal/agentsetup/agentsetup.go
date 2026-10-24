// Package agentsetup is the optional setup for coding agents: the MCP server
// registered with Claude Code and Codex, and the agent skill in their skill
// directories. It checks each item and applies the missing ones, for
// `setup`, `doctor` and the menu bar app.
package agentsetup

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/BurntSushi/toml"

	"github.com/padmk/portside/internal/agentskill"
	"github.com/padmk/portside/internal/app"
	"github.com/padmk/portside/internal/paths"
)

// State of an item.
type State string

const (
	OK       State = "ok"
	Missing  State = "missing"
	Outdated State = "outdated"
	// Foreign is a skill file with the product's name that it did not write.
	Foreign State = "foreign"
	// Unavailable is a missing MCP server whose agent CLI was not found, so
	// only the manual command can add it.
	Unavailable State = "unavailable"
)

// Item is one setup step. Applicable is false when its agent is not
// installed for this account. Command is what applying it runs, or what to
// run by hand when it cannot be applied.
type Item struct {
	ID         string `json:"id"`
	Label      string `json:"label"`
	State      State  `json:"state"`
	Applicable bool   `json:"applicable"`
	Command    string `json:"command"`
	// Detail explains a state that needs the user, such as a foreign file.
	Detail string `json:"detail,omitempty"`

	apply func(ctx context.Context) error
}

// Fixable reports whether applying the item can make it ok without
// replacing anything the user wrote.
func (it Item) Fixable() bool {
	return it.Applicable && (it.State == Missing || it.State == Outdated)
}

// Result is the outcome of applying one item.
type Result struct {
	ID      string `json:"id"`
	Label   string `json:"label"`
	OK      bool   `json:"ok"`
	Changed bool   `json:"changed"`
	State   State  `json:"state"`
	Error   string `json:"error,omitempty"`
	// Command is the manual command after a failure.
	Command string `json:"command,omitempty"`
}

// agent is a coding agent that can use the MCP server and the skill.
type agent struct {
	key  string // item ID prefix; the agent of agentskill.Target
	name string
	cli  string
	// dir is its configuration directory; it exists once the agent ran.
	dir func(agentskill.Dirs) string
	// envKey is the variable that moves dir; def is dir's default.
	envKey string
	def    func(agentskill.Dirs) string
	// registered reports whether the MCP server is configured for it.
	registered func(agentskill.Dirs) bool
	add        func(exe string) []string // arguments that register the MCP server
}

var agents = []agent{
	{key: "claude", name: "Claude Code", cli: "claude",
		dir:    func(d agentskill.Dirs) string { return d.Claude },
		envKey: "CLAUDE_CONFIG_DIR",
		def:    func(d agentskill.Dirs) string { return filepath.Join(d.Home, ".claude") },
		registered: func(d agentskill.Dirs) bool {
			// The file is in the home directory, or in CLAUDE_CONFIG_DIR
			// when that is set.
			if jsonHasServer(filepath.Join(d.Claude, ".claude.json")) {
				return true
			}
			return d.Claude == filepath.Join(d.Home, ".claude") && jsonHasServer(filepath.Join(d.Home, ".claude.json"))
		},
		add: func(exe string) []string {
			return []string{"mcp", "add", "--scope", "user", app.ID, "--", exe, "mcp"}
		}},
	{key: "codex", name: "Codex", cli: "codex",
		dir:    func(d agentskill.Dirs) string { return d.Codex },
		envKey: "CODEX_HOME",
		def:    func(d agentskill.Dirs) string { return filepath.Join(d.Home, ".codex") },
		registered: func(d agentskill.Dirs) bool {
			var cfg struct {
				MCPServers map[string]toml.Primitive `toml:"mcp_servers"`
			}
			if _, err := toml.DecodeFile(filepath.Join(d.Codex, "config.toml"), &cfg); err != nil {
				return false
			}
			_, ok := cfg.MCPServers[app.ID]
			return ok
		},
		add: func(exe string) []string {
			return []string{"mcp", "add", app.ID, "--", exe, "mcp"}
		}},
}

// jsonHasServer reports whether the JSON file at path lists the MCP server
// under mcpServers.
func jsonHasServer(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var cfg struct {
		MCPServers map[string]json.RawMessage `json:"mcpServers"`
	}
	_ = json.Unmarshal(data, &cfg)
	_, ok := cfg.MCPServers[app.ID]
	return ok
}

// Check returns every item, applicable or not, for a daemon on port with
// its config at cfgPath (the skill names both).
func Check(port int, cfgPath string) []Item {
	dirs, err := agentskill.Locate()
	if err != nil {
		return nil
	}
	t := &tools{}
	exe, self := executable(t)
	var items []Item
	for _, a := range agents {
		items = append(items, mcpItem(a, dirs, exe, t))
	}
	items = append(items, skillItems(dirs, self, agentskill.Render(port, cfgPath))...)
	return items
}

// MCPConfig is the MCP server entry for clients configured by hand, in the
// common mcpServers shape, with the path agents should start it with.
func MCPConfig() any {
	type server struct {
		Command string   `json:"command"`
		Args    []string `json:"args"`
	}
	exe, _ := executable(&tools{})
	return map[string]map[string]server{"mcpServers": {app.ID: {Command: exe, Args: []string{"mcp"}}}}
}

// Applicable reports whether any agent is installed.
func Applicable(items []Item) bool {
	for _, it := range items {
		if it.Applicable {
			return true
		}
	}
	return false
}

func mcpItem(a agent, dirs agentskill.Dirs, exe string, t *tools) Item {
	cli, env := t.find(a.cli)
	_, dirErr := os.Stat(a.dir(dirs))
	it := Item{
		ID:         a.key + "-mcp",
		Label:      a.name + " MCP server",
		Applicable: dirErr == nil || cli != "",
		Command:    shellJoin(append([]string{a.cli}, a.add(exe)...)),
	}
	switch {
	case a.registered(dirs):
		it.State = OK
	case cli == "":
		it.State = Unavailable
		it.Detail = "the " + a.cli + " command was not found"
	default:
		it.State = Missing
		args := a.add(exe)
		env = agentEnv(a, dirs, env)
		it.apply = func(ctx context.Context) error {
			ctx, cancel := context.WithTimeout(ctx, time.Minute)
			defer cancel()
			cmd := exec.CommandContext(ctx, cli, args...)
			cmd.Env = env
			out, err := cmd.CombinedOutput()
			if err != nil {
				if msg := strings.TrimSpace(string(out)); msg != "" {
					return fmt.Errorf("%s: %w: %s", a.cli, err, msg)
				}
				return fmt.Errorf("%s: %w", a.cli, err)
			}
			return nil
		}
	}
	return it
}

// agentEnv is the environment to run a's CLI with: env (nil for this
// process's own) with a's directory variable set when the directory found
// is not the default, since it may come only from the login shell's startup
// files, which a CLI found on this process's PATH does not see.
func agentEnv(a agent, dirs agentskill.Dirs, env []string) []string {
	v := a.dir(dirs)
	if v == a.def(dirs) {
		return env
	}
	if env == nil {
		env = os.Environ()
	}
	out := make([]string, 0, len(env)+1)
	for _, kv := range env {
		if !strings.HasPrefix(kv, a.envKey+"=") {
			out = append(out, kv)
		}
	}
	return append(out, a.envKey+"="+v)
}

func skillItems(dirs agentskill.Dirs, self, content string) []Item {
	targets := map[string]string{}
	for _, t := range agentskill.AgentTargets() {
		targets[t.Agent] = t.Path
	}
	installed := func(a agent) bool {
		_, err := os.Stat(a.dir(dirs))
		return err == nil
	}
	var items []Item
	for i, a := range agents {
		path, ok := targets[a.key]
		if !ok && installed(a) {
			// Its skills directory is an earlier agent's, so that item covers it.
			continue
		}
		label := "Skill for " + a.name
		for _, b := range agents[i+1:] {
			if _, own := targets[b.key]; ok && !own && installed(b) {
				label += " and " + b.name
			}
		}
		it := Item{
			ID:         a.key + "-skill",
			Label:      label,
			Applicable: ok,
			Command:    self + " skill install",
			State:      Missing,
		}
		if !ok {
			items = append(items, it)
			continue
		}
		switch agentskill.State(path, content) {
		case agentskill.Current:
			it.State = OK
		case agentskill.Outdated:
			it.State = Outdated
		case agentskill.Foreign:
			it.State = Foreign
			it.Command = self + " skill install --force"
			it.Detail = paths.ShortenHome(path) + " was not installed by " + app.Name
		}
		it.apply = func(context.Context) error {
			_, err := agentskill.Install(path, content, false)
			return err
		}
		items = append(items, it)
	}
	return items
}

// Apply applies items and reports each. An item that is already ok
// succeeds unchanged; one that needs the user fails with its manual command.
func Apply(ctx context.Context, items []Item) []Result {
	out := make([]Result, 0, len(items))
	for _, it := range items {
		r := Result{ID: it.ID, Label: it.Label, State: it.State, OK: it.State == OK}
		switch {
		case it.State == OK:
		case !it.Applicable:
			r.Error = "not installed for this account"
		case !it.Fixable() || it.apply == nil:
			r.Error = it.Detail
			r.Command = it.Command
		default:
			if err := it.apply(ctx); err != nil {
				r.Error = err.Error()
				r.Command = it.Command
			} else {
				r.OK, r.Changed, r.State = true, true, OK
			}
		}
		out = append(out, r)
	}
	return out
}

// Select returns the items with the given IDs, in that order.
func Select(items []Item, ids []string) ([]Item, error) {
	var out []Item
	var unknown []string
	for _, id := range ids {
		found := false
		for _, it := range items {
			if it.ID == id {
				out = append(out, it)
				found = true
				break
			}
		}
		if !found {
			unknown = append(unknown, id)
		}
	}
	if len(unknown) > 0 {
		var known []string
		for _, it := range items {
			known = append(known, it.ID)
		}
		return nil, fmt.Errorf("unknown item %s; items: %s", strings.Join(unknown, ", "), strings.Join(known, ", "))
	}
	return out, nil
}

// Failed reports whether any result failed.
func Failed(results []Result) bool {
	for _, r := range results {
		if !r.OK {
			return true
		}
	}
	return false
}

// tools finds agent CLIs on PATH, then on the login shell's PATH, which
// the app's helper needs because it starts with a minimal one.
type tools struct {
	once sync.Once
	env  []string
}

// find returns the absolute path of name and the environment to run it
// with (nil for this process's own).
func (t *tools) find(name string) (string, []string) {
	if p, err := exec.LookPath(name); err == nil {
		if abs, err := filepath.Abs(p); err == nil {
			return abs, nil
		}
	}
	t.once.Do(func() { t.env = agentskill.ShellEnv() })
	for _, kv := range t.env {
		path, ok := strings.CutPrefix(kv, "PATH=")
		if !ok {
			continue
		}
		for _, dir := range filepath.SplitList(path) {
			cand := filepath.Join(dir, name)
			if !filepath.IsAbs(cand) {
				continue
			}
			if st, err := os.Stat(cand); err == nil && !st.IsDir() && st.Mode()&0o111 != 0 {
				return cand, t.env
			}
		}
	}
	return "", nil
}

// executable returns the path agents should start the MCP server with: the
// command on PATH when it is this binary (a stable link such as
// ~/.local/bin or a Homebrew bin entry), else this binary's own path. self
// is how the user runs this binary in a shell: its name when PATH has it,
// else that path.
func executable(t *tools) (path, self string) {
	exe, err := os.Executable()
	if err != nil {
		return app.ID, app.ID
	}
	real, err := filepath.EvalSymlinks(exe)
	if err != nil {
		real = exe
	}
	if p, _ := t.find(app.ID); p != "" {
		if r, err := filepath.EvalSymlinks(p); err == nil && r == real {
			return p, app.ID
		}
	}
	return real, shellJoin([]string{real})
}

// shellJoin quotes args for a POSIX shell.
func shellJoin(args []string) string {
	q := make([]string, len(args))
	for i, a := range args {
		if a != "" && strings.Trim(a, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_./=:@+,") == "" {
			q[i] = a
			continue
		}
		q[i] = "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
	}
	return strings.Join(q, " ")
}
