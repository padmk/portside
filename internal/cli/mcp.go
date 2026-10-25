package cli

import (
	"flag"
	"strings"

	"github.com/padmk/portside/internal/client"
	"github.com/padmk/portside/internal/mcpserver"
)

// listFlag collects a repeatable string flag.
type listFlag []string

func (l *listFlag) String() string     { return strings.Join(*l, ",") }
func (l *listFlag) Set(v string) error { *l = append(*l, v); return nil }

func cmdMCP(e *env, args []string) error {
	fs := flag.NewFlagSet("mcp", flag.ContinueOnError)
	modeName := fs.String("mode", string(mcpserver.Full), "")
	readOnly := fs.Bool("read-only", false, "")
	var dirs listFlag
	fs.Var(&dirs, "dir", "")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(pos) > 0 {
		return usageError{}
	}
	mode, err := mcpMode(*modeName)
	if err != nil {
		return err
	}
	if *readOnly {
		if mode != mcpserver.ReadOnly && flagSet(fs, "mode") {
			return usageError{"--read-only is --mode read-only; give one of them"}
		}
		mode = mcpserver.ReadOnly
	}
	return mcpserver.Run(e.ctx, client.New(e.port()), mcpserver.Options{Mode: mode, Dirs: dirs})
}

// mcpMode parses a --mode value as a usage error when it is unknown.
func mcpMode(name string) (mcpserver.Mode, error) {
	m, err := mcpserver.ParseMode(name)
	if err != nil {
		return "", usageError{err.Error()}
	}
	return m, nil
}

// flagSet reports whether the named flag was given.
func flagSet(fs *flag.FlagSet, name string) bool {
	given := false
	fs.Visit(func(f *flag.Flag) { given = given || f.Name == name })
	return given
}
