package cli

import (
	"errors"
	"flag"
	"os"

	"github.com/padmk/portside/internal/client"
	"github.com/padmk/portside/internal/tui"
)

func cmdTUI(e *env, args []string) error {
	fs := flag.NewFlagSet("tui", flag.ContinueOnError)
	themeName := fs.String("theme", "", "")
	icons := fs.String("icons", "", "")
	rest, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(rest) > 0 {
		return usageError{"tui takes no arguments"}
	}
	o := tui.Options{Theme: *themeName, Icons: *icons}
	if err := o.Validate(); err != nil {
		return usageError{err.Error()}
	}
	if !isTerminal(os.Stdin) || !isTerminal(os.Stdout) {
		return errors.New("tui needs an interactive terminal on standard input and output; use `status` or `logs` instead")
	}
	c, err := e.client()
	if err != nil {
		return err
	}
	o.Computer = e.on
	if e.conn != nil {
		o.Computer, o.Conn = e.conn.Peer.Name, e.conn
		// The computers, the daemon and the agent setup act on this
		// computer's daemon, at the port c reaches it through.
		o.Local = client.New(c.Port)
	}
	return tui.Run(e.ctx, c, o)
}
