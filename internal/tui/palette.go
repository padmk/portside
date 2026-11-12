package tui

import (
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/padmk/portside/internal/tui/containers"
	"github.com/padmk/portside/internal/tui/keymap"
	"github.com/padmk/portside/internal/tui/overlay"
	"github.com/padmk/portside/internal/tui/ports"
	"github.com/padmk/portside/internal/tui/theme"
	"github.com/padmk/portside/internal/tui/ui"
	"github.com/padmk/portside/internal/viewfmt"
)

// openPalette opens the command palette over every action it can run now.
func (m *model) openPalette() {
	m.palette = &paletteBox{Palette: overlay.NewPalette(m.paletteItems())}
}

// paletteKey handles a key while the palette is open: typing filters, the
// arrows (and ctrl+n, ctrl+p) move, enter runs the highlighted entry
// through the command line's executor, esc closes.
func (m *model) paletteKey(msg tea.KeyPressMsg) tea.Cmd {
	p := m.palette
	switch k := msg.String(); k {
	case "esc":
		m.palette = nil
	case "enter":
		return m.paletteRun()
	case "down", "ctrl+n", "tab":
		p.Down()
	case "up", "ctrl+p", "shift+tab":
		p.Up()
	case "backspace":
		if p.Query() == "" {
			m.palette = nil
			return nil
		}
		p.Backspace()
	case "ctrl+w":
		p.DeleteWord()
	case "ctrl+u":
		p.Clear()
	case "space":
		p.Insert(" ")
	default:
		key := msg.Key()
		if key.Text != "" && key.Mod&(tea.ModCtrl|tea.ModAlt|tea.ModSuper) == 0 {
			p.Insert(key.Text)
		}
	}
	return nil
}

// paletteRun closes the palette and runs the highlighted entry, or says
// why it is off.
func (m *model) paletteRun() tea.Cmd {
	it, ok := m.palette.Selected()
	m.palette = nil
	if !ok {
		return nil
	}
	if it.Off != "" {
		return m.warn(it.Label + ": " + it.Off)
	}
	if it.Act.ID != "" {
		return m.actMsg(it.Act)
	}
	m.keepCommand(it.Command)
	return m.execute(it.Command)
}

// paletteClick runs the entry under the pointer; a click outside the box
// closes the palette.
func (m *model) paletteClick(ms tea.Mouse) tea.Cmd {
	p := m.palette
	w, h := p.Size(m.width, m.height)
	if ms.X < p.x || ms.X >= p.x+w || ms.Y < p.y || ms.Y >= p.y+h {
		m.palette = nil
		return nil
	}
	if i := p.Hit(ms.Y - p.y); i >= 0 && ms.Button == tea.MouseLeft {
		p.Pick(i)
		return m.paletteRun()
	}
	return nil
}

