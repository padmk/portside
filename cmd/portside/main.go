// Command portside keeps local dev servers running in one place.
package main

import (
	"os"

	"github.com/padmk/portside/internal/app"
	"github.com/padmk/portside/internal/cli"
)

// version is set at build time with -ldflags "-X main.version=1.2.3".
var version string

func main() {
	if version != "" {
		app.Version = version
	}
	os.Exit(cli.Main(os.Args[1:]))
}
