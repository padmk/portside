package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/padmk/portside/internal/supervisor"
	"github.com/padmk/portside/internal/tui/act"
	"github.com/padmk/portside/internal/tui/containers"
	"github.com/padmk/portside/internal/tui/keymap"
	"github.com/padmk/portside/internal/tui/logs"
	"github.com/padmk/portside/internal/tui/overlay"
	"github.com/padmk/portside/internal/tui/ports"
	"github.com/padmk/portside/internal/tui/ui"
	"github.com/padmk/portside/internal/viewfmt"
)

// targetActions are the servers' actions that act on a target (a server or
// a group header). Keys, the menu and the command line all run them
// through act with the target they resolved, so a selection that changes
// in between never redirects them.
var targetActions = map[string]bool{
	"toggle": true, "start": true, "stop": true, "restart": true,
	"start-group": true, "stop-group": true, "restart-group": true,
	"attach": true, "open": true, "copy-url": true, "shell": true,
	"editor": true, "terminal": true, "toggle-enabled": true,
	"clear-logs": true, "delete": true, "edit": true, "takeover": true,
}

// groupVerbs maps an action on a group header to the group's verb.
var groupVerbs = map[string]string{
	"start": "start", "stop": "stop", "restart": "restart",
	"start-group": "start", "stop-group": "stop", "restart-group": "restart",
}

// op is an operation whose request is in progress on an item.
type op struct {
	id   int
	verb string // "restarting"
}

// cursorTarget is what the active list's visible cursor is on
// (followSelection keeps it so); none when the list shows no rows.
func (m *model) cursorTarget() ui.Selection { return m.env.Target }

// members are a group's servers in config order; "" is the ungrouped ones.
func (m *model) members(group string) []supervisor.View {
	var out []supervisor.View
	if m.env.State != nil {
		for _, v := range viewfmt.Ordered(m.env.State) {
			if v.Group == group {
				out = append(out, v)
			}
		}
	}
	return out
}

// toggleVerb is what space does on a group: stop when a member can stop,
// else start.
func (m *model) toggleVerb(group string) string {
	if len(supervisor.GroupTargets(m.members(group), "stop")) > 0 {
		return "stop"
	}
	return "start"
}

// off is why action id does nothing on target t, "" when it acts: one
// rule for keys, menus, the footer, the palette and the command line.
func (m *model) off(id string, t ui.Selection) string {
	if why, ok := m.itemOff(id, t); ok {
		return why
	}
	switch t.Kind {
	case ui.SelectGroup:
		verb, ok := groupVerbs[id]
		if id == "toggle" {
			verb, ok = m.toggleVerb(t.Name), true
		}
		if !ok {
			return "needs a server, not a group header"
		}
		return m.groupOff(verb, t.Name)
	case ui.SelectServer:
	default:
		return "nothing selected"
	}
	v, ok := m.server(t.Name)
	if !ok {
		return "no server named " + t.Name
	}
	if o, busy := m.ops[t]; busy {
		switch id {
		case "toggle", "start", "stop", "restart", "toggle-enabled", "delete":
			return "already " + o.verb
		}
	}
	switch id {
	case "start":
		if v.HasProcess() {
			return "already " + string(v.Display())
		}
	case "stop":
		if !v.CanStop() {
			return "not running"
		}
	case "restart":
		if !v.CanStop() && !v.Enabled {
			return "disabled; S starts it anyway"
		}
	case "attach":
		if !v.HasProcess() {
			return "not running"
		}
	case "open", "copy-url":
		if m.env.Session.URL(v.Name, v.URL) == "" {
			return "no URL"
		}
	case "shell", "editor", "terminal":
		if m.env.Session.Remote() {
			return "only on this computer"
		}
	case "start-group", "stop-group", "restart-group":
		if v.Group == "" {
			return "not in a group"
		}
		return m.groupOff(groupVerbs[id], v.Group)
	case "delete":
		if v.Temporary {
			return "a temporary copy, not in the config; stop it to remove it"
		}
	case "edit":
		if v.Temporary {
			return "a temporary copy, not in the config"
		}
	case "takeover":
		return ports.TakeoverOff(&m.env, v.Name)
	}
	return ""
}

