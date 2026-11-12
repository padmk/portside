package tui

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/padmk/portside/internal/browser"
	"github.com/padmk/portside/internal/server"
	"github.com/padmk/portside/internal/tui/containers"
	"github.com/padmk/portside/internal/tui/keymap"
	"github.com/padmk/portside/internal/tui/logs"
	"github.com/padmk/portside/internal/tui/overlay"
	"github.com/padmk/portside/internal/tui/ports"
	"github.com/padmk/portside/internal/tui/servers"
	"github.com/padmk/portside/internal/tui/ui"
)

// itemPanes is what the root model keeps for the left column's panes
// other than the servers: every left pane (left holds the ones shown),
// whether the ports pane is open, the last server selected, and each
// container's last announced problem for its toasts.
type itemPanes struct {
	leftAll    []ui.Pane
	showPorts  bool
	lastServer ui.Selection
	// ctrProblem is each container's problem in the last snapshot ("" for
	// none), by name; nil before the first.
	ctrProblem map[string]string
}

// initLeft keeps every left pane and shows the ones that belong on
// screen.
func (m *model) initLeft() {
	m.leftAll = m.left
	m.left = []ui.Pane{m.left[0]}
	m.syncLeft()
}

// pane returns the left pane of a scope among all of them.
func (m *model) paneOf(sc keymap.Scope) ui.Pane {
	for _, p := range m.leftAll {
		if p.Scope() == sc {
			return p
		}
	}
	return nil
}

// syncLeft shows the left panes that belong on screen: the servers, the
// containers while the daemon reports some or an engine problem
// (containers.Shown), the ports while open. The focus, the active pane
// and an input mode's owner stay on their panes; one that goes gives the
// focus to the servers.
func (m *model) syncLeft() {
	var want []ui.Pane
	for _, p := range m.leftAll {
		switch p.Scope() {
		case keymap.ScopeContainers:
			if !containers.Shown(m.env.State) {
				continue
			}
		case keymap.ScopePorts:
			if !m.showPorts {
				continue
			}
		}
		want = append(want, p)
	}
	if slices.Equal(want, m.left) {
		return
	}
	panes := m.panes()
	focused, active := panes[m.focus], m.left[min(m.leftActive, len(m.left)-1)]
	var owner ui.Pane
	if m.mode != ui.ModeNormal && m.modeOwner >= 0 {
		owner = panes[m.modeOwner]
	}
	m.left = want
	panes = m.panes()
	m.focus = max(0, slices.Index(panes, focused))
	m.leftActive = max(0, slices.Index(m.left, active))
	if focused != panes[m.focus] {
		m.leftActive = 0
	}
	if owner != nil {
		if i := slices.Index(panes, owner); i >= 0 {
			m.modeOwner = i
		} else {
			m.setMode(ui.ModeNormal, -1)
		}
	}
	if !slices.ContainsFunc(m.left, func(p ui.Pane) bool { return p.Scope() == keymap.ScopeContainers }) && m.env.Selected.Kind == ui.SelectContainer {
		m.env.Selected, m.env.Target = m.lastServer, m.lastServer
		m.keepSelection()
	}
	m.layout()
}

// togglePorts opens or closes the ports pane; open, it is focused.
func (m *model) togglePorts() tea.Cmd {
	if m.showPorts {
		return m.hidePorts()
	}
	return m.openPorts()
}

func (m *model) openPorts() tea.Cmd {
	p, _ := m.paneOf(keymap.ScopePorts).(*ports.Pane)
	var cmd tea.Cmd
	if !m.showPorts {
		m.showPorts = true
		cmd = p.Open(&m.env)
		m.syncLeft()
	}
	return tea.Batch(cmd, m.focusScope(keymap.ScopePorts), m.followSelection())
}

func (m *model) hidePorts() tea.Cmd {
	p, _ := m.paneOf(keymap.ScopePorts).(*ports.Pane)
	p.Close(&m.env)
	m.showPorts = false
	m.syncLeft()
	if m.env.Target.Kind == ui.SelectPort {
		m.env.Target = m.env.Selected
	}
	return m.followSelection()
}

