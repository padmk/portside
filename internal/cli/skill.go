package cli

import (
	"errors"
	"flag"
	"fmt"

	"github.com/padmk/portside/internal/agentskill"
	"github.com/padmk/portside/internal/paths"
)

// cmdSkill prints the agent skill or installs it for Claude Code and Codex.
func cmdSkill(e *env, args []string) error {
	fs := flag.NewFlagSet("skill", flag.ContinueOnError)
	force := fs.Bool("force", false, "")
	words, err := parse(fs, args)
	if err != nil {
		return err
	}
	content := agentskill.Render(e.port(), e.cfgPath)
	switch {
	case len(words) == 1 && words[0] == "print":
		fmt.Fprint(e.out, content)
		return nil
	case len(words) == 1 && words[0] == "install":
	default:
		return usageError{""}
	}
	targets := agentskill.Targets()
	if len(targets) == 0 {
		return errors.New("neither Claude Code (~/.claude) nor Codex (~/.codex) is set up for this account; use `skill print` to get the file")
	}
	var errs []error
	for _, path := range targets {
		changed, err := agentskill.Install(path, content, *force)
		switch {
		case err != nil:
			errs = append(errs, err)
		case changed:
			fmt.Fprintf(e.out, "%s Installed %s\n", e.paint(green, "✓"), paths.ShortenHome(path))
		default:
			fmt.Fprintf(e.out, "%s %s is up to date\n", e.paint(green, "✓"), paths.ShortenHome(path))
		}
	}
	return errors.Join(errs...)
}
