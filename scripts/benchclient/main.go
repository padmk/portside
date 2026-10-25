// Command benchclient holds one events connection (/api/events) to the
// daemon on -port open for -for, the way the dashboard (usage shown) or the
// menu bar app with its panel closed (-usage=false) does, and discards what
// the daemon sends. scripts/bench-idle.sh runs it; the daemon's state
// directory comes from the environment, as for the CLI.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/padmk/portside/internal/client"
)

func main() {
	port := flag.Int("port", 0, "daemon port")
	usage := flag.Bool("usage", true, "show resource usage (false connects with usage=0)")
	hold := flag.Duration("for", time.Minute, "how long to stay connected")
	flag.Parse()
	if *port <= 0 {
		fmt.Fprintln(os.Stderr, "benchclient: -port is required")
		os.Exit(2)
	}
	n, err := run(*port, *usage, *hold)
	if err != nil {
		fmt.Fprintln(os.Stderr, "benchclient:", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "benchclient: %d messages in %s\n", n, *hold)
}

// run connects, reads messages until hold has passed, and returns how many
// arrived.
func run(port int, usage bool, hold time.Duration) (int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), hold)
	defer cancel()
	path := "/api/events"
	if !usage {
		path += "?usage=0"
	}
	ws, err := client.New(port).DialWebSocket(ctx, path)
	if err != nil {
		return 0, err
	}
	defer ws.CloseNow()
	ws.SetReadLimit(64 << 20) // a state message lists every server
	n := 0
	for {
		if _, _, err := ws.Read(ctx); err != nil {
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return n, nil
			}
			return n, err
		}
		n++
	}
}
