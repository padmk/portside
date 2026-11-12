package keymap

import "strings"

// Default is the action table. The keys follow lazygit and lazydocker
// (design/tui.md "Keys"): h and l or the arrows move between panes, [ and ]
// between tabs, J and K scroll the main panel from anywhere, x opens the
// focused pane's menu, ? lists the keys, : runs a command and ctrl+p finds
// an action or a server in the palette.
func Default() *Registry {
	r := &Registry{}
	// keys are the action's keys separated by spaces.
	add := func(scope Scope, group, id, keys, help string) *Action {
		a := Action{Scope: scope, ID: id, Help: help, Short: id, Group: group}
		a.Keys = strings.Fields(keys)
		r.Add(a)
		return &r.actions[len(r.actions)-1]
	}

	// Pane scopes come first: the footer lists the focused pane's hints
	// before the global ones.
	list := func(scope Scope) {
		for _, v := range [][3]string{
			{"down", "j down", "Down"},
			{"up", "k up", "Up"},
			{"next-page", ".", "Next page"},
			{"prev-page", ",", "Previous page"},
			{"top", "< home", "Go to top"},
			{"bottom", "> end", "Go to bottom"},
		} {
			add(scope, "Navigation", v[0], v[1], v[2])
		}
	}

	list(ScopeServers)
	for _, v := range [][3]string{
		{"toggle", "space", "Start or stop"},
		{"restart", "r", "Restart"},
		{"start", "S", "Start"},
		{"stop", "s", "Stop"},
		{"attach", "a", "Attach to its terminal"},
		{"open", "o", "Open in browser"},
		{"copy-url", "y", "Copy URL"},
		// e edits the server in a form; the editor and terminal apps open
		// from the menu and the command line.
		{"edit", "e", "Edit its config entry"},
		{"shell", "E", "Shell in its folder"},
		{"editor", "", "Open folder in editor"},
		{"terminal", "", "Open a terminal app there"},
		{"start-group", "", "Start group"},
		{"stop-group", "", "Stop group"},
		{"restart-group", "", "Restart group"},
		{"toggle-enabled", "", "Enable or disable"},
		{"takeover", "", "Start and take over the port"},
		{"clear-logs", "c", "Clear the daemon's scrollback"},
		{"delete", "d", "Remove from config"},
	} {
		a := add(ScopeServers, "Servers", v[0], v[1], v[2])
		// The menu lists start and stop with their keys; space toggles
		// between them, which the footer says.
		a.Menu, a.Optional = v[0] != "toggle", true
		switch v[0] {
		case "toggle", "start", "stop", "restart":
			a.Arg = ArgServerOrGroup
		case "start-group", "stop-group", "restart-group":
			a.Arg = ArgGroup
		default:
			a.Arg = ArgServer
		}
		switch v[0] {
		case "toggle", "restart", "attach", "open", "edit":
			a.Footer = true
		case "clear-logs", "delete":
			a.Destructive = true
		}
		switch v[0] {
		case "toggle":
			a.Short = "start/stop"
		case "clear-logs":
			a.Short = "clear"
		}
	}
	for _, v := range [][3]string{
		{"open-detail", "enter", "Show logs, or fold a group"},
		{"collapse-all", "-", "Fold all groups"},
		{"expand-all", "=", "Unfold all groups"},
		{"filter", "/", "Filter"},
		{"clear-filter", "esc", "Clear filter"},
	} {
		a := add(ScopeServers, "Servers", v[0], v[1], v[2])
		switch v[0] {
		case "open-detail":
			a.Footer, a.Short = true, "logs"
		case "filter":
			a.Footer = true
		}
	}

	newServer := add(ScopeServers, "Servers", "new", "n", "New server: scan proposals or a form")
	newServer.Menu = true

	list(ScopeLogs)
	for _, v := range [][3]string{
		{"search", "/", "Search"},
		{"next-match", "n", "Next match"},
		{"prev-match", "N", "Previous match"},
		{"keep-matching", "F", "Only matching lines"},
		{"follow", "f", "Follow new output"},
		{"wrap", "w", "Wrap lines"},
		{"timestamps", "t", "Time column"},
		{"visual", "v", "Select lines"},
		{"select-down", "shift+down", "Select down"},
		{"select-up", "shift+up", "Select up"},
		{"yank", "y", "Copy line"},
		{"scroll-left", "H", "Scroll left"},
		{"scroll-right", "L", "Scroll right"},
		{"attach", "a", "Attach to its terminal"},
		{"clear-logs", "c", "Clear the daemon's scrollback"},
		{"back", "esc", "Clear the search, then back"},
	} {
		a := add(ScopeLogs, "Logs", v[0], v[1], v[2])
		switch v[0] {
		case "visual":
			a.Short = "select"
		case "clear-logs":
			a.Short, a.Destructive = "clear", true
		}
		switch v[0] {
		case "search", "follow", "wrap", "visual", "attach":
			a.Footer = true
		}
		switch v[0] {
		case "search", "keep-matching", "follow", "wrap", "timestamps", "visual", "yank", "attach", "clear-logs":
			a.Menu = true
		}
	}

	list(ScopeDetail)
	copyValue := add(ScopeDetail, "Details, Env, Metrics", "copy", "y", "Copy the value under the cursor (:copy on Logs: the line)")
	copyValue.Footer, copyValue.Short = true, "copy"
	add(ScopeDetail, "Details, Env, Metrics", "copy-dir", "", "Copy the directory (:copy-dir from any tab)")
	add(ScopeDetail, "Details, Env, Metrics", "back", "esc", "Back to the list")

	containerActions(add, list)
	portActions(add, list)

	global := func(group, id, keys, help string) *Action { return add(ScopeGlobal, group, id, keys, help) }
	for _, v := range [][3]string{
		{"focus-left", "h left", "Pane on the left"},
		{"focus-right", "l right", "Pane on the right"},
		{"focus-next", "tab", "Next pane"},
		{"focus-prev", "shift+tab", "Previous pane"},
		{"focus-servers", "1", "Servers"},
		{"focus-containers", "2", "Containers (when Docker has some)"},
		{"focus-ports", "3", "Ports (opens the pane)"},
		{"focus-main", "0", "Main panel"},
		{"tab-next", "]", "Next tab"},
		{"tab-prev", "[", "Previous tab"},
		{"screen-next", "+", "Next screen mode (normal, half, full)"},
		{"screen-prev", "_", "Previous screen mode"},
	} {
		global("Panes", v[0], v[1], v[2])
	}
	for _, v := range [][3]string{
		{"scroll-down", "J", "Scroll down"},
		{"scroll-up", "K", "Scroll up"},
		{"half-down", "ctrl+d", "Half page down"},
		{"half-up", "ctrl+u", "Half page up"},
		{"page-down", "pgdown", "Page down"},
		{"page-up", "pgup", "Page up"},
	} {
		global("Main panel", v[0], v[1], v[2])
	}
	menu := global("General", "menu", "x", "Menu of actions")
	menu.Footer = true
	help := global("General", "help", "?", "Keys")
	help.Footer, help.Short = true, "keys"
	global("General", "command", ":", "Command line")
	global("General", "palette", "ctrl+p", "Find an action or a server")
	theme := global("General", "theme", "T", "Next theme")
	theme.Arg, theme.Optional = ArgTheme, true
	icons := global("General", "icons", "", "Next icon set")
	icons.Arg, icons.Optional = ArgIcons, true
	global("General", "reconnect", "R", "Reconnect")
	global("Config", "config", "", "Edit the config in your editor").Menu = true
	global("Config", "settings", "", "Settings").Menu = true
	scan := global("Config", "scan", "", "Scan a folder for projects")
	scan.Arg, scan.Optional = ArgText, true
	global("General", "messages", "", "Messages")
	global("General", "ports", "", "Show or hide the listening ports")
	global("General", "start-docker", "", "Start the container engine")
	global("General", "filter-state", "", "Filter by state").Arg = ArgState
	systemActions(global)
	global("General", "quit", "q ctrl+c", "Quit")
	return r
}