// followSelection makes the active list's visible cursor the target: the
// focused left pane's, or the last focused one's while the main panel has
// the focus. A container under it becomes the selection the right pane
// shows, and so does a server; a group header, a project header or an
// empty list leaves the right pane on what it showed (a preview, which its
// title says), and an empty list is no target at all, so actions say
// nothing is selected. It returns the selection message the panes follow,
// nil when the selection did not change. Keys, clicks, commands and menus
// all go through it before they act.
func (m *model) followSelection() tea.Cmd {
	if m.env.Selected.Kind == ui.SelectServer {
		m.lastServer = m.env.Selected
	}
	if len(m.left) == 0 {
		return nil
	}
	i := m.focus
	if i >= len(m.left) {
		i = min(m.leftActive, len(m.left)-1)
	}
	before := m.env.Selected
	switch p := m.left[i].(type) {
	case *containers.Pane:
		it := p.Cursor(&m.env)
		m.env.Target = it
		if it.Kind == ui.SelectContainer {
			m.env.Selected = it
		}
	case *ports.Pane:
		m.env.Target = ui.Selection{}
		if l, ok := p.Cursor(); ok {
			m.env.Target = ports.Item(l)
		}
	case *servers.Pane:
		// The cursor is read before the selection changes: while the
		// selection is another pane's, the list's cursor is its own.
		it := p.Cursor(&m.env)
		switch {
		case it.Kind == ui.SelectServer:
			m.env.Selected = it
		case m.env.Selected.Kind != ui.SelectServer && m.lastServer.Kind == ui.SelectServer && m.hasServer(m.lastServer.Name):
			m.env.Selected = m.lastServer
		}
		if m.env.Selected.Kind != ui.SelectServer {
			m.keepSelection()
			it = p.Cursor(&m.env)
		}
		m.env.Target = it
	}
	if m.env.Selected.Kind == ui.SelectServer {
		m.lastServer = m.env.Selected
	}
	if m.env.Selected == before {
		return nil
	}
	return m.broadcast(ui.SelectMsg{Item: m.env.Selected})
}

// after runs cmd, then follows the selection a key or a click moved.
func (m *model) after(cmd tea.Cmd) tea.Cmd { return tea.Batch(cmd, m.followSelection()) }

// argCommand runs a command whose argument only a pane can resolve:
// `:kill <port or pid>` through the ports pane, which opens for it; false
// for the others.
func (m *model) argCommand(a keymap.Action, arg string) (tea.Cmd, bool) {
	if a.Scope != keymap.ScopePorts || a.ID != "kill" || arg == "" {
		return nil, false
	}
	p, _ := m.paneOf(keymap.ScopePorts).(*ports.Pane)
	cmd := p.Kill(&m.env, arg)
	if !m.showPorts {
		m.showPorts = true
		m.syncLeft()
	}
	return cmd, true
}

// focusPorts focuses the ports pane, opening it first (3).
func (m *model) focusPorts() tea.Cmd { return m.openPorts() }

// itemOff is why action id does nothing on a container, project or port
// target; false for the other kinds, which off handles.
func (m *model) itemOff(id string, t ui.Selection) (string, bool) {
	switch t.Kind {
	case ui.SelectContainer, ui.SelectProject:
		if o, busy := m.ops[t]; busy {
			return "already " + o.verb, true
		}
		return containers.Off(&m.env, id, t), true
	case ui.SelectPort:
		l, ok := m.listener(t)
		if !ok {
			return "nothing listens on that port any more", true
		}
		if o, busy := m.ops[t]; busy {
			return "already " + o.verb, true
		}
		return ports.Off(&m.env, id, l), true
	}
	return "", false
}

// listener finds a port target in the ports pane's last list.
func (m *model) listener(t ui.Selection) (ports.Listener, bool) {
	p, _ := m.paneOf(keymap.ScopePorts).(*ports.Pane)
	if p == nil {
		return ports.Listener{}, false
	}
	return p.Find(t)
}

// itemAct runs action id on a container, project or port target that
// itemOff let through; false for the other kinds.
func (m *model) itemAct(id string, t ui.Selection, confirmed bool) (tea.Cmd, bool) {
	switch t.Kind {
	case ui.SelectContainer, ui.SelectProject:
		return m.containerAct(id, t, confirmed), true
	case ui.SelectPort:
		return m.portAct(id, t, confirmed), true
	}
	return nil, false
}

// containerAct runs a container or project action: requests as tracked
// operations, the shell in the terminal, open and copy here.
func (m *model) containerAct(id string, t ui.Selection, confirmed bool) tea.Cmd {
	st := m.env.State
	if t.Kind == ui.SelectProject {
		id = containers.ProjectVerb(st, id, t.Name)
		p, _ := containers.FindProject(st, t.Name)
		ref := ""
		for _, c := range p.Containers {
			if c.Compose() {
				ref = c.Name
				break
			}
		}
		return m.containerOp(id, t, ref, confirmed)
	}
	c, _ := containers.Find(st, t)
	switch id {
	case "toggle":
		return m.containerOp(containers.ToggleVerb(c), t, c.Name, confirmed)
	case "shell":
		return logs.ContainerShell(m.env.Context, &m.env, c.Name)
	case "open":
		return openAddress(c.Name, containers.URL(&m.env, c))
	case "copy-url":
		return copyAddress(containers.URL(&m.env, c))
	case "start-project", "stop-project", "down-project":
		return m.containerOp(id, ui.ProjectItem(c.Project), c.Name, confirmed)
	}
	return m.containerOp(id, t, c.Name, confirmed)
}

