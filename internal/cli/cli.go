// Package cli implements the command-line interface.
package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/padmk/portside/internal/app"
	"github.com/padmk/portside/internal/client"
	"github.com/padmk/portside/internal/config"
	"github.com/padmk/portside/internal/daemon"
	"github.com/padmk/portside/internal/paths"
)

type command struct {
	names   []string
	usage   string
	summary string
	run     func(e *env, args []string) error
	section string
	// remote: the command takes --on <computer> and then acts on that
	// connected computer.
	remote bool
}

// env carries what every command needs.
type env struct {
	ctx     context.Context
	cfgPath string
	out     io.Writer
	errOut  io.Writer
	color   bool
	// on names the connected computer that commands act on, from --on.
	on string
	// conn is the connection to that computer, once client() resolved it.
	conn *client.Connection
}

// port is where commands reach the daemon: the port a running daemon
// actually listens on (it may differ from the config until a restart), else
// the configured one.
func (e *env) port() int {
	if p := e.envPort(); p > 0 {
		return p
	}
	if p := client.LivePort(); p > 0 {
		if _, err := client.New(p).Ping(e.ctx); err == nil {
			return p
		}
	}
	return e.configPort()
}

// configPort is the port a newly started daemon will use.
func (e *env) configPort() int {
	if p := e.envPort(); p > 0 {
		return p
	}
	var port int
	var auto bool
	if cfg, err := config.Load(e.cfgPath); err == nil {
		port, auto = cfg.Port, cfg.PortAuto
	} else if p, a, portErr := config.ReadPort(e.cfgPath); portErr == nil {
		port, auto = p, a
	} else {
		fmt.Fprintf(e.errOut, "config: %v\n", err)
		return app.DefaultPort
	}
	// A daemon whose config sets no port keeps a fallback port it chose.
	if p := daemon.SavedPort(paths.StateDir()); auto && p > 0 {
		return p
	}
	return port
}

func (e *env) envPort() int {
	if p := os.Getenv(app.Env("PORT")); p != "" {
		var n int
		if _, err := fmt.Sscanf(p, "%d", &n); err == nil && n > 0 {
			return n
		}
	}
	return 0
}

// client returns a client for a running daemon, starting one if needed.
// With --on its API requests go to that connected computer.
func (e *env) client() (*client.Client, error) {
	c := client.New(e.port())
	if _, err := c.Ensure(e.ctx); err != nil {
		return nil, err
	}
	if e.on == "" {
		return c, nil
	}
	on, cn, err := c.On(e.ctx, e.on)
	if err != nil {
		return nil, err
	}
	e.conn = cn
	return on, nil
}

// takeOn removes --on <computer> or --on=<computer> from args.
func takeOn(args []string) (rest []string, on string, err error) {
	for i := 0; i < len(args); i++ {
		switch a := args[i]; {
		case a == "--on" || a == "-on":
			if i+1 >= len(args) || args[i+1] == "" {
				return nil, "", usageError{"--on needs a computer name"}
			}
			on = args[i+1]
			i++
		case strings.HasPrefix(a, "--on=") || strings.HasPrefix(a, "-on="):
			_, on, _ = strings.Cut(a, "=")
			if on == "" {
				return nil, "", usageError{"--on needs a computer name"}
			}
		default:
			rest = append(rest, a)
		}
	}
	return rest, on, nil
}

// clientIfRunning returns a client only if a daemon is already up.
func (e *env) clientIfRunning() (*client.Client, bool) {
	c := client.New(e.port())
	if _, err := c.Ping(e.ctx); err != nil {
		return c, false
	}
	return c, true
}

var commands []*command

