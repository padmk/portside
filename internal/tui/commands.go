package tui

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/padmk/portside/internal/supervisor"
	"github.com/padmk/portside/internal/tui/containers"
	"github.com/padmk/portside/internal/tui/keymap"
	"github.com/padmk/portside/internal/tui/overlay"
	"github.com/padmk/portside/internal/tui/theme"
	"github.com/padmk/portside/internal/tui/ui"
	"github.com/padmk/portside/internal/viewfmt"
)

// cmdHistoryLimit is how many commands the session keeps for up and down.
const cmdHistoryLimit = 100

// openCmdLine opens an empty command line over the session's history.
func (m *model) openCmdLine() {
	m.cmdLine = overlay.NewCmdLine(slices.Clone(m.cmdHistory), func(line string, cursor int) keymap.Completion {
		return m.env.Keys.Complete(line, cursor, m.names())
	})
	m.mode = ui.ModeCommand
}

func (m *model) closeCmdLine() {
	m.cmdLine = nil
	m.mode = ui.ModeNormal
}

// cmdKey handles a key on the command line.
func (m *model) cmdKey(msg tea.KeyPressMsg) tea.Cmd {
	c := m.cmdLine
	switch k := msg.String(); k {
	case "esc":
		m.closeCmdLine()
	case "enter":
		line := strings.TrimSpace(c.Value())
		m.closeCmdLine()
		if line == "" {
			return nil
		}
		m.keepCommand(line)
		return m.execute(line)
	case "backspace":
		if c.Value() == "" {
			m.closeCmdLine()
			return nil
		}
		c.Backspace()
	case "left":
		c.Left()
	case "right":
		c.Right()
	case "home", "ctrl+a":
		c.Home()
	case "end", "ctrl+e":
		c.End()
	case "ctrl+w":
		c.DeleteWord()
	case "up":
		c.Prev()
	case "down":
		c.Next()
	case "tab":
		c.Complete(false)
	case "shift+tab":
		c.Complete(true)
	case "space":
		c.Insert(" ")
	default:
		key := msg.Key()
		if key.Text != "" && key.Mod&(tea.ModCtrl|tea.ModAlt|tea.ModSuper) == 0 {
			c.Insert(key.Text)
		}
	}
	return nil
}

// keepCommand adds a command to the session's history, once in a row.
func (m *model) keepCommand(line string) {
	if n := len(m.cmdHistory); n > 0 && m.cmdHistory[n-1] == line {
		return
	}
	m.cmdHistory = append(m.cmdHistory, line)
	if len(m.cmdHistory) > cmdHistoryLimit {
		m.cmdHistory = m.cmdHistory[len(m.cmdHistory)-cmdHistoryLimit:]
	}
}

// names are the command line's completions for arguments.
func (m *model) names() keymap.Names {
	var n keymap.Names
	for _, f := range theme.Families() {
		n.Themes = append(n.Themes, f.ID, f.ID+"-light", f.ID+"-dark")
	}
	for _, g := range theme.GlyphSets() {
		n.Icons = append(n.Icons, g.ID)
	}
	n.States = stateNames()
	n.Computers = m.sys.Names()
	st := m.env.State
	if st == nil {
		return n
	}
	for _, v := range viewfmt.Ordered(st) {
		n.Servers = append(n.Servers, v.Name)
	}
	for _, g := range viewfmt.Groups(st) {
		if g.Name != "" && !slices.Contains(n.Groups, g.Name) {
			n.Groups = append(n.Groups, g.Name)
		}
	}
	for _, c := range st.Docker.Containers {
		n.Containers = append(n.Containers, c.Name)
	}
	for _, p := range containers.Projects(st) {
		if p.Name != "" {
			n.Projects = append(n.Projects, p.Name)
		}
	}
	return n
}

// execute runs a command line, after checking its argument: how many the
// command takes and that a name names something of the kind it takes.
func (m *model) execute(line string) tea.Cmd {
	id, args, err := keymap.ParseCommand(line)
	if err != nil {
		return m.fail(err.Error())
	}
	a, err := m.env.Keys.Command(id)
	if err != nil {
		return m.fail(err.Error())
	}
	a = m.commandVariant(a, args)
	if err := a.CheckArgs(args); err != nil {
		return m.fail(err.Error())
	}
	arg := ""
	if len(args) == 1 {
		arg = args[0]
		if err := m.checkName(a, arg); err != nil {
			return m.fail(err.Error())
		}
	}
	return m.command(a, arg)
}

