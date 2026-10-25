package cli

import (
	"flag"

	"github.com/padmk/portside/internal/client"
	"github.com/padmk/portside/internal/mcpserver"
)

func cmdMCP(e *env, args []string) error {
	fs := flag.NewFlagSet("mcp", flag.ContinueOnError)
	readOnly := fs.Bool("read-only", false, "")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(pos) > 0 {
		return usageError{}
	}
	return mcpserver.Run(e.ctx, client.New(e.port()), mcpserver.Options{ReadOnly: *readOnly})
}
