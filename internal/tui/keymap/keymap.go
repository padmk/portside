// Package keymap is the terminal UI's one table of actions: each action has
// its keys, its command-line name and its help text, so the footer, the
// help overlay, the command line and the menus are
// all generated from the same entries (design/tui.md "Keys").
package keymap

import "fmt"

// Scope is where an action applies; the root model passes the focused
// pane's scope, then ScopeGlobal.
type Scope string

const (
	ScopeGlobal     Scope = "global"
	ScopeServers    Scope = "servers"
	ScopeContainers Scope = "containers"
	ScopePorts      Scope = "ports"
	ScopeLogs       Scope = "logs"
	ScopeDetail     Scope = "detail" // the Details, Env and Metrics tabs
)

// Scopes lists every scope in the order the help overlay shows the ones
// after the focused pane's.
func Scopes() []Scope {
	return []Scope{ScopeGlobal, ScopeServers, ScopeContainers, ScopePorts, ScopeLogs, ScopeDetail}
}

// Title is how the help overlay and the menus name a scope.
func (s Scope) Title() string {
	switch s {
	case ScopeGlobal:
		return "Everywhere"
	case ScopeServers:
		return "Servers"
	case ScopeContainers:
		return "Containers"
	case ScopePorts:
		return "Ports"
	case ScopeLogs:
		return "Logs"
	case ScopeDetail:
		return "Details, Env, Metrics"
	}
	return string(s)
}

// Arg is what an action's one command-line argument names: completion
// offers those names and the command line checks the argument against
// them before it runs anything.
type Arg int

const (
	ArgNone Arg = iota
	ArgServer
	ArgGroup
	ArgServerOrGroup
	ArgContainer
	ArgTheme
	ArgIcons
	ArgState
	ArgComputer
	ArgPort
	ArgText    // free text, e.g. a filter
	ArgProject // a compose project
)

// Noun is how messages name an argument kind ("a server").
func (a Arg) Noun() string {
	return [...]string{"nothing", "a server", "a group", "a server or group", "a container", "a theme", "an icon set", "a state", "a computer", "a port", "text", "a project"}[a]
}

// Action is one thing the user can do.
type Action struct {
	// ID is the action's name and its command: `:restart` runs "restart".
	ID string
	// Keys are key names as tea.KeyPressMsg.String() gives them: "s",
	// "space", "ctrl+d", "shift+down".
	Keys  []string
	Scope Scope
	// Help is the description in the help overlay and the menus; Short is
	// the footer's word ("restart").
	Help, Short string
	// Group is the section in the help overlay.
	Group string
	// Footer puts the action in the footer of its scope, in table order.
	Footer bool
	// Arg is the command line's argument; Optional makes it optional (the
	// selection is used then).
	Arg      Arg
	Optional bool
	// Menu puts the action in the menu of its scope (x or a right click).
	Menu bool
	// Destructive marks an action that removes something; it asks first,
	// and the palette and menus mark it.
	Destructive bool
}

// Command is the action's command line with an argument: "restart web".
// An argument with spaces or quotes is quoted.
func (a Action) Command(arg string) string {
	if arg == "" {
		return a.ID
	}
	return a.ID + " " + Quote(arg)
}

// CheckArgs checks the number of arguments a command line gives an
// action: none for ArgNone, at most one otherwise, exactly one when the
// argument is not optional.
func (a Action) CheckArgs(args []string) error {
	switch {
	case a.Arg == ArgNone && len(args) > 0:
		return fmt.Errorf("%s takes no argument", a.ID)
	case len(args) > 1:
		return fmt.Errorf("%s takes one name, got %d; quote a name with spaces", a.ID, len(args))
	case len(args) == 0 && a.Arg != ArgNone && !a.Optional:
		return fmt.Errorf("%s needs %s", a.ID, a.Arg.Noun())
	}
	return nil
}

// Registry holds the actions in table order.
type Registry struct {
	actions []Action
}

// Add appends actions.
func (r *Registry) Add(a ...Action) { r.actions = append(r.actions, a...) }

// All returns every action in table order.
func (r *Registry) All() []Action { return r.actions }

// Variants returns every action with the id, in table order: "restart"
// is a server's, a group's and a container's.
func (r *Registry) Variants(id string) []Action {
	var out []Action
	for _, a := range r.actions {
		if a.ID == id {
			out = append(out, a)
		}
	}
	return out
}

// ByID returns the first action with the id, in table order (an id in
// several scopes, such as "attach", has the same meaning in each).
func (r *Registry) ByID(id string) (Action, bool) {
	for _, a := range r.actions {
		if a.ID == id {
			return a, true
		}
	}
	return Action{}, false
}

// InScope returns the actions of the scopes, the first scope's first.
func (r *Registry) InScope(scopes ...Scope) []Action {
	var out []Action
	for _, s := range scopes {
		for _, a := range r.actions {
			if a.Scope == s {
				out = append(out, a)
			}
		}
	}
	return out
}

// Lookup returns the action a key runs in the scopes, the first scope's
// first.
func (r *Registry) Lookup(key string, scopes ...Scope) (Action, bool) {
	for _, a := range r.InScope(scopes...) {
		for _, k := range a.Keys {
			if k == key {
				return a, true
			}
		}
	}
	return Action{}, false
}