func init() {
	commands = []*command{
		{names: []string{"status", "ls", "ps", "st"}, section: "Servers", usage: "status [--json]", summary: "List servers with state, port and usage", run: cmdStatus, remote: true},
		{names: []string{"up", "start"}, section: "Servers", usage: "up <name|group>... | --all [--wait] [--takeover]", summary: "Start servers (dependencies first)", run: cmdUp, remote: true},
		{names: []string{"down", "stop"}, section: "Servers", usage: "down <name|group>... | --all", summary: "Stop servers gracefully (removes temporary ones)", run: cmdDown, remote: true},
		{names: []string{"restart"}, section: "Servers", usage: "restart <name|group>...", summary: "Restart servers", run: cmdRestart, remote: true},
		{names: []string{"ensure"}, section: "Servers", usage: "ensure <name> [--dir <checkout>] [--timeout 90s] [--json]", summary: "Start if needed, wait until ready, print the URL; --dir runs it from another worktree", run: cmdEnsure, remote: true},
		{names: []string{"logs", "log"}, section: "Servers", usage: "logs <name> [-n 200] [-f] [--raw] [--grep <regexp> [-C N]] [-t] [--since 10m]", summary: "Print, search or follow a server's or container's output", run: cmdLogs, remote: true},
		{names: []string{"attach", "a"}, section: "Servers", usage: "attach <name>", summary: "Interactive terminal of a server (Ctrl-] detaches)", run: cmdAttach, remote: true},
		{names: []string{"open"}, section: "Servers", usage: "open [name] [--alias] [--print]", summary: "Open the dashboard or a server in the browser", run: cmdOpen},
		{names: []string{"containers"}, section: "Servers", usage: "containers [server|project]... [--json] | start|stop|restart|recreate <name> [--build] [--pull]", summary: "List containers or act on one container", run: cmdContainers, remote: true},
		{names: []string{"ports"}, section: "Servers", usage: "ports [--json]", summary: "Show every listening TCP port and its owner", run: cmdPorts},

		{names: []string{"computers"}, section: "Other computers", usage: "computers [--json] | --print-prompt", summary: "List your other computers that share their servers, and the connection; --print-prompt prints a setup request for a coding agent", run: cmdComputers},
		{names: []string{"pair"}, section: "Other computers", usage: "pair <computer> <code>", summary: "Pair with a computer using the code it shows, then connect", run: cmdPair},
		{names: []string{"connect"}, section: "Other computers", usage: "connect <computer>", summary: "Connect to a paired computer; --on <computer> then acts on it", run: cmdConnect},
		{names: []string{"forward"}, section: "Other computers", usage: "forward on|off <port>", summary: "Turn forwarding of a port of the connected computer on or off, kept for that computer", run: cmdForward},
		{names: []string{"disconnect"}, section: "Other computers", usage: "disconnect", summary: "End the connection to another computer", run: cmdDisconnect},

		{names: []string{"add"}, section: "Configuration", usage: "add <name> --cmd <command> [--dir <dir>] [--port N] [--group G]", summary: "Add a server to the config", run: cmdAdd},
		{names: []string{"rm", "remove"}, section: "Configuration", usage: "rm <name>", summary: "Remove a server from the config", run: cmdRemove},
		{names: []string{"enable"}, section: "Configuration", usage: "enable <name>", summary: "Include a server in --all and restore", run: cmdEnable(true)},
		{names: []string{"disable"}, section: "Configuration", usage: "disable <name>", summary: "Exclude a server from --all and restore", run: cmdEnable(false)},
		{names: []string{"scan"}, section: "Configuration", usage: "scan [dir] [--add [--yes]] [--json]", summary: "Find projects with dev servers and propose entries", run: cmdScan},
		{names: []string{"init"}, section: "Configuration", usage: "init", summary: "Create the config file", run: cmdInit},
		{names: []string{"config"}, section: "Configuration", usage: "config [path|edit|check]", summary: "Show, edit or validate the config", run: cmdConfig},

		{names: []string{"daemon"}, section: "Daemon", usage: "daemon [start|stop|restart|status|info [--no-start]|logs]", summary: "Run the daemon in the foreground, or control it", run: cmdDaemon},
		{names: []string{"service"}, section: "Daemon", usage: "service install|uninstall|status", summary: "Start the daemon at login (launchd / systemd)", run: cmdService},
		{names: []string{"share"}, section: "Daemon", usage: "share [on|off|code|devices|revoke <device>]", summary: "Let your paired devices on Tailscale connect to this computer", run: cmdShare},
		{names: []string{"mcp"}, section: "Daemon", usage: "mcp [--mode read-only|start|full] [--dir <path>]...", summary: "MCP server on stdio for coding agents; --mode limits what the tools change (--read-only for read-only), --dir the projects they see", run: cmdMCP},
		{names: []string{"skill"}, section: "Daemon", usage: "skill install [--force] | print", summary: "Install the agent skill for Claude Code and Codex", run: cmdSkill},
		{names: []string{"setup"}, section: "Daemon", usage: "setup [--yes] [--json] [--mcp-mode <mode>] | --apply <id>[,<id>...] --yes | --print-mcp | --print-prompt", summary: "Connect Claude Code and Codex: the MCP server and the agent skill", run: cmdSetup},
		{names: []string{"doctor"}, section: "Daemon", usage: "doctor [--fix]", summary: "Check the setup and explain problems; --fix connects coding agents", run: cmdDoctor},
		{names: []string{"version"}, section: "Daemon", usage: "version", summary: "Print the version", run: cmdVersion},
	}
}

