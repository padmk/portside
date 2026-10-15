package cli

import (
	"github.com/padmk/portside/internal/client"
	"github.com/padmk/portside/internal/mcpserver"
)

func cmdMCP(e *env, args []string) error {
	return mcpserver.Run(e.ctx, client.New(e.port()))
}