// containerOp runs a container request as a tracked operation on item,
// through the container ref; removing a project's containers asks first.
func (m *model) containerOp(id string, item ui.Selection, ref string, confirmed bool) tea.Cmd {
	if id == "down-project" && !confirmed {
		again := ui.ActMsg{ID: id, Item: item, Confirmed: true}
		return containers.ConfirmDown(item.Name, func() tea.Msg { return again })
	}
	items := []ui.Selection{item}
	if cmd := m.busy(items); cmd != nil {
		return cmd
	}
	op := m.beginAs(containers.Progressive(id), items)
	ctx := m.env.Context
	if ctx == nil {
		ctx = context.Background()
	}
	cmd := containers.Request(ctx, m.env.Client, op, id, item, ref)
	if d, ok := m.right.(*containers.Main); ok && (id == "recreate" || id == "rebuild" || id == "pull") {
		d.Container.ShowJob()
	}
	return tea.Batch(cmd, m.spin())
}

// beginAs records an operation whose pending word is verb.
func (m *model) beginAs(verb string, items []ui.Selection) int {
	m.opSeq++
	for _, it := range items {
		m.ops[it] = op{id: m.opSeq, verb: verb}
		m.env.Pending[it] = verb
	}
	return m.opSeq
}

// portAct runs a port action: kill asks first (on a managed server's port
// it stops the server instead, after asking that), takeover starts the
// blocked server.
func (m *model) portAct(id string, t ui.Selection, confirmed bool) tea.Cmd {
	l, _ := m.listener(t)
	return m.listenerAct(id, t, l, confirmed)
}

// listenerAct is portAct on the listener l that target t names; both
// `d` and `:kill <port>` arrive here.
func (m *model) listenerAct(id string, t ui.Selection, l ports.Listener, confirmed bool) tea.Cmd {
	switch id {
	case "kill":
		if ports.KillStops(l) {
			if !confirmed {
				again := ui.ActMsg{ID: id, Item: t, Confirmed: true}
				return confirmStopOnPort(l, func() tea.Msg { return again })
			}
			return m.serverOp("stop", l.Server)
		}
		if !confirmed {
			again := ui.ActMsg{ID: id, Item: t, Confirmed: true}
			return ports.ConfirmKill(l, func() tea.Msg { return again })
		}
		items := []ui.Selection{t}
		if cmd := m.busy(items); cmd != nil {
			return cmd
		}
		return tea.Batch(ports.Kill(m.env.Client, m.beginAs("killing", items), l), m.spin())
	case "takeover":
		return m.takeover(l.Server)
	case "open":
		return openAddress(fmt.Sprintf("port %d", l.Port), ports.URL(&m.env, l))
	case "copy-url":
		return copyAddress(ports.URL(&m.env, l))
	case "open-detail":
		switch ports.RoleOf(l) {
		case ports.RoleContainer:
			if c, ok := containers.Find(m.env.State, ui.ContainerItem("", l.Container.Name)); ok {
				return m.selectContainer(containers.Item(c))
			}
		case ports.RoleManaged, ports.RoleBlocks:
			if m.hasServer(l.Server) {
				return m.selectServer(ui.ServerItem(l.Server))
			}
		}
		if l.Managed && m.hasServer(l.Server) {
			return m.selectServer(ui.ServerItem(l.Server))
		}
		return m.warn(fmt.Sprintf("port %d: no server or container of the daemon's uses it", l.Port))
	}
	return nil
}

// confirmStopOnPort asks before kill stops the managed server that
// listens on a port; yes stops it the way s does.
func confirmStopOnPort(l ports.Listener, yes tea.Cmd) tea.Cmd {
	return func() tea.Msg {
		return ui.ConfirmMsg{
			Title:   "Stop server " + l.Server,
			Body:    fmt.Sprintf("Stop server %s on port %d? The daemon stops it as s does, so a task it runs ends and what it holds in memory is lost.", l.Server, l.Port),
			Confirm: "Stop",
			Yes:     yes,
		}
	}
}

// takeover starts a server whose port another process holds, telling the
// daemon to take the port over.
func (m *model) takeover(name string) tea.Cmd {
	items := []ui.Selection{ui.ServerItem(name)}
	if cmd := m.busy(items); cmd != nil {
		return cmd
	}
	return tea.Batch(ports.Takeover(m.env.Client, m.begin("start", items), name), m.spin())
}

// selectContainer selects a container and focuses the containers list.
func (m *model) selectContainer(it ui.Selection) tea.Cmd {
	m.env.Selected, m.env.Target = it, it
	cmd := m.broadcast(ui.SelectMsg{Item: it})
	return tea.Batch(cmd, m.focusScope(keymap.ScopeContainers))
}

