package cli

import (
	"fmt"
	"strings"

	"github.com/padmk/portside/internal/server"
)

// exposedListeners names the running servers and published container ports
// that the daemon marks as maybe reachable from the network, such as
// "web (:3000)" and "container db (:5432)".
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

// uncheckedPorts names the running containers with published TCP ports
// whose exposure the daemon could not read, such as "container db (:5432)":
// the ports of an engine on another computer, or of a forwarder that lsof
// does not show (rootful Docker's docker-proxy on Linux). Containers whose
// owning server sets lan = true are left out.
func uncheckedPorts(st server.State) []string {
	lan := map[string]bool{}
	for _, s := range st.Servers {
		lan[s.Name] = s.LAN
	}
	var out []string
	for _, c := range st.Docker.Containers {
		if c.State != "running" || lan[c.Server] {
			continue
		}
		var ps []string
		for _, p := range c.Ports {
			if p.Proto == "tcp" && p.Exposure == "" {
				ps = append(ps, fmt.Sprintf(":%d", p.Public))
			}
		}
		if len(ps) > 0 {
			out = append(out, fmt.Sprintf("container %s (%s)", c.Name, strings.Join(ps, ", ")))
		}
	}
	return out
}

// doctorExposure warns about listeners that may be reachable from the
// network and notes published ports it could not check.
func doctorExposure(st server.State, warn, note func(string, ...any)) {
	if exposed := exposedListeners(st); len(exposed) > 0 {
		warn("may be reachable from the network, so unless a firewall blocks them, other devices on it can connect: %s; bind them to 127.0.0.1 (in Compose, \"127.0.0.1:<host port>:<container port>\"), or set lan = true in the server's config if that is intended", strings.Join(exposed, ", "))
	}
	if unchecked := uncheckedPorts(st); len(unchecked) > 0 {
		note("could not check whether these published ports are reachable from the network (the engine runs elsewhere or its forwarder is hidden from lsof): %s", strings.Join(unchecked, ", "))
	}
}
