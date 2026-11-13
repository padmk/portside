// Package ports is the terminal UI's ports pane (design/tui.md "Ports"):
// the processes listening on TCP ports as the daemon reports them
// (GET /api/listeners), what each is in the daemon's terms, and killing
// one or taking its port over for the server it blocks.
package ports

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/padmk/portside/internal/client"
	"github.com/padmk/portside/internal/server"
	"github.com/padmk/portside/internal/supervisor"
	"github.com/padmk/portside/internal/tui/ui"
)

// Listener is one listening process with what the daemon knows of it.
type Listener = server.ListenerView

// Item names a listener.
func Item(l Listener) ui.Selection { return ui.PortItem(l.PID, l.Port) }

// Role is what a port is in the daemon's terms.
type Role int

const (
	RoleOther     Role = iota
	RoleManaged        // a managed server's own process
	RoleContainer      // a container's published port
	RoleBlocks         // a configured server's port, held by another process
)

// RoleOf is a listener's role; RoleManaged needs a server and a process the
// daemon started (not the daemon itself, which is protected).
func RoleOf(l Listener) Role {
	switch {
	case l.Container != nil:
		return RoleContainer
	case l.Managed && l.Server != "" && l.Via == "" && l.Protected == "":
		return RoleManaged
	case l.Server != "" && !l.Managed:
		return RoleBlocks
	}
	return RoleOther
}

// Describe says what the port is: "server web", "container db (shop)",
// "blocks web", or the protection's reason for the daemon itself.
func Describe(l Listener) string {
	switch RoleOf(l) {
	case RoleManaged:
		return "server " + l.Server
	case RoleContainer:
		c := l.Container
		name := c.Name
		if c.Service != "" {
			name = c.Service
		}
		if c.Server != "" {
			return "container " + name + " (" + c.Server + ")"
		}
		return "container " + name
	case RoleBlocks:
		return "blocks " + l.Server
	case RoleOther:
	}
	if l.Managed && l.Server != "" {
		return l.Server
	}
	return "unmanaged"
}

// Reach is where the listener can be reached from: this computer only
// (loopback), all interfaces, or the addresses it binds.
func Reach(l Listener) string {
	local := len(l.Addresses) > 0
	for _, a := range l.Addresses {
		switch a {
		case "*", "0.0.0.0", "[::]", "::":
			return "all interfaces"
		}
		if !loopback(a) {
			local = false
		}
	}
	if local {
		return "local"
	}
	return strings.Join(l.Addresses, " ")
}

func loopback(a string) bool {
	a = strings.Trim(a, "[]")
	return a == "::1" || a == "localhost" || strings.HasPrefix(a, "127.")
}

// findServer finds a server by name in the snapshot.
func findServer(env *ui.Env, name string) (supervisor.View, bool) {
	if env.State != nil {
		for _, v := range env.State.Servers {
			if v.Name == name {
				return v, true
			}
		}
	}
	return supervisor.View{}, false
}

// URL is the port's address as it opens from here: on this computer
// http://127.0.0.1:<port>; on another computer its forwarded port, or the
// proxy of the server whose configured port it is (ui.Session.PortURL),
// else "".
func URL(env *ui.Env, l Listener) string {
	if !env.Session.Remote() {
		return "http://127.0.0.1:" + strconv.Itoa(l.Port)
	}
	serverPort := 0
	if v, ok := findServer(env, l.Server); ok {
		serverPort = v.Port
	}
	return env.Session.PortURL(l.Server, serverPort, l.Port)
}

// Off is why action id does nothing on listener l, "" when it acts. Kill
// on a managed server's port stops the server instead (KillStops).
func Off(env *ui.Env, id string, l Listener) string {
	switch id {
	case "kill":
		if RoleOf(l) == RoleManaged {
			if v, ok := findServer(env, l.Server); !ok || !v.CanStop() {
				return l.Server + " is not running"
			}
			return ""
		}
		if l.Protected != "" {
			return "protected: " + l.Protected
		}
	case "takeover":
		if RoleOf(l) != RoleBlocks {
			return "no configured server waits for this port"
		}
		return TakeoverOff(env, l.Server)
	case "open", "copy-url":
		if URL(env, l) == "" {
			return env.Session.NoAddress(l.Port)
		}
	case "open-detail":
		switch RoleOf(l) {
		case RoleOther:
			return "no server or container of the daemon's uses this port"
		case RoleManaged, RoleContainer, RoleBlocks:
		}
	default:
		return "not for a port"
	}
	return ""
}