// commandVariant is the action a command line means among those that share its
// id (keymap.Registry.Variants): with a name, the first whose argument
// kind has something of that name (:restart db-1 is the container's), the
// focused pane's first; without one, the focused pane's when it has the
// id, else the first, which acts on the cursor's target. When no variant
// takes the name, the first that takes a name explains why.
func (m *model) commandVariant(a keymap.Action, args []string) keymap.Action {
	vs := m.env.Keys.Variants(a.ID)
	scope := m.focused().Scope()
	slices.SortStableFunc(vs, func(x, y keymap.Action) int {
		return b2i(y.Scope == scope) - b2i(x.Scope == scope)
	})
	if len(args) != 1 {
		for _, v := range vs {
			if v.Scope == scope {
				return v
			}
		}
		return a
	}
	for _, v := range vs {
		if v.Arg != keymap.ArgNone && m.checkName(v, args[0]) == nil {
			return v
		}
	}
	for _, v := range vs {
		if v.Arg != keymap.ArgNone {
			return v
		}
	}
	return a
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// checkName checks that arg names something of the kind a takes, and
// names the closest one when it does not.
func (m *model) checkName(a keymap.Action, arg string) error {
	n := m.names()
	choices := n.For(a.Arg)
	switch a.Arg {
	case keymap.ArgText, keymap.ArgNone, keymap.ArgPort, keymap.ArgComputer:
		return nil
	case keymap.ArgState:
		choices = append(slices.Clone(choices), "all")
	default:
	}
	if slices.Contains(choices, arg) {
		return nil
	}
	what := map[keymap.Arg]string{
		keymap.ArgServer: "no server named %q", keymap.ArgGroup: "no group named %q",
		keymap.ArgServerOrGroup: "no server or group named %q", keymap.ArgTheme: "unknown theme %q",
		keymap.ArgIcons: "unknown icons %q", keymap.ArgState: "unknown state %q",
		keymap.ArgContainer: "no container named %q", keymap.ArgProject: "no compose project named %q",
	}[a.Arg]
	msg := fmt.Sprintf(what, arg)
	if a.Arg == keymap.ArgGroup && m.hasServer(arg) {
		v, _ := m.server(arg)
		if v.Group != "" {
			return fmt.Errorf("%s; %s is a server in group %s", msg, arg, v.Group)
		}
		return fmt.Errorf("%s; %s is a server outside any group", msg, arg)
	}
	if c := keymap.Closest(arg, choices); c != "" {
		return fmt.Errorf("%s; did you mean %s?", msg, c)
	}
	if a.Arg == keymap.ArgState || a.Arg == keymap.ArgIcons {
		return fmt.Errorf("%s; one of %s", msg, strings.Join(choices, ", "))
	}
	return errors.New(msg)
}

// fail shows an error notice.
func (m *model) fail(text string) tea.Cmd {
	return m.show(ui.NoticeMsg{Text: text, Level: ui.LevelError}, noticeTime)
}

// command runs an action from the command line with its argument, or
// without one as its keys do.
func (m *model) command(a keymap.Action, arg string) tea.Cmd {
	if cmd, ok := m.systemCommand(a, arg); ok {
		return cmd
	}
	switch a.ID {
	case "theme":
		if arg != "" {
			return m.setTheme(arg)
		}
	case "icons":
		if arg != "" {
			return m.setIcons(arg)
		}
	case "filter-state":
		return m.filterState(arg)
	case "scan":
		return m.formAction("scan", arg)
	}
	if a.Scope == keymap.ScopeServers && formActions[a.ID] {
		return m.formAction(a.ID, arg)
	}
	if a.Scope == keymap.ScopeServers && targetActions[a.ID] {
		return m.act(a.ID, m.commandTarget(a, arg))
	}
	if cmd, ok := m.argCommand(a, arg); ok {
		return cmd
	}
	if a.Scope == keymap.ScopeContainers && arg != "" {
		if a.Arg == keymap.ArgProject {
			return m.act(a.ID, ui.ProjectItem(arg))
		}
		if c, ok := containers.Find(m.env.State, ui.ContainerItem("", arg)); ok {
			return m.act(a.ID, containers.Item(c))
		}
		return m.act(a.ID, ui.ContainerItem("", arg))
	}
	if a.Scope == keymap.ScopeGlobal {
		return m.run(a)
	}
	return m.toPane(a.ID)
}

// toPane runs a pane action in the first pane, the focused one first,
// whose scope has an action with the id; that pane is focused before the
// action runs, so an input mode it enters is its own.
func (m *model) toPane(id string) tea.Cmd {
	order := []int{m.focus}
	for i := range m.panes() {
		if i != m.focus {
			order = append(order, i)
		}
	}
	for _, i := range order {
		for _, a := range m.env.Keys.InScope(m.panes()[i].Scope()) {
			if a.ID == id {
				m.focusPane(i)
				return m.do(i, a)
			}
		}
	}
	// A pane can also run an action of another scope (detail.Pane.Runs).
	for _, i := range order {
		p := m.panes()[i]
		if main, ok := p.(*containers.Main); ok && m.env.Selected.Kind != ui.SelectContainer {
			p = main.Server
		}
		if r, ok := p.(interface{ Runs(string) bool }); ok && r.Runs(id) {
			if a, ok := m.env.Keys.ByID(id); ok {
				m.focusPane(i)
				return m.do(i, a)
			}
		}
	}
	return m.show(ui.NoticeMsg{Text: id + ": not available in these panes", Level: ui.LevelInfo}, noticeTime)
}

// setTheme switches to a theme family, with "-light" or "-dark" forcing
// the variant, and saves it.
func (m *model) setTheme(arg string) tea.Cmd {
	f, variant, err := pickTheme(arg, prefs{})
	if err != nil {
		return m.fail(err.Error())
	}
	m.family, m.variant = f, variant
	m.restyle()
	m.prefs.Theme, m.prefs.Variant = f.ID, variant
	flash := m.flash(ui.NoticeMsg{Text: "theme " + f.Label, Level: ui.LevelInfo}, noticeTime)
	return tea.Batch(flash, m.savePrefs())
}

// setIcons switches to a glyph set and saves it.
func (m *model) setIcons(arg string) tea.Cmd {
	g, ok := glyphSet(arg)
	if !ok {
		return m.fail("unknown icons " + arg + "; use unicode, nerd or ascii")
	}
	m.env.Glyphs = g
	m.prefs.Icons = g.ID
	flash := m.flash(ui.NoticeMsg{Text: "icons " + g.ID, Level: ui.LevelInfo}, noticeTime)
	return tea.Batch(flash, m.savePrefs())
}

// server finds a server by name in the latest snapshot.
func (m *model) server(name string) (supervisor.View, bool) {
	if m.env.State != nil {
		for _, v := range m.env.State.Servers {
			if v.Name == name {
				return v, true
			}
		}
	}
	return supervisor.View{}, false
}

// commandTarget is what a command acts on: the server or group its
// argument names (checkName made sure it exists and has the right kind),
// else the cursor's target.
func (m *model) commandTarget(a keymap.Action, name string) ui.Selection {
	switch {
	case name == "":
		return m.cursorTarget()
	case a.Arg == keymap.ArgGroup:
		return ui.GroupItem(name)
	case a.Arg == keymap.ArgServerOrGroup && !m.hasServer(name):
		return ui.GroupItem(name)
	}
	return ui.ServerItem(name)
}

func (m *model) hasServer(name string) bool {
	_, ok := m.server(name)
	return ok
}

// stateNames are the display states `:filter-state` takes.
func stateNames() []string {
	var out []string
	for _, g := range countOrder {
		for _, d := range g {
			out = append(out, string(d))
		}
	}
	return out
}

// filterState shows only the servers in a display state; a state the top
// bar counts with others ("crashed" with "blocked") shows them too, and
// "all" clears the filter.
func (m *model) filterState(state string) tea.Cmd {
	if state == "" {
		return m.show(ui.NoticeMsg{Text: "filter-state needs a state, such as :filter-state crashed, or all", Level: ui.LevelInfo}, noticeTime)
	}
	if state == "all" {
		return m.setStateFilter(nil, "")
	}
	if !slices.Contains(stateNames(), state) {
		return m.fail("unknown state " + state + "; states: " + strings.Join(stateNames(), ", ") + ", all")
	}
	states := []supervisor.Display{supervisor.Display(state)}
	for _, g := range countOrder {
		if string(g[0]) == state {
			states = g
		}
	}
	return m.setStateFilter(states, state)
}

// setStateFilter passes a state filter to the servers list, after checking
// that a server is in one of the states.
func (m *model) setStateFilter(states []supervisor.Display, label string) tea.Cmd {
	if len(states) > 0 {
		found := false
		if m.env.State != nil {
			for _, v := range m.env.State.Servers {
				found = found || slices.Contains(states, v.Display())
			}
		}
		if !found {
			return m.show(ui.NoticeMsg{Text: "no server is " + label, Level: ui.LevelInfo}, noticeTime)
		}
	}
	m.stateFilter = label
	m.focusPane(0)
	return m.broadcast(ui.StateFilterMsg{States: states, Label: label})
}
