// Command portside keeps local dev servers running in one place.
package main

import (
	"os"

	"github.com/padmk/portside/internal/cli"
)

func main() { os.Exit(cli.Main(os.Args[1:])) }