// groupOff is why a group verb does nothing on a group: no member it
// applies to (supervisor.GroupTargets).
func (m *model) groupOff(verb, group string) string {
	if len(supervisor.GroupTargets(m.members(group), verb)) > 0 {
		return ""
	}
	return map[string]string{"start": "nothing to start", "stop": "nothing running", "restart": "nothing running to restart"}[verb]
}

// act runs a target action on t, or says why it does nothing there.
func (m *model) act(id string, t ui.Selection) tea.Cmd {
	if why := m.off(id, t); why != "" {
		subject := t.Name
		if t.Kind == ui.SelectGroup {
			subject = ui.GroupLabel(t.Name)
		}
		if subject == "" || t.Kind == ui.SelectNone {
			return m.warn(id + ": " + why)
		}
		return m.warn(id + " " + subject + ": " + why)
	}
	if cmd, ok := m.itemAct(id, t, false); ok {
		return cmd
	}
	if t.Kind == ui.SelectGroup {
		verb := groupVerbs[id]
		if id == "toggle" {
			verb = m.toggleVerb(t.Name)
		}
		return m.groupOp(verb, t.Name)
	}
	v, _ := m.server(t.Name)
	switch id {
	case "toggle":
		verb := "start"
		if v.CanStop() {
			verb = "stop"
		}
		return m.serverOp(verb, v.Name)
	case "start", "stop", "restart":
		return m.serverOp(id, v.Name)
	case "start-group", "stop-group", "restart-group":
		return m.groupOp(groupVerbs[id], v.Group)
	case "attach":
		return logs.AttachContext(m.env.Context, &m.env, v.Name)
	case "open":
		return act.OpenURL(&m.env, v, false)
	case "copy-url":
		return act.CopyURL(&m.env, v)
	case "shell":
		return act.Shell(&m.env, v)
	case "editor", "terminal":
		return act.OpenOnDesktop(&m.env, v.Name, id)
	case "toggle-enabled":
		return act.SetEnabled(&m.env, v.Name, !v.Enabled)
	case "clear-logs":
		return act.ConfirmClearLogs(&m.env, v.Name)
	case "delete":
		return act.ConfirmDelete(&m.env, v)
	case "edit":
		return m.edit.EditServer(&m.env, v.Name)
	case "takeover":
		return m.takeover(v.Name)
	}
	return nil
}

// busy is the notice for an item whose operation is still in progress.
func (m *model) busy(items []ui.Selection) tea.Cmd {
	for _, it := range items {
		if o, ok := m.ops[it]; ok {
			return m.warn(it.Name + " is already " + o.verb)
		}
	}
	return nil
}

// begin records an operation on items and returns its id.
func (m *model) begin(verb string, items []ui.Selection) int {
	m.opSeq++
	for _, it := range items {
		m.ops[it] = op{id: m.opSeq, verb: act.Progressive(verb)}
		m.env.Pending[it] = act.Progressive(verb)
	}
	return m.opSeq
}

// serverOp starts, stops or restarts one server unless an operation on it
// is in progress.
func (m *model) serverOp(verb, name string) tea.Cmd {
	items := []ui.Selection{ui.ServerItem(name)}
	if cmd := m.busy(items); cmd != nil {
		return cmd
	}
	id := m.begin(verb, items)
	return tea.Batch(act.Server(m.env.Client, id, verb, name), m.spin())
}

// groupOp runs a group verb on the members it applies to
// (supervisor.GroupTargets); none gets a notice instead of a request.
func (m *model) groupOp(verb, group string) tea.Cmd {
	targets := supervisor.GroupTargets(m.members(group), verb)
	if len(targets) == 0 {
		what := map[string]string{"start": "to start", "stop": "to stop", "restart": "running to restart"}[verb]
		return m.show(ui.NoticeMsg{Text: "nothing " + what + " in " + ui.GroupLabel(group), Level: ui.LevelInfo}, noticeTime)
	}
	names := make([]string, len(targets))
	items := make([]ui.Selection, len(targets))
	for i, v := range targets {
		names[i], items[i] = v.Name, ui.ServerItem(v.Name)
	}
	if cmd := m.busy(items); cmd != nil {
		return cmd
	}
	id := m.begin(verb, items)
	return tea.Batch(act.Group(m.env.Client, id, verb, group, names), m.spin())
}

