package keymap

// adder adds an action to the table being built and returns it for its
// details; lister adds a scope's navigation keys.
type (
	adder  func(scope Scope, group, id, keys, help string) *Action
	lister func(scope Scope)
)

// containerActions are the containers pane's actions. The ones that share
// a server action's id (toggle, start, stop, restart, shell, open,
// copy-url) mean the same thing for a container; the root model runs them
// on the container or project the cursor is on.
func containerActions(add adder, list lister) {
	list(ScopeContainers)
	for _, v := range [][3]string{
		{"toggle", "space", "Start or stop"},
		{"restart", "r", "Restart"},
		{"start", "S", "Start"},
		{"stop", "s", "Stop"},
		{"shell", "a", "Shell in the container"},
		{"open", "o", "Open the first published port in the browser"},
		{"copy-url", "y", "Copy the first published port's address"},
		{"recreate", "", "Recreate the service"},
		{"rebuild", "", "Rebuild the image and recreate"},
		{"pull", "", "Pull the image and recreate"},
		{"start-project", "", "Start the project's containers"},
		{"stop-project", "", "Stop the project's containers"},
		{"down-project", "d", "Remove the project's containers (volumes are kept)"},
		{"dismiss-job", "", "Dismiss the finished compose job"},
	} {
		a := add(ScopeContainers, "Containers", v[0], v[1], v[2])
		a.Menu = v[0] != "toggle"
		switch v[0] {
		case "toggle", "restart", "shell", "open":
			a.Footer = true
		case "down-project":
			a.Destructive = true
		}
		switch v[0] {
		case "toggle":
			a.Short = "start/stop"
		case "down-project":
			a.Short = "down"
		}
		// :restart db-1 names the container, :stop-project shop the
		// project; without one they act on the cursor's row.
		a.Arg, a.Optional = ArgContainer, true
		switch v[0] {
		case "start-project", "stop-project", "down-project":
			a.Arg = ArgProject
		}
	}
	add(ScopeContainers, "Containers", "open-detail", "enter", "Show logs, or fold a project")
}

// portActions are the ports pane's actions; open and copy-url mean the
// same as a server's, on http://127.0.0.1:<port>.
func portActions(add adder, list lister) {
	list(ScopePorts)
	for _, v := range [][3]string{
		{"kill", "d", "Kill the process (asks first)"},
		{"takeover", "t", "Start the blocked server, taking over the port"},
		{"open", "o", "Open in browser"},
		{"copy-url", "y", "Copy the address"},
		{"open-detail", "enter", "Select the server or container on this port"},
		{"filter", "/", "Filter"},
		{"back", "esc", "Clear the filter, then close the pane"},
	} {
		a := add(ScopePorts, "Ports", v[0], v[1], v[2])
		switch v[0] {
		case "kill", "takeover", "open", "copy-url":
			a.Menu = true
		}
		switch v[0] {
		case "kill", "open", "filter":
			a.Footer = true
		case "back":
			a.Short = "close"
		}
		if v[0] == "kill" {
			// :kill 3000 or :kill 5141 names the port or the PID.
			a.Destructive, a.Arg, a.Optional = true, ArgPort, true
		}
	}
}