// TakeoverOff is why a server cannot start taking over its port: only a
// server whose port the daemon reports held by another process can, shown
// as waiting for it or blocked by a start that found it taken, as the
// dashboard's port banner offers it.
func TakeoverOff(env *ui.Env, name string) string {
	v, ok := findServer(env, name)
	switch {
	case !ok:
		return "no server named " + name
	case v.External == nil || v.HasProcess():
		return "its port is not held by another process"
	case v.External.PID == 0:
		// Nothing to confirm: the daemon would stop whatever it finds.
		return fmt.Sprintf("port %d is taken, but the process holding it is not known", v.External.Port)
	}
	return ""
}

// KillStops reports whether kill on l stops a managed server.
func KillStops(l Listener) bool { return RoleOf(l) == RoleManaged }

// ConfirmKill asks before killing the process on a port, naming it, its
// PID and the port; yes runs it.
func ConfirmKill(l Listener, yes tea.Cmd) tea.Cmd {
	return func() tea.Msg {
		return ui.ConfirmMsg{
			Title:   fmt.Sprintf("Kill the process on port %d?", l.Port),
			Body:    fmt.Sprintf("%s (pid %d) will be terminated. Unsaved work in that process is lost.", l.Command, l.PID),
			Confirm: "Kill",
			Yes:     yes,
		}
	}
}

// Kill asks the daemon to kill the process on a port as operation op; the
// daemon checks again that it listens there and may be killed.
func Kill(c *client.Client, op int, l Listener) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		err := c.Do(ctx, http.MethodPost, "/api/listeners/kill", map[string]int{"pid": l.PID, "port": l.Port}, nil)
		return ui.OpDoneMsg{ID: op, Label: fmt.Sprintf("kill %s on port %d", l.Command, l.Port), Items: []ui.Selection{Item(l)}, Err: err}
	}
}

// ConfirmTakeover asks before a takeover stops holder, the process on the
// port server is configured for, naming it, its PID, the port and what
// happens; yes runs it. Cancel is the dialog's default.
func ConfirmTakeover(server string, holder Listener, yes tea.Cmd) tea.Cmd {
	return func() tea.Msg {
		return ui.ConfirmMsg{
			Title: fmt.Sprintf("Take over port %d for %s?", holder.Port, server),
			Body: fmt.Sprintf("%s (pid %d) holds port %d, which %s is configured for. It is sent SIGTERM, and SIGKILL if it has not ended after 5 seconds; then %s starts on the port. Unsaved work in that process is lost.",
				holder.Command, holder.PID, holder.Port, server, server),
			Confirm: "Take over",
			Yes:     yes,
		}
	}
}

// Takeover starts a server telling the daemon to take over its port from
// holder, the process the user confirmed, as operation op. It first reads
// the port's owner again and stops if another process holds it now; the
// daemon checks once more (takeoverPid) right before it signals.
func Takeover(c *client.Client, op int, name string, holder Listener) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		done := ui.OpDoneMsg{ID: op, Label: "take over the port for " + name, Items: []ui.Selection{ui.ServerItem(name)}}
		var body struct {
			Listeners []Listener `json:"listeners"`
		}
		if err := c.Do(ctx, http.MethodGet, "/api/listeners", nil, &body); err != nil {
			done.Err = fmt.Errorf("could not check who holds port %d, so nothing was stopped: %w", holder.Port, err)
			return done
		}
		if other := changedHolder(body.Listeners, holder); other != nil {
			done.Err = fmt.Errorf("port %d is now held by %s (pid %d), not the process you confirmed; nothing was stopped", other.Port, other.Command, other.PID)
			return done
		}
		done.Err = c.Do(ctx, http.MethodPost, "/api/servers/"+url.PathEscape(name)+"/start", map[string]any{"takeover": true, "takeoverPid": holder.PID}, nil)
		return done
	}
}

// changedHolder is a process listening on holder's port when holder no
// longer does, nil while it still holds the port or the port is free.
// Several processes can listen on one socket (a prefork server and its
// workers), so the port changed hands only when holder is not among them.
func changedHolder(listeners []Listener, holder Listener) *Listener {
	var other *Listener
	for _, l := range listeners {
		if l.Port != holder.Port {
			continue
		}
		if l.PID == holder.PID {
			return nil
		}
		if other == nil {
			other = &l
		}
	}
	return other
}

// PaletteActions are the port actions the palette lists for each port;
// a takeover is the blocked server's entry.
var PaletteActions = []string{"kill", "open", "copy-url"}

// Label names action id on a port for the palette and the menu: "Kill
// node 5141 on :3016", "Stop server web on :3000" where kill stops a
// managed server.
func Label(id string, l Listener) string {
	port := ":" + strconv.Itoa(l.Port)
	switch id {
	case "kill":
		if KillStops(l) {
			return "Stop server " + l.Server + " on " + port
		}
		return fmt.Sprintf("Kill %s %d on %s", l.Command, l.PID, port)
	case "open":
		return "Open " + port + " in the browser"
	case "copy-url":
		return "Copy the address of " + port
	}
	return id + " " + port
}