// finish ends an operation: only items still marked with its id are
// cleared, so a later operation's mark stays.
func (m *model) finish(msg ui.OpDoneMsg) tea.Cmd {
	for _, it := range msg.Items {
		if o, ok := m.ops[it]; ok && o.id == msg.ID {
			delete(m.ops, it)
			delete(m.env.Pending, it)
		}
	}
	switch {
	case msg.Err != nil:
		return m.fail(msg.Label + ": " + msg.Err.Error())
	case len(msg.Errs) == 1:
		return m.fail(msg.Errs[0])
	case len(msg.Errs) > 1:
		for _, e := range msg.Errs {
			m.remember(ui.NoticeMsg{Text: e, Level: ui.LevelError})
		}
		return m.flash(ui.NoticeMsg{Text: fmt.Sprintf("%s: %d failed; :messages lists them", msg.Label, len(msg.Errs)), Level: ui.LevelError}, noticeTime)
	}
	return nil
}

// warn shows a warning notice.
func (m *model) warn(text string) tea.Cmd {
	return m.show(ui.NoticeMsg{Text: text, Level: ui.LevelWarn}, noticeTime)
}

// groupLabel is a group action's label with how many members it applies
// to: "Restart group (2)".
func (m *model) groupLabel(verb, group string) string {
	n := len(supervisor.GroupTargets(m.members(group), verb))
	return fmt.Sprintf("%s group (%d)", strings.ToUpper(verb[:1])+verb[1:], n)
}

// groupLabelNamed is groupLabel with the group's name: "Restart group shop
// (2)".
func (m *model) groupLabelNamed(verb, group string) string {
	n := len(supervisor.GroupTargets(m.members(group), verb))
	name := group
	if name == "" {
		name = "(ungrouped)"
	}
	return fmt.Sprintf("%s group %s (%d)", strings.ToUpper(verb[:1])+verb[1:], name, n)
}

// targetTitle names a menu's target: "web", "group shop (3)".
func (m *model) targetTitle(t ui.Selection) string {
	switch t.Kind {
	case ui.SelectGroup:
		name := t.Name
		if name == "" {
			name = "(ungrouped)"
		}
		return fmt.Sprintf("group %s (%d)", name, len(m.members(t.Name)))
	case ui.SelectServer:
		return t.Name
	}
	return ""
}

// serverMenu is the servers' menu for a target, titled with it: the
// group's actions with their counts on a header, else the server's
// actions, with the group ones only when it is in a group, each marked
// with why it is off.
func (m *model) serverMenu(t ui.Selection) overlay.Menu {
	menu := overlay.MenuFor(m.env.Keys, keymap.ScopeServers, m.ascii())
	out := overlay.Menu{Title: m.targetTitle(t)}
	if t.Kind == ui.SelectGroup {
		for _, it := range menu.Items {
			switch it.ActionID {
			case "start", "stop", "restart":
				it.Label = m.groupLabel(it.ActionID, t.Name)
				it.Off = m.off(it.ActionID, t)
				out.Items = append(out.Items, it)
			}
		}
		return out
	}
	if t.Kind != ui.SelectServer {
		return out
	}
	v, _ := m.server(t.Name)
	for _, it := range menu.Items {
		if verb, ok := groupVerbs[it.ActionID]; ok && strings.HasSuffix(it.ActionID, "-group") {
			if v.Group == "" {
				continue
			}
			it.Label = m.groupLabel(verb, v.Group)
		}
		switch it.ActionID {
		case "toggle":
			it.Label = "Start"
			if v.CanStop() {
				it.Label = "Stop"
			}
		case "toggle-enabled":
			it.Label = "Disable (skipped by --all and restore)"
			if !v.Enabled {
				it.Label = "Enable"
			}
		}
		it.Off = m.off(it.ActionID, t)
		out.Items = append(out.Items, it)
	}
	return out
}

