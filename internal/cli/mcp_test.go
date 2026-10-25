package cli

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestMCPModeFlags(t *testing.T) {
	cfg, _, _ := isolateDaemon(t)
	for _, args := range [][]string{
		{"--read-only", "--mode", "full"},
		{"--mode", "start", "--read-only"},
		{"--mode", "write"},
	} {
		e, _, _ := testEnv(cfg)
		// A command that got past its flags would serve MCP; a cancelled
		// context ends that at once instead of reading the test's stdin.
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		e.ctx = ctx
		err := cmdMCP(e, args)
		var ue usageError
		if !errors.As(err, &ue) {
			t.Errorf("mcp %s: %v, want a usage error", strings.Join(args, " "), err)
		}
	}
}