// paletteItems are the palette's entries, generated from the registry:
// the actions on the cursor's target (with the keys that do the same
// here), the global and pane actions, every theme, icon set and state
// filter, then the actions on every other server, group, container,
// compose project and listed port, each target action once per target.
// Server entries run as their command line, so they do exactly what
// typing it does; container, project and port entries run their action
// on their item, as its key does there.
func (m *model) paletteItems() []overlay.PaletteItem {
	var out []overlay.PaletteItem
	t := m.cursorTarget()
	scope := m.focused().Scope()
	serverActions := m.env.Keys.InScope(keymap.ScopeServers)
	add := func(a keymap.Action, label, arg, key string, off string) {
		out = append(out, overlay.PaletteItem{Label: label, Key: key, Command: a.Command(arg), Off: off, Destructive: a.Destructive})
	}
	// keyHere is the action's key when it does the same from the focused
	// pane.
	keyHere := func(a keymap.Action) string {
		for _, k := range a.Keys {
			if b, ok := m.env.Keys.Lookup(k, scope, keymap.ScopeGlobal); ok && b.ID == a.ID && b.Scope == a.Scope {
				return overlay.KeyLabel(k, m.ascii())
			}
		}
		return ""
	}
	// targetEntries are the server actions on t, each label once (on a
	// group, space and the verb it toggles to are one entry); current is
	// set for the cursor's target, whose entries carry keys. A server's
	// group actions are its group's entries.
	targetEntries := func(t ui.Selection, current bool) {
		labels := map[string]bool{}
		for _, a := range serverActions {
			if !targetActions[a.ID] || !m.takes(a, t) || strings.HasSuffix(a.ID, "-group") {
				continue
			}
			label, key := m.actionLabel(a, t), ""
			if labels[label] {
				continue
			}
			labels[label] = true
			if current {
				key = keyHere(a)
			}
			add(a, label, t.Name, key, m.off(a.ID, t))
		}
	}
	// itemEntry is action id of scope on a container, project or port.
	itemEntry := func(scope keymap.Scope, id string, t ui.Selection, label string, current bool) {
		a, ok := scopeAction(m.env.Keys, scope, id)
		if !ok {
			return
		}
		off, _ := m.itemOff(id, t)
		if id == "dismiss-job" && off != "" && !current {
			return
		}
		key := ""
		if current {
			key = keyHere(a)
		}
		out = append(out, overlay.PaletteItem{Label: label, Key: key, Command: m.itemCommand(a, t), Act: ui.ActMsg{ID: id, Item: t}, Off: off, Destructive: a.Destructive})
	}
	containerEntries := func(t ui.Selection, current bool) {
		ids := containers.PaletteActions
		if t.Kind == ui.SelectProject {
			if t.Name == "" {
				return
			}
			ids = containers.ProjectActions
		}
		for _, id := range ids {
			itemEntry(keymap.ScopeContainers, id, t, containers.Label(m.env.State, id, t), current)
		}
	}
	portEntries := func(l ports.Listener, current bool) {
		for _, id := range ports.PaletteActions {
			itemEntry(keymap.ScopePorts, id, ports.Item(l), ports.Label(id, l), current)
		}
	}
	switch t.Kind {
	case ui.SelectServer, ui.SelectGroup:
		targetEntries(t, true)
	case ui.SelectContainer, ui.SelectProject:
		containerEntries(t, true)
	case ui.SelectPort:
		if l, ok := m.listener(t); ok {
			portEntries(l, true)
		}
	}
	seen := map[string]bool{}
	// A pane action runs in the focused pane when it has one with the id
	// (toPane), so the focused scope's comes first and names it.
	for _, a := range append(m.env.Keys.InScope(scope), m.env.Keys.All()...) {
		if a.Group == "Navigation" || a.ID == "palette" || seen[a.ID] || itemTarget(a) {
			continue
		}
		seen[a.ID] = true
		switch a.Arg {
		case keymap.ArgTheme:
			add(a, a.Help, "", keyHere(a), "")
			for _, f := range theme.Families() {
				add(a, "Theme "+f.Label, f.ID, "", "")
				for _, v := range []string{"dark", "light"} {
					add(a, "Theme "+f.Label+" "+v, f.ID+"-"+v, "", "")
				}
			}
			continue
		case keymap.ArgIcons:
			for _, g := range theme.GlyphSets() {
				add(a, "Icons "+g.ID, g.ID, "", "")
			}
			continue
		case keymap.ArgState:
			for _, st := range append(stateNames(), "all") {
				add(a, "Show only "+st+" servers", st, "", "")
			}
			continue
		}
		label := a.Help
		if a.Scope != keymap.ScopeGlobal {
			label = a.Scope.Title() + ": " + a.Help
		}
		add(a, label, "", keyHere(a), m.systemOff(a.ID))
	}
	if m.env.State != nil {
		for _, v := range viewfmt.Ordered(m.env.State) {
			if it := ui.ServerItem(v.Name); it != t {
				targetEntries(it, false)
			}
		}
		for _, g := range viewfmt.Groups(m.env.State) {
			if it := ui.GroupItem(g.Name); g.Name != "" && it != t {
				targetEntries(it, false)
			}
		}
		for _, c := range m.env.State.Docker.Containers {
			if it := containers.Item(c); it != t {
				containerEntries(it, false)
			}
		}
		for _, p := range containers.Projects(m.env.State) {
			if it := ui.ProjectItem(p.Name); it != t {
				containerEntries(it, false)
			}
		}
	}
	if p, ok := m.paneOf(keymap.ScopePorts).(*ports.Pane); ok && m.showPorts {
		for _, l := range p.Listeners() {
			if ports.Item(l) != t {
				portEntries(l, false)
			}
		}
	}
	return out
}