// openAddress opens an address in a browser of this session; without one
// it copies the address instead.
func openAddress(what, u string) tea.Cmd {
	if u == "" {
		return func() tea.Msg {
			return ui.NoticeMsg{Text: "open " + what + ": no address available", Level: ui.LevelError}
		}
	}
	args := browser.Command(runtime.GOOS, os.Getenv, u)
	if len(args) == 0 {
		return copyAddress(u)
	}
	return func() tea.Msg {
		if err := exec.Command(args[0], args[1:]...).Start(); err != nil {
			return ui.NoticeMsg{Text: "open " + what + ": " + err.Error(), Level: ui.LevelError}
		}
		return ui.NoticeMsg{Text: "opened " + u + " in the browser", Level: ui.LevelSuccess}
	}
}

// actMsg runs an action a pane or a confirmation sent: on a container,
// project or port with the confirmation it may carry, else as act does.
func (m *model) actMsg(msg ui.ActMsg) tea.Cmd {
	if !msg.Confirmed {
		return m.act(msg.ID, msg.Item)
	}
	if why, _ := m.itemOff(msg.ID, msg.Item); why != "" {
		return m.warn(msg.ID + ": " + why)
	}
	cmd, _ := m.itemAct(msg.ID, msg.Item, true)
	return cmd
}

// copyAddress copies an address to the clipboard (OSC 52) with a notice.
func copyAddress(u string) tea.Cmd {
	if u == "" {
		return func() tea.Msg { return ui.NoticeMsg{Text: "no address available", Level: ui.LevelError} }
	}
	return tea.Batch(tea.SetClipboard(u), func() tea.Msg { return ui.NoticeMsg{Text: "copied " + u, Level: ui.LevelSuccess} })
}

// itemMenu is the menu of the containers or ports pane for target t:
// its scope's menu actions, labeled for t and marked with why each is
// off.
func (m *model) itemMenu(scope keymap.Scope, t ui.Selection) overlay.Menu {
	menu := overlay.MenuFor(m.env.Keys, scope, m.ascii())
	out := overlay.Menu{Title: m.itemTitle(t)}
	for _, it := range menu.Items {
		if t.Kind == ui.SelectProject {
			switch it.ActionID {
			case "start-project", "stop-project", "down-project":
			default:
				continue
			}
		}
		if t.Kind == ui.SelectPort && it.ActionID == "kill" {
			if l, ok := m.listener(t); ok && ports.KillStops(l) {
				it.Label = "Stop server " + l.Server
			}
		}
		it.Off, _ = m.itemOff(it.ActionID, t)
		out.Items = append(out.Items, it)
	}
	if scope == keymap.ScopeContainers && m.env.State != nil && m.env.State.Docker.Status != "ready" {
		if a, ok := m.env.Keys.ByID("start-docker"); ok {
			out.Items = append([]overlay.MenuItem{{Label: a.Help, Key: ":" + a.ID, ActionID: a.ID}}, out.Items...)
		}
	}
	return out
}

// itemTitle names a container, project or port target for a menu.
func (m *model) itemTitle(t ui.Selection) string {
	switch t.Kind {
	case ui.SelectContainer:
		return "container " + t.Name
	case ui.SelectProject:
		if t.Name == "" {
			return "other containers"
		}
		return "project " + t.Name
	case ui.SelectPort:
		if l, ok := m.listener(t); ok {
			return fmt.Sprintf("port %d: %s (pid %d)", l.Port, l.Command, l.PID)
		}
		return fmt.Sprintf("port %d", t.Port)
	}
	return ""
}

// containerToasts announce the containers that became unhealthy, were
// killed for memory or failed since the last snapshot, as crashes are.
func (m *model) containerToasts(st *server.State) tea.Cmd {
	first := m.ctrProblem == nil
	prev := m.ctrProblem
	m.ctrProblem = map[string]string{}
	var out []ui.ToastMsg
	for _, c := range st.Docker.Containers {
		problem := ""
		switch {
		case c.OOMKilled:
			problem = "was killed: out of memory"
		case c.State == "running" && c.Health == "unhealthy":
			problem = "is unhealthy"
		case c.Failed:
			problem = "crashed"
			if c.ExitCode != nil {
				problem += fmt.Sprintf(" (exit %d)", *c.ExitCode)
			}
		}
		m.ctrProblem[c.Name] = problem
		if first || problem == "" || prev[c.Name] == problem {
			continue
		}
		body := ""
		if len(c.IssueFor) > 0 {
			body = "used by " + strings.Join(c.IssueFor, ", ")
		}
		out = append(out, ui.ToastMsg{Title: "container " + c.Name + " " + problem, Body: body, Level: ui.LevelError, Item: containers.Item(c)})
	}
	return m.toastAll(out, "containers need attention")
}
