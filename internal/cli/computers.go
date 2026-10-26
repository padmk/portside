package cli

import (
	"errors"
	"flag"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/padmk/portside/internal/agentsetup"
	"github.com/padmk/portside/internal/app"
	"github.com/padmk/portside/internal/client"
	"github.com/padmk/portside/internal/remote"
	"github.com/padmk/portside/internal/server"
	"github.com/padmk/portside/internal/supervisor"
)

// Other computers: thin wrappers over /api/remote. The connection belongs
// to the daemon, so the dashboard follows what these commands do.

// localize rewrites the URLs of a server on the connected computer to the
// ones that open it from here.
func (e *env) localize(v *supervisor.View) {
	if e.conn == nil {
		return
	}
	if v.URL != "" {
		v.URL = e.conn.LocalURL(v.Name, v.URL)
	}
	if v.Alias != "" {
		v.Alias = e.conn.LocalURL(v.Name, v.Alias)
	}
}

// cmdComputers lists the owner's other computers that share their daemon,
// and the current connection.
func cmdComputers(e *env, args []string) error {
	fs := flag.NewFlagSet("computers", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "")
	printPrompt := fs.Bool("print-prompt", false, "")
	if pos, err := parse(fs, args); err != nil {
		return err
	} else if len(pos) > 0 {
		return usageError{}
	}
	if *printPrompt {
		fmt.Fprint(e.out, agentsetup.SharePrompt())
		return nil
	}
	c, err := e.client()
	if err != nil {
		return err
	}
	var d remote.Discovery
	if err := c.Do(e.ctx, http.MethodGet, "/api/remote/peers", nil, &d); err != nil {
		return err
	}
	var ov server.RemoteOverview
	if err := c.Do(e.ctx, http.MethodGet, "/api/remote", nil, &ov); err != nil {
		return err
	}
	if *asJSON {
		if d.Peers == nil {
			d.Peers = []remote.Peer{}
		}
		return printJSON(e, map[string]any{"tailnet": d.Tailnet, "computers": d.Peers, "connection": ov.Connection})
	}
	switch {
	case !d.Tailnet.Available:
		fmt.Fprintf(e.errOut, "%s %s\n%s computers --print-prompt prints a request that has your coding agent set this up.\n", e.paint(red, "●"), d.Tailnet.Error, app.ID)
	case len(d.Peers) == 0:
		fmt.Fprintf(e.out, "No other computer shares its servers. Turn sharing on there with `%s share on`;\nboth computers need the same Tailscale account.\n", app.ID)
	}
	nameW := 4
	for _, p := range d.Peers {
		nameW = max(nameW, len(p.Name))
	}
	for _, p := range d.Peers {
		dot, state := e.paint(dim, "○"), "not paired"
		switch {
		case p.Connected:
			dot, state = e.paint(green, "●"), "connected"
		case p.Paired:
			state = "paired"
		}
		servers := ""
		if p.Servers != nil {
			servers = fmt.Sprintf("%d of %d running", p.Servers.Running, p.Servers.Total)
		}
		fmt.Fprintf(e.out, "  %s %-*s  %-10s  %-16s %s\n", dot, nameW, p.Name, state, servers, e.paint(dim, p.DNSName))
	}
	if cn := ov.Connection; cn != nil {
		fmt.Fprintln(e.out)
		printConnection(e, cn)
	}
	return nil
}

// printConnection describes the connection and its forwarded ports.
func printConnection(e *env, cn *remote.Info) {
	switch cn.Status {
	case remote.Open:
		fmt.Fprintf(e.out, "%s connected to %s since %s\n", e.paint(green, "●"), cn.Peer.Name, cn.Since.Local().Format("15:04"))
	default:
		fmt.Fprintf(e.out, "%s %s to %s\n", e.paint(yellow, "◐"), cn.Status, cn.Peer.Name)
	}
	if cn.Error != "" {
		fmt.Fprintf(e.out, "  %s\n", e.paint(dim, cn.Error))
	}
	var ports []string
	for _, m := range cn.Mirrors {
		p := strconv.Itoa(m.Port)
		if m.Local != 0 {
			p += "->" + strconv.Itoa(m.Local)
		}
		switch m.State {
		case "off":
			p += " (off)"
		case "busy":
			p += " (busy here)"
		}
		ports = append(ports, p)
	}
	if len(ports) > 0 {
		fmt.Fprintf(e.out, "  %s %s\n", e.paint(dim, "forwarded ports"), strings.Join(ports, ", "))
	}
	fmt.Fprintf(e.out, "  %s\n", e.paint(dim, fmt.Sprintf("--on %s runs %s there", cn.Peer.Label, remoteList())))
}

// cmdForward turns forwarding of a port of the connected computer on or off.
func cmdForward(e *env, args []string) error {
	if len(args) != 2 || (args[0] != "on" && args[0] != "off") {
		return usageError{}
	}
	port, err := strconv.Atoi(args[1])
	if err != nil || port <= 0 || port > 65535 {
		return usageError{"the port is a number from 1 to 65535"}
	}
	c, err := e.client()
	if err != nil {
		return err
	}
	var info remote.Info
	if err := c.Do(e.ctx, http.MethodPut, "/api/remote/forwarding", map[string]any{"port": port, "on": args[0] == "on"}, &info); err != nil {
		return err
	}
	printConnection(e, &info)
	return nil
}

// cmdPair redeems a pairing code shown on another computer and connects.
func cmdPair(e *env, args []string) error {
	if len(args) != 2 {
		return usageError{fmt.Sprintf("get a code on the other computer: Computers in its dashboard, or `%s share code`", app.ID)}
	}
	return connect(e, "/api/remote/pair", map[string]string{"peer": args[0], "code": args[1]}, args[0])
}

// cmdConnect connects to a paired computer.
func cmdConnect(e *env, args []string) error {
	if len(args) != 1 {
		return usageError{}
	}
	return connect(e, "/api/remote/connect", map[string]string{"peer": args[0]}, args[0])
}

func connect(e *env, path string, body map[string]string, name string) error {
	c, err := e.client()
	if err != nil {
		return err
	}
	var info remote.Info
	if err := c.Do(e.ctx, http.MethodPost, path, body, &info); err != nil {
		var apiErr *client.APIError
		if errors.As(err, &apiErr) && apiErr.Code == remote.CodePairingRequired {
			return fmt.Errorf("%s\nGet a code on %s (Computers in its dashboard, or `%s share code`) and run `%s pair %s <code>`", err, name, app.ID, app.ID, name)
		}
		return err
	}
	printConnection(e, &info)
	return nil
}

// cmdDisconnect ends the connection to another computer.
func cmdDisconnect(e *env, args []string) error {
	if len(args) != 0 {
		return usageError{}
	}
	c, err := e.client()
	if err != nil {
		return err
	}
	var ov server.RemoteOverview
	if err := c.Do(e.ctx, http.MethodGet, "/api/remote", nil, &ov); err != nil {
		return err
	}
	if err := c.Do(e.ctx, http.MethodPost, "/api/remote/disconnect", nil, nil); err != nil {
		return err
	}
	if ov.Connection == nil {
		fmt.Fprintln(e.out, "Not connected to another computer.")
		return nil
	}
	fmt.Fprintf(e.out, "%s disconnected from %s\n", e.paint(dim, "○"), ov.Connection.Peer.Name)
	return nil
}
