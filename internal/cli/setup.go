package cli

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/padmk/portside/internal/agentsetup"
	"github.com/padmk/portside/internal/app"
)

// cmdSetup connects the installed coding agents: the MCP server and the
// agent skill (internal/agentsetup).
func cmdSetup(e *env, args []string) error {
	fs := flag.NewFlagSet("setup", flag.ContinueOnError)
	yes := fs.Bool("yes", false, "")
	asJSON := fs.Bool("json", false, "")
	only := fs.String("apply", "", "")
	printMCP := fs.Bool("print-mcp", false, "")
	printPrompt := fs.Bool("print-prompt", false, "")
	words, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(words) > 0 {
		return usageError{""}
	}
	if *printMCP {
		// The entry for MCP clients configured by hand (Cursor, Windsurf and
		// others); see README "Coding agents".
		return printJSON(e, agentsetup.MCPConfig())
	}
	if *printPrompt {
		// A request to paste into a coding agent; see README "Coding agents".
		fmt.Fprint(e.out, agentsetup.Prompt())
		return nil
	}
	items := agentsetup.Check(e.port(), e.cfgPath)
	if *asJSON && *only == "" {
		return printJSON(e, items)
	}
	if *only != "" {
		if !*yes {
			return usageError{"--apply needs --yes"}
		}
		chosen, err := agentsetup.Select(items, strings.Split(*only, ","))
		if err != nil {
			return err
		}
		results := agentsetup.Apply(e.ctx, chosen)
		if err := printJSON(e, results); err != nil {
			return err
		}
		if agentsetup.Failed(results) {
			return silentError{1}
		}
		return nil
	}

	if !agentsetup.Applicable(items) {
		fmt.Fprintf(e.out, "No coding agent (Claude Code, Codex, Gemini CLI) is installed for this account; nothing to set up. `%s setup --print-mcp` prints the MCP entry for other clients.\n", app.ID)
		return nil
	}
	var todo []agentsetup.Item
	for _, it := range items {
		switch {
		case !it.Applicable:
		case it.State == agentsetup.OK:
			fmt.Fprintf(e.out, "%s %s\n", e.paint(green, "✓"), it.Label)
		case it.Fixable():
			todo = append(todo, it)
		default:
			fmt.Fprintf(e.out, "%s %s: %s; run `%s` yourself\n", e.paint(yellow, "!"), it.Label, it.Detail, it.Command)
		}
	}
	if len(todo) == 0 {
		return nil
	}
	fmt.Fprintln(e.out, "\nThis will run:")
	for _, it := range todo {
		fmt.Fprintf(e.out, "  %s  (%s)\n", it.Command, it.Label)
	}
	if !*yes {
		if !isTerminal(os.Stdin) {
			fmt.Fprintf(e.out, "\nRun `%s setup --yes` to apply.\n", app.ID)
			return nil
		}
		fmt.Fprint(e.out, "\nContinue? [y/N] ")
		answer, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		if a := strings.ToLower(strings.TrimSpace(answer)); a != "y" && a != "yes" {
			fmt.Fprintln(e.out, "Nothing changed.")
			return nil
		}
	}
	fmt.Fprintln(e.out)
	if agentsetup.Failed(printResults(e, agentsetup.Apply(e.ctx, todo))) {
		return silentError{1}
	}
	return nil
}

// printResults prints what applying items did and returns the results.
func printResults(e *env, results []agentsetup.Result) []agentsetup.Result {
	for _, r := range results {
		if r.OK {
			fmt.Fprintf(e.out, "%s %s added\n", e.paint(green, "✓"), r.Label)
			continue
		}
		fmt.Fprintf(e.out, "%s %s failed: %s\n", e.paint(red, "✗"), r.Label, r.Error)
		if r.Command != "" {
			fmt.Fprintf(e.out, "  run it yourself: %s\n", r.Command)
		}
	}
	return results
}

// doctorAgents reports, for each agent installed for this account, whether
// it has the MCP server and the current skill. With fix it applies the
// missing and outdated items first.
func doctorAgents(e *env, fix bool, ok, warn, bad func(string, ...any)) {
	items := agentsetup.Check(e.port(), e.cfgPath)
	if !agentsetup.Applicable(items) {
		fmt.Fprintf(e.out, "%s no coding agent found (Claude Code, Codex, Gemini CLI)\n", e.paint(dim, "•"))
		return
	}
	for _, it := range items {
		if !it.Applicable {
			continue
		}
		if fix && it.Fixable() {
			r := agentsetup.Apply(e.ctx, []agentsetup.Item{it})[0]
			if r.OK {
				ok("%s added", it.Label)
			} else {
				bad("%s could not be added: %s; run `%s` yourself", it.Label, r.Error, it.Command)
			}
			continue
		}
		switch it.State {
		case agentsetup.OK:
			ok("%s is set up", it.Label)
		case agentsetup.Missing, agentsetup.Outdated:
			warn("%s is %s; `%s doctor --fix` or `%s setup` sets it up (it runs `%s`)", it.Label, it.State, app.ID, app.ID, it.Command)
		default:
			warn("%s: %s; run `%s` yourself", it.Label, it.Detail, it.Command)
		}
	}
}