// Main runs the CLI and returns the process exit code.
func Main(args []string) int {
	for _, note := range paths.MoveLegacy() {
		fmt.Fprintf(os.Stderr, "%s: %s\n", app.ID, note)
	}
	e := &env{
		ctx:     context.Background(),
		cfgPath: paths.ConfigFile(),
		out:     os.Stdout,
		errOut:  os.Stderr,
		color:   colorEnabled(),
	}
	explicitConfig := false
	// Global flags may precede the command.
	for len(args) > 0 && strings.HasPrefix(args[0], "-") {
		switch {
		case args[0] == "--config" && len(args) > 1:
			e.cfgPath = paths.ExpandHome(args[1])
			explicitConfig = true
			args = args[2:]
		case strings.HasPrefix(args[0], "--config="):
			e.cfgPath = paths.ExpandHome(strings.TrimPrefix(args[0], "--config="))
			explicitConfig = true
			args = args[1:]
		case args[0] == "--on" && len(args) > 1:
			e.on = args[1]
			args = args[2:]
		case strings.HasPrefix(args[0], "--on="):
			e.on = strings.TrimPrefix(args[0], "--on=")
			args = args[1:]
		case args[0] == "-h" || args[0] == "--help":
			usage(e.out)
			return 0
		case args[0] == "-v" || args[0] == "--version":
			fmt.Fprintln(e.out, app.ID, app.Version)
			return 0
		default:
			fmt.Fprintf(e.errOut, "unknown flag %s\n", args[0])
			return 2
		}
	}
	if explicitConfig {
		// A spawned daemon runs from "/", so it needs an absolute path, and an
		// explicit flag wins over an inherited environment variable.
		if abs, err := filepath.Abs(e.cfgPath); err == nil {
			e.cfgPath = abs
		}
		os.Setenv(app.Env("CONFIG"), e.cfgPath)
	}
	if len(args) == 0 {
		if _, err := os.Stat(e.cfgPath); err != nil {
			usage(e.out)
			return 0
		}
		args = []string{"status"}
	}
	if args[0] == "help" {
		if len(args) > 1 {
			if c := find(args[1]); c != nil {
				fmt.Fprintf(e.out, "%s %s\n\n%s\n", app.ID, c.usage, c.summary)
				if c.remote {
					fmt.Fprintln(e.out, "--on <computer> runs it on the connected computer.")
				}
				return 0
			}
		}
		usage(e.out)
		return 0
	}
	c := find(args[0])
	if c == nil {
		fmt.Fprintf(e.errOut, "unknown command %q; see `%s help`\n", args[0], app.ID)
		return 2
	}
	rest, on, err := takeOn(args[1:])
	if on != "" {
		e.on = on
	}
	if err == nil && e.on != "" && !c.remote {
		err = usageError{fmt.Sprintf("--on works with %s", strings.Join(remoteCommands(), ", "))}
	}
	if err == nil {
		err = c.run(e, rest)
	}
	if err != nil {
		var ue usageError
		if errors.As(err, &ue) {
			fmt.Fprintf(e.errOut, "usage: %s %s\n", app.ID, c.usage)
			if ue.msg != "" {
				fmt.Fprintln(e.errOut, ue.msg)
			}
			return 2
		}
		var se silentError
		if errors.As(err, &se) {
			return se.code
		}
		fmt.Fprintf(e.errOut, "%s %s\n", e.paint(red, "error:"), err)
		return 1
	}
	return 0
}

func find(name string) *command {
	for _, c := range commands {
		for _, n := range c.names {
			if n == name {
				return c
			}
		}
	}
	return nil
}

type usageError struct{ msg string }

func (u usageError) Error() string { return "usage: " + u.msg }

// silentError ends the command with a code after it already printed output.
type silentError struct{ code int }

func (s silentError) Error() string { return fmt.Sprintf("exit %d", s.code) }

func usage(w io.Writer) {
	fmt.Fprintf(w, "%s keeps your local dev servers running, in one place.\n\n", app.Name)
	fmt.Fprintf(w, "Usage: %s [--config FILE] [--on COMPUTER] <command> [arguments]\n", app.ID)
	section := ""
	for _, c := range commands {
		if c.section != section {
			section = c.section
			fmt.Fprintf(w, "\n%s\n", section)
		}
		fmt.Fprintf(w, "  %-10s %s\n", c.names[0], c.summary)
	}
	fmt.Fprintf(w, "\n--on COMPUTER runs %s on the connected computer.\n", strings.Join(remoteCommands(), ", "))
	fmt.Fprintf(w, "\nConfig: %s\nDashboard: `%s open`\nRun `%s help <command>` for arguments.\n",
		paths.ShortenHome(paths.ConfigFile()), app.ID, app.ID)
}

// remoteCommands names the commands that take --on.
func remoteCommands() []string {
	var out []string
	for _, c := range commands {
		if c.remote {
			out = append(out, c.names[0])
		}
	}
	return out
}

// parse parses flags that may appear anywhere among positional arguments.
func parse(fs *flag.FlagSet, args []string) ([]string, error) {
	fs.SetOutput(io.Discard)
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, usageError{err.Error()}
		}
		args = fs.Args()
		if len(args) == 0 {
			return pos, nil
		}
		pos = append(pos, args[0])
		args = args[1:]
	}
}
