package cli

import (
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/coder/websocket"

	"github.com/padmk/portside/internal/client"
	"github.com/padmk/portside/internal/docker"
	"github.com/padmk/portside/internal/supervisor"
)

// cmdContainers lists the containers on the active engine and the server
// each belongs to.
func cmdContainers(e *env, args []string) error {
	fs := flag.NewFlagSet("containers", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "")
	names, err := parse(fs, args)
	if err != nil {
		return err
	}
	st, err := e.state()
	if err != nil {
		return err
	}
	d := st.Docker
	shown := d.Containers[:0]
	for _, c := range d.Containers {
		if len(names) > 0 && !containsAny(names, append(c.Servers(), c.Project)...) {
			continue
		}
		c.CPUHistory, c.MemHistory = nil, nil
		shown = append(shown, c)
	}
	d.Containers = shown
	if *asJSON {
		return printJSON(e, d)
	}
	if d.Status != docker.Ready {
		msg := d.Status
		if d.Error != "" {
			msg += ": " + d.Error
		}
		fmt.Fprintf(e.errOut, "%s %s (%s)\n", e.paint(red, "docker"), msg, d.Engine)
		return nil
	}
	if len(shown) == 0 {
		fmt.Fprintln(e.out, "No containers.")
		return nil
	}
	nameW, serverW := 4, 1
	for _, c := range shown {
		nameW, serverW = max(nameW, len(c.Name)), max(serverW, len(c.Server))
	}
	for _, c := range shown {
		dot := e.paint(dim, "○")
		switch {
		case c.Failed, c.State == "restarting", c.State == "running" && c.Health == "unhealthy":
			dot = e.paint(red, "●")
		case c.State == "running":
			dot = e.paint(green, "●")
		}
		server := c.Server
		if server == "" {
			server = "-"
		}
		fmt.Fprintf(e.out, "  %s %-*s  %-*s  %s\n", dot, nameW, c.Name, serverW, server, c.Status)
	}
	return nil
}

func containsAny(names []string, values ...string) bool {
	for _, n := range names {
		for _, v := range values {
			if v != "" && strings.EqualFold(n, v) {
				return true
			}
		}
	}
	return false
}

// containerLogs prints, searches or follows a container's output for the
// logs command.
func containerLogs(e *env, c *client.Client, name string, q url.Values, follow bool) error {
	path := "/api/containers/" + url.PathEscape(name) + "/logs"
	if !follow {
		return c.Do(e.ctx, http.MethodGet, path+"?"+q.Encode(), nil, e.out)
	}
	ws, _, err := websocket.Dial(e.ctx, "ws://127.0.0.1:"+strconv.Itoa(c.Port)+path, &websocket.DialOptions{HTTPHeader: c.Header()})
	if err != nil {
		return err
	}
	defer ws.CloseNow()
	ws.SetReadLimit(-1)
	plain := !isTerminal(os.Stdout) || !e.color
	for {
		_, data, err := ws.Read(e.ctx)
		if err != nil {
			return nil
		}
		if plain {
			data = []byte(supervisor.StripANSI(strings.ReplaceAll(string(data), "\r\n", "\n")))
		}
		_, _ = e.out.Write(data)
	}
}