// itemCommand is the command line that runs action a on a container,
// project or port, as the palette shows it (":restart shop-db-1",
// ":kill 3102"); "" when the action takes no such argument. The command
// line resolves it to the same action (commandVariant).
func (m *model) itemCommand(a keymap.Action, t ui.Selection) string {
	switch {
	case t.Kind == ui.SelectContainer && a.Arg == keymap.ArgContainer:
		return a.Command(t.Name)
	case t.Kind == ui.SelectProject && a.Arg == keymap.ArgProject && t.Name != "":
		return a.Command(t.Name)
	case t.Kind == ui.SelectPort && a.Arg == keymap.ArgPort:
		// The port names it unless several processes listen there.
		if p, ok := m.paneOf(keymap.ScopePorts).(*ports.Pane); ok {
			for _, l := range p.Listeners() {
				if l.Port == t.Port && l.PID != t.PID {
					return a.Command(strconv.Itoa(t.PID))
				}
			}
		}
		return a.Command(strconv.Itoa(t.Port))
	}
	return ""
}

// itemTarget reports whether a containers or ports action acts on the
// item under the cursor; the palette lists those once per item instead.
func itemTarget(a keymap.Action) bool {
	switch a.Scope {
	case keymap.ScopeContainers, keymap.ScopePorts:
		return a.Menu || a.ID == "toggle"
	}
	return a.Scope == keymap.ScopeServers && targetActions[a.ID]
}

// scopeAction is the action of a scope with an id.
func scopeAction(r *keymap.Registry, scope keymap.Scope, id string) (keymap.Action, bool) {
	for _, a := range r.InScope(scope) {
		if a.ID == id {
			return a, true
		}
	}
	return keymap.Action{}, false
}

// takes reports whether a server action applies to the kind of target:
// on a group only the group verbs, on a server the group verbs only when
// it is in a group.
func (m *model) takes(a keymap.Action, t ui.Selection) bool {
	_, verb := groupVerbs[a.ID]
	switch t.Kind {
	case ui.SelectGroup:
		return (verb || a.ID == "toggle") && !strings.HasSuffix(a.ID, "-group")
	case ui.SelectServer:
		if strings.HasSuffix(a.ID, "-group") {
			v, _ := m.server(t.Name)
			return v.Group != ""
		}
		return true
	}
	return false
}

// actionLabel names an action on a target: "Restart web", "Restart group
// shop (2)", "Disable web", "Start group shop (1)" for a server's group
// action.
func (m *model) actionLabel(a keymap.Action, t ui.Selection) string {
	if t.Kind == ui.SelectGroup {
		verb := a.ID
		if a.ID == "toggle" {
			verb = m.toggleVerb(t.Name)
		}
		return m.groupLabelNamed(verb, t.Name)
	}
	v, _ := m.server(t.Name)
	switch a.ID {
	case "toggle":
		if v.CanStop() {
			return "Stop " + v.Name
		}
		return "Start " + v.Name
	case "toggle-enabled":
		if v.Enabled {
			return "Disable " + v.Name
		}
		return "Enable " + v.Name
	case "start", "stop", "restart":
		return strings.ToUpper(a.ID[:1]) + a.ID[1:] + " " + v.Name
	case "start-group", "stop-group", "restart-group":
		return m.groupLabelNamed(groupVerbs[a.ID], v.Group)
	}
	return a.Help + ": " + v.Name
}
