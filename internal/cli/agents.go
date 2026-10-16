package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"

	"github.com/padmk/portside/internal/agentskill"
	"github.com/padmk/portside/internal/app"
	"github.com/padmk/portside/internal/paths"
)

// agent is a coding agent that can use the MCP server and the skill.
type agent struct {
	name string
	home string // ~/.claude or ~/.codex
	// registered reports whether the MCP server is configured for it.
	registered func(home string) bool
	add        string // the command that registers the MCP server
}

var agents = []agent{
	{name: "Claude Code", home: ".claude", add: "claude mcp add --scope user " + app.ID + " -- " + app.ID + " mcp",
		registered: func(string) bool {
			h, _ := os.UserHomeDir()
			data, err := os.ReadFile(filepath.Join(h, ".claude.json"))
			if err != nil {
				return false
			}
			var cfg struct {
				MCPServers map[string]json.RawMessage `json:"mcpServers"`
			}
			_ = json.Unmarshal(data, &cfg)
			_, ok := cfg.MCPServers[app.ID]
			return ok
		}},
	{name: "Codex", home: ".codex", add: "codex mcp add " + app.ID + " -- " + app.ID + " mcp",
		registered: func(home string) bool {
			var cfg struct {
				MCPServers map[string]toml.Primitive `toml:"mcp_servers"`
			}
			if _, err := toml.DecodeFile(filepath.Join(home, "config.toml"), &cfg); err != nil {
				return false
			}
			_, ok := cfg.MCPServers[app.ID]
			return ok
		}},
}

// doctorAgents reports, for each agent set up on this account, whether it
// has the MCP server and the current skill.
func doctorAgents(e *env, ok, warn func(string, ...any)) {
	h, err := os.UserHomeDir()
	if err != nil {
		return
	}
	found := false
	for _, a := range agents {
		home := filepath.Join(h, a.home)
		if _, err := os.Stat(home); err != nil {
			continue
		}
		found = true
		if a.registered(home) {
			ok("%s has the MCP server", a.name)
		} else {
			warn("%s does not have the MCP server; add it with `%s`", a.name, a.add)
		}
	}
	if !found {
		fmt.Fprintf(e.out, "%s no coding agent found (Claude Code, Codex)\n", e.paint(dim, "•"))
		return
	}
	content := agentskill.Render(e.port(), e.cfgPath)
	for _, path := range agentskill.Targets() {
		switch agentskill.State(path, content) {
		case agentskill.Current:
			ok("agent skill %s is up to date", paths.ShortenHome(path))
		case agentskill.Outdated:
			warn("agent skill %s is out of date; run `%s skill install`", paths.ShortenHome(path), app.ID)
		case agentskill.Foreign:
			warn("%s was not installed by %s; `%s skill install --force` replaces it", paths.ShortenHome(path), app.Name, app.ID)
		default:
			warn("agent skill not installed at %s; run `%s skill install`", paths.ShortenHome(path), app.ID)
		}
	}
}
