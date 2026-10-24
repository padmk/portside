# Coding agents

Portside gives coding agents the same servers you use, so they reuse a running server instead of starting a duplicate on a random port.

## Setup

Connect Claude Code, Codex and Gemini CLI once, with the MCP server and, for the first two, the [agent skill](#agent-skill):

```sh
portside setup
```

It checks each agent installed for the account (its configuration directory exists: `~/.claude` or `$CLAUDE_CONFIG_DIR`, `~/.codex` or `$CODEX_HOME`, `~/.gemini` or `$GEMINI_CLI_HOME/.gemini`; or the `claude`, `codex` or `gemini` command is on PATH or the login shell's PATH), lists what is missing, asks once, and then runs `claude mcp add --scope user portside -- <path> mcp`, `codex mcp add portside -- <path> mcp` and `gemini mcp add --scope user portside <path> mcp` and installs the skill. When the variables are not set in its own environment, as for the app, setup reads them from the login shell. `<path>` is the `portside` on PATH when it is the same binary (such as `~/.local/bin/portside` or the Homebrew link), otherwise the binary's own path, such as `/Applications/Portside.app/Contents/Helpers/portside`.

- `--yes` skips the question.
- `--json` lists the items (`id`, `label`, `state`: `ok`, `missing`, `outdated`, `foreign` or `unavailable`, `applicable`, `command`).
- `--apply claude-mcp,codex-skill --yes` applies only those and prints the results as JSON.

When an agent's command is not found, or a skill file with that name was not written by Portside, setup leaves it alone and prints the command to run yourself. `portside doctor` reports the same items without changing anything, and `portside doctor --fix` applies the missing and outdated ones.

The dashboard offers the same setup: on its getting-started page while no server is configured (which the macOS app opens on its first launch), and under **Coding agents** in Settings, which the app's **Settings > Connect Coding Agents** and the command palette open. Each installed agent's MCP server and skill is listed as connected or with a checkbox, and **Connect** adds the checked ones. It is shown for this computer only, not while the dashboard shows another one.

To let an agent do the whole setup, paste it the prompt that `portside setup --print-prompt` prints, or that **Copy prompt for your agent** there copies. It asks the agent to run `portside setup`, check `portside doctor`, scan your projects folder, add the servers you pick, start each one and fix what fails with you, then summarize the URLs.

## Other MCP clients

Other MCP clients start the same server, `<path> mcp`, from their own config. `portside setup --print-mcp` prints the entry with the resolved path:

```json
{
  "mcpServers": {
    "portside": { "command": "/Users/you/.local/bin/portside", "args": ["mcp"] }
  }
}
```

- Cursor: put it in `~/.cursor/mcp.json` (every project) or `.cursor/mcp.json` in a project.
- Windsurf: the same `mcpServers` entry in its `mcp_config.json`, opened from the MCP settings in Cascade.
- VS Code: `.vscode/mcp.json`, or the user one from **MCP: Open User Configuration**, uses `servers`: `{"servers": {"portside": {"type": "stdio", "command": "<path>", "args": ["mcp"]}}}`.
- Zed: `context_servers` in `settings.json`: `{"context_servers": {"portside": {"command": "<path>", "args": ["mcp"]}}}`.
- opencode: `mcp` in `~/.config/opencode/opencode.json`: `{"mcp": {"portside": {"type": "local", "command": ["<path>", "mcp"], "enabled": true}}}`.
- Gemini CLI: `portside setup` adds it; by hand, `gemini mcp add --scope user portside <path> mcp`.

## MCP tools

Agents get these tools, plus instructions to reuse running servers rather than start their own:

| Tool | What it does |
| --- | --- |
| `list_servers` | Each server with the containers of its Compose project; `dir` narrows it to one checkout. |
| `ensure_server` | Starts a server if needed and reports whether it reused or started it; `dir` runs it from another [worktree](#worktrees). |
| `restart_server` | Waits for readiness, with an optional `timeout`, default `90s`. |
| `stop_server` | Stops a server. |
| `server_logs`, `container_logs` | Log lines, see below. |
| `list_containers` | Containers and the server each belongs to. |
| `container_action` | `name`, `action` (`start`, `stop`, `restart` or `recreate`), and optional `build` or `pull` for recreation; recreate waits and returns its last 100 output lines, including failures. |
| `list_ports` | Every listening TCP port and its owner. |

Readiness failures include recent output lines for a crashed server. Both log tools put each line's local time before it and take `match`, a case-insensitive regular expression, `context`, lines around each match, and `since`, a duration such as `10m` or an RFC 3339 time. A container search covers its last 20,000 lines; a server's line times come from when the daemon received the output, to the second. Replies stay under 256 KiB with long lines cut, and bracketed notes at the top say what was left out.

While this computer is connected to another one, the server and container tools take `computer` (its name) to act there, and their results name it. Without MCP, `portside ensure <name> --json`, `portside status --json`, `portside containers --json` and `portside logs <name> --grep <regexp> -C <n>` give the same information.

## Worktrees

Agents often work in a linked git worktree (`git worktree add`), where the configured server would serve the main checkout. `ensure_server` with `dir` (the agent's working directory), or `portside ensure <name> --dir <path>`, then starts a temporary copy of the server in that checkout: the same command, environment, health check and restart policy, the same subdirectory inside the worktree, and a free port of its own. Its name is `<name>--<worktree folder>` (for example `web--feature-x`), which also works as `http://web--feature-x.localhost:7878`. Dependencies are not copied: the copy uses the configured ones, such as a shared database. When `dir` is the server's own checkout, `ensure` behaves as without it; a directory outside the repository's worktrees (a different git common directory, such as a separate clone) is refused.

Temporary servers are not written to the config file. They are kept in the daemon's state, so they survive a daemon restart, and appear in `status`, the dashboard (under their original, marked `worktree`) and `list_servers` with `temporary`, `origin` and `worktree`; `list_servers` with `dir` lists the ones of that checkout. Stopping one (`stop_server`, `portside down`, or Remove in the dashboard) removes it with its logs, and the daemon removes it on its own once its worktree directory is gone. Editing or removing the original in the config applies to its copies too.

## Agent skill

The agent skill (`portside setup` installs it, as does `portside skill install`) writes `SKILL.md` for the agents set up on the account: to `~/.claude/skills/portside/` (`$CLAUDE_CONFIG_DIR/skills` when set) for Claude Code and to `~/.agents/skills/portside/` for Codex, or to `~/.codex/skills/portside/` (`$CODEX_HOME/skills`), which Codex still reads, when an earlier install put it there; once when one directory links to the other. The skill covers when to reuse, start or restart a server, how to diagnose one through its logs and containers, and what to leave alone.

The daemon rewrites an installed copy when it starts with the default config and state paths, including login-service starts, so the skill follows app updates; a file with that name that `skill install` did not write is kept unless you pass `--force`. `portside skill print` shows the file.

A command run inside an agent's sandbox does not start a daemon; see [How it works](how-it-works.md#agent-sandboxes).
