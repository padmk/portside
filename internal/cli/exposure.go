package cli

import (
	"fmt"
	"strings"

	"github.com/padmk/portside/internal/server"
)

// exposedListeners names the running servers and published container ports
// that the daemon marks as reachable from the network, such as "web (:3000)"
// and "container db (:5432)".
func exposedListeners(st server.State) []string {
	var out []string
	for _, s := range st.Servers {
		if s.NetworkWarning {
			out = append(out, fmt.Sprintf("%s (:%d)", s.Name, s.Port))
		}
	}
	for _, c := range st.Docker.Containers {
		var ps []string
		for _, p := range c.Ports {
			if p.NetworkWarning {
				ps = append(ps, fmt.Sprintf(":%d", p.Public))
			}
		}
		if len(ps) > 0 {
			out = append(out, fmt.Sprintf("container %s (%s)", c.Name, strings.Join(ps, ", ")))
		}
	}
	return out
}

// doctorExposure warns about listeners reachable from the network.
func doctorExposure(st server.State, warn func(string, ...any)) {
	if exposed := exposedListeners(st); len(exposed) > 0 {
		warn("reachable from the network, so other devices on it can connect: %s; bind them to 127.0.0.1 (for Compose, \"127.0.0.1:5432:5432\"), or set lan = true in the server's config if that is intended", strings.Join(exposed, ", "))
	}
}