// shownActions are the server actions the Details, Env and Metrics tabs'
// menu offers for the shown server; their keys belong to the list, so the
// menu shows their commands.
var shownActions = []string{"toggle", "restart", "attach", "open", "copy-url", "shell", "editor"}

// shownServerMenu is the menu of the Details, Env and Metrics tabs: the
// shown server's main actions.
func (m *model) shownServerMenu() overlay.Menu {
	t := m.env.Selected
	if t.Kind != ui.SelectServer || !m.hasServer(t.Name) {
		return overlay.Menu{}
	}
	full := m.serverMenu(t)
	out := overlay.Menu{Title: full.Title}
	for _, id := range shownActions {
		for _, it := range full.Items {
			if it.ActionID == id {
				it.Key = ":" + id
				out.Items = append(out.Items, it)
			}
		}
	}
	return out
}

// hintShort is a footer hint's word: space says whether it starts or
// stops the server, enter what it opens, and on a group header the server
// verbs name the group and how many members they apply to.
func (m *model) hintShort(a keymap.Action) string {
	t := m.cursorTarget()
	if a.ID == "open-detail" {
		switch t.Kind {
		case ui.SelectGroup, ui.SelectProject:
			return "fold/unfold"
		case ui.SelectPort:
			if l, ok := m.listener(t); ok {
				return "select " + ports.Describe(l)
			}
		}
	}
	if a.Scope == keymap.ScopeLogs && a.ID == "attach" && m.env.Selected.Kind == ui.SelectContainer {
		return "shell"
	}
	if a.Scope == keymap.ScopeContainers && a.ID == "toggle" && m.env.State != nil {
		switch t.Kind {
		case ui.SelectContainer:
			if c, ok := containers.Find(m.env.State, t); ok {
				return containers.ToggleVerb(c)
			}
		case ui.SelectProject:
			if p, ok := containers.FindProject(m.env.State, t.Name); ok {
				return strings.ReplaceAll(containers.ProjectToggleVerb(p), "-", " ")
			}
		}
	}
	if a.Scope == keymap.ScopeServers && a.ID == "toggle" && t.Kind == ui.SelectServer {
		if v, ok := m.server(t.Name); ok && v.CanStop() {
			return "stop"
		}
		return "start"
	}
	if a.Scope != keymap.ScopeServers || t.Kind != ui.SelectGroup {
		return a.Short
	}
	verb := ""
	switch a.ID {
	case "toggle":
		verb = m.toggleVerb(t.Name)
	case "start", "stop", "restart":
		verb = a.ID
	default:
		return a.Short
	}
	return fmt.Sprintf("%s group (%d)", verb, len(supervisor.GroupTargets(m.members(t.Name), verb)))
}

// hintOff is why the footer leaves action a out: the rule the menus mark
// it off with (off, logsOff) for the cursor's target, or the shown
// source on the Logs tab. Actions on the pane itself (filter, search,
// enter on a server) have no such rule and always show.
func (m *model) hintOff(a keymap.Action) string {
	t := m.cursorTarget()
	switch a.Scope {
	case keymap.ScopeServers:
		if targetActions[a.ID] {
			return m.off(a.ID, t)
		}
	case keymap.ScopeContainers:
		if a.Menu || a.ID == "toggle" {
			return m.off(a.ID, t)
		}
	case keymap.ScopePorts:
		if a.Menu || a.ID == "open-detail" {
			return m.off(a.ID, t)
		}
	case keymap.ScopeLogs:
		return m.logsOff(a.ID)
	}
	return ""
}

// logsOff is why a Logs tab action does nothing on the source it shows:
// attach opens a container's shell (containers.Off's rule for shell) or a
// server's terminal (off), and only a server has a scrollback to clear.
func (m *model) logsOff(id string) string {
	sel := m.env.Selected
	switch id {
	case "attach":
		if sel.Kind == ui.SelectContainer {
			return containers.Off(&m.env, "shell", sel)
		}
		return m.off("attach", sel)
	case "clear-logs":
		if sel.Kind == ui.SelectContainer {
			return "the container engine keeps its logs"
		}
		return m.off("clear-logs", sel)
	}
	return ""
}
