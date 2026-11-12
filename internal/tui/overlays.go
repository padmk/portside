package tui

import (
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/padmk/portside/internal/tui/containers"
	"github.com/padmk/portside/internal/tui/detail"
	"github.com/padmk/portside/internal/tui/keymap"
	"github.com/padmk/portside/internal/tui/logs"
	"github.com/padmk/portside/internal/tui/overlay"
	"github.com/padmk/portside/internal/tui/theme"
	"github.com/padmk/portside/internal/tui/ui"
)

// overlays is what the root model draws over the panes and the bars, and
// where the bars can be clicked.
type overlays struct {
	help     *overlay.Help
	messages *messageList
	menu     *contextMenu
	palette  *paletteBox
	cmdLine  *overlay.CmdLine
	// cmdHistory holds this session's commands, oldest first.
	cmdHistory []string
	// hints and counts are the clickable ranges of the last footer and
	// top bar; toastAt is where the last toasts were drawn.
	hints   []hintHit
	counts  []countHit
	toastAt toastHit
	// dialogAt is the dialog's top left corner on the last screen.
	dialogAt [2]int
	// portAt is where the top bar's daemon port is, x0 to x1.
	portAt [2]int
}

// toastHit is the toast stack's place on the last screen.
type toastHit struct {
	x, y, w int
	stack   overlay.ToastStack
}

// contextMenu is the menu of a pane's actions (x or a right click) at x, y
// for the pane at index pane; target is what its server actions act on,
// taken when it opened.
type contextMenu struct {
	overlay.Menu
	x, y, pane int
	target     ui.Selection
}

// paletteBox is the open command palette and where it was drawn.
type paletteBox struct {
	*overlay.Palette
	x, y int
}

// messageList is the `:messages` overlay: cursor is the highlighted
// message, offset the first row shown and rows how many the last view
// showed.
type messageList struct{ cursor, offset, rows int }

func (m *model) ascii() bool { return m.env.Glyphs.ID == theme.ASCII.ID }

func (m *model) openHelp() { m.help = &overlay.Help{} }

// openMessages opens the message history with the newest highlighted.
func (m *model) openMessages() {
	m.messages = &messageList{cursor: max(0, len(m.history)-1)}
}

// scrollKey handles a key while the help overlay is open.
func (m *model) scrollKey(k string) {
	half := max(1, (m.height-4)/2)
	n := 0
	switch k {
	case "j", "down":
		n = 1
	case "k", "up":
		n = -1
	case "ctrl+d":
		n = half
	case "ctrl+u":
		n = -half
	case "esc", "q", "?":
		m.help = nil
		return
	}
	m.scrollOverlay(n)
}

// messagesKey handles a key while the messages overlay is open: j/k move,
// enter selects the server the highlighted message is about.
func (m *model) messagesKey(k string) tea.Cmd {
	l := m.messages
	half := max(1, (m.height-4)/2)
	switch k {
	case "j", "down":
		l.move(1, len(m.history))
	case "k", "up":
		l.move(-1, len(m.history))
	case "ctrl+d":
		l.move(half, len(m.history))
	case "ctrl+u":
		l.move(-half, len(m.history))
	case "esc", "q":
		m.messages = nil
	case "enter":
		if it, ok := m.messageTarget(); ok {
			m.messages = nil
			if it.Kind == ui.SelectServer {
				return m.selectServer(it)
			}
			return m.selectContainer(it)
		}
	}
	return nil
}

// messageTarget is the server or container the highlighted message is
// about, when it still exists and enter can select it.
func (m *model) messageTarget() (ui.Selection, bool) {
	l := m.messages
	if l == nil || l.cursor < 0 || l.cursor >= len(m.history) {
		return ui.Selection{}, false
	}
	it := m.history[l.cursor].Item
	switch it.Kind {
	case ui.SelectServer:
		return it, m.hasServer(it.Name)
	case ui.SelectContainer:
		_, ok := containers.Find(m.env.State, it)
		return it, ok
	}
	return ui.Selection{}, false
}

func (l *messageList) move(n, total int) {
	l.cursor = max(0, min(l.cursor+n, total-1))
}

// selectServer selects a server and focuses the servers list on it.
func (m *model) selectServer(it ui.Selection) tea.Cmd {
	m.env.Selected, m.env.Target = it, it
	m.focusPane(0)
	return m.broadcast(ui.SelectMsg{Item: it})
}

func (m *model) scrollOverlay(n int) {
	switch {
	case m.help != nil:
		m.help.Scroll(n)
	case m.messages != nil:
		m.messages.move(n, len(m.history))
	}
}

// openMenu opens the menu of the pane at index pane at the pointer, kept
// inside the screen; a pane without menu actions has none and says so.
func (m *model) openMenu(pane, x, y int) tea.Cmd {
	menu, target := m.paneMenu(pane)
	if len(menu.Items) == 0 {
		return m.flash(ui.NoticeMsg{Text: "no actions here; ctrl+p finds one", Level: ui.LevelInfo}, unknownTime)
	}
	m.fitMenu(&menu)
	w, h := menu.Size()
	top := m.bodyTop()
	m.menu = &contextMenu{Menu: menu, x: max(0, min(x, m.width-w)), y: max(top, min(y, m.height-1-h)), pane: pane, target: target}
	return nil
}

// fitMenu bounds a menu to the screen: the width, and the body's height
// between the bars, beyond which it scrolls.
func (m *model) fitMenu(menu *overlay.Menu) {
	menu.MaxWidth = m.width
	menu.MaxHeight = max(5, m.height-m.bodyTop()-1)
}

// menuLabels are the pane menus' short labels for the actions' longer
// help texts; the help and the palette keep the long ones.
var menuLabels = map[string]string{
	"Attach to its terminal":                             "Attach",
	"Edit its config entry":                              "Edit config entry",
	"Shell in its folder":                                "Shell in folder",
	"Open folder in editor":                              "Open in editor",
	"Open a terminal app there":                          "Open in terminal app",
	"Disable (skipped by --all and restore)":             "Disable",
	"Start and take over the port":                       "Take over the port",
	"Clear the daemon's scrollback":                      "Clear scrollback",
	"Shell in the container":                             "Shell",
	"Open the first published port in the browser":       "Open in browser",
	"Copy the first published port's address":            "Copy address",
	"Recreate the service":                               "Recreate",
	"Rebuild the image and recreate":                     "Rebuild and recreate",
	"Pull the image and recreate":                        "Pull and recreate",
	"Start the project's containers":                     "Start project",
	"Stop the project's containers":                      "Stop project",
	"Remove the project's containers (volumes are kept)": "Remove project",
	"Dismiss the finished compose job":                   "Dismiss job",
	"Kill the process (asks first)":                      "Kill process",
	"Start the blocked server, taking over the port":     "Take over the port",
	"Copy the address":                                   "Copy address",
	"Only matching lines":                                "Only matching",
	"Follow new output":                                  "Follow",
}

// menuSection files a menu item under the part of the target it acts on:
// a server's group, a container's project, else the target itself.
func menuSection(t ui.Selection, id string) string {
	switch {
	case t.Kind == ui.SelectServer && strings.HasSuffix(id, "-group"):
		return "Group"
	case t.Kind == ui.SelectServer:
		return "Server"
	case t.Kind == ui.SelectContainer && strings.HasSuffix(id, "-project"):
		return "Project"
	case t.Kind == ui.SelectContainer:
		return "Container"
	}
	return ""
}

// finishMenu gives a pane menu its short labels and sections, the target's
// own actions before its group's or project's, and the last entry, which
// opens the palette with every action.
func (m *model) finishMenu(menu overlay.Menu, t ui.Selection) overlay.Menu {
	if len(menu.Items) == 0 {
		return menu
	}
	for i := range menu.Items {
		it := &menu.Items[i]
		if l, ok := menuLabels[it.Label]; ok {
			it.Label = l
		}
		it.Section = menuSection(t, it.ActionID)
	}
	slices.SortStableFunc(menu.Items, func(a, b overlay.MenuItem) int {
		return b2i(a.Section == "Group" || a.Section == "Project") - b2i(b.Section == "Group" || b.Section == "Project")
	})
	if a, ok := m.env.Keys.ByID("palette"); ok {
		it := overlay.ItemFor(a, m.ascii())
		it.Label = "All actions"
		menu.Items = append(menu.Items, it)
	}
	return menu
}

// paneMenu is the menu of the pane at index pane and the target its
// actions take, resolved for what the pane shows: the servers' menu acts
// on the cursor's target (a server, with its group's actions, or a group),
// the Details, Env and Metrics tabs' on the shown server or container, and
// the logs' own actions on the shown source. Every menu ends with the
// palette; the config, the computers, the daemon and the agents are
// there, on the command line and in the help, not in a pane's menu.
func (m *model) paneMenu(pane int) (overlay.Menu, ui.Selection) {
	menu, t := m.paneActions(pane)
	return m.finishMenu(menu, t), t
}

// paneActions are the actions of paneMenu before finishMenu.
func (m *model) paneActions(pane int) (overlay.Menu, ui.Selection) {
	switch scope := m.panes()[pane].Scope(); scope {
	case keymap.ScopeServers:
		t := m.cursorTarget()
		menu := m.serverMenu(t)
		menu.Items = slices.DeleteFunc(menu.Items, func(it overlay.MenuItem) bool { return it.ActionID == "new" })
		return menu, t
	case keymap.ScopeDetail:
		if m.env.Selected.Kind == ui.SelectContainer {
			return m.shownItemMenu(), m.env.Selected
		}
		return m.shownServerMenu(), m.env.Selected
	case keymap.ScopeLogs:
		return m.logsMenu(), ui.Selection{}
	case keymap.ScopeContainers, keymap.ScopePorts:
		t := m.cursorTarget()
		if pane == len(m.left) {
			t = m.env.Selected
		}
		return m.itemMenu(scope, t), t
	default:
		menu := overlay.MenuFor(m.env.Keys, scope, m.ascii())
		if m.env.Selected.Kind == ui.SelectServer {
			menu.Title = scope.Title() + " of " + m.env.Selected.Name
		}
		return menu, ui.Selection{}
	}
}

// hasMenu reports whether the focused pane has a menu, for the footer.
func (m *model) hasMenu() bool {
	menu, _ := m.paneMenu(m.focus)
	return len(menu.Items) > 0
}

// openMenuAtCursor opens the focused pane's menu in the middle of the
// screen (x).
func (m *model) openMenuAtCursor() tea.Cmd {
	menu, _ := m.paneMenu(m.focus)
	m.fitMenu(&menu)
	w, h := menu.Size()
	return m.openMenu(m.focus, (m.width-w)/2, (m.height-h)/2)
}

// menuKey handles a key while the menu is open: j/k move, enter and space
// run the highlighted item (as in lazydocker's menus), an item's own key
// runs it, and esc, q or x close the menu.
func (m *model) menuKey(k string) tea.Cmd {
	switch k {
	case "j", "down":
		m.menu.Down()
	case "k", "up":
		m.menu.Up()
	case "enter", "space":
		return m.menuRun(m.menu.Cursor)
	case "esc", "q", "x":
		m.menu = nil
	default:
		label := overlay.KeyLabel(k, m.ascii())
		for i, it := range m.menu.Items {
			if it.Key != "" && it.Key == label {
				return m.menuRun(i)
			}
		}
	}
	return nil
}

// menuClick handles a click while the context menu is open: a left click
// on an item runs it, a click outside closes the menu.
func (m *model) menuClick(ms tea.Mouse) tea.Cmd {
	c := m.menu
	w, h := c.Size()
	if ms.X < c.x || ms.X >= c.x+w || ms.Y < c.y || ms.Y >= c.y+h {
		m.menu = nil
		return nil
	}
	if i := c.Hit(ms.Y - c.y); i >= 0 && ms.Button == tea.MouseLeft {
		return m.menuRun(i)
	}
	return nil
}

// menuRun closes the menu and runs its item i on the menu's pane; an item
// that is off says why instead.
func (m *model) menuRun(i int) tea.Cmd {
	c := m.menu
	m.menu = nil
	if i < 0 || i >= len(c.Items) {
		return nil
	}
	it := c.Items[i]
	if it.Off != "" {
		return m.warn(strings.TrimSpace(it.Label + ": " + it.Off))
	}
	m.focusPane(c.pane)
	switch c.target.Kind {
	case ui.SelectContainer, ui.SelectProject, ui.SelectPort:
		// Every container, project and port action of a menu runs on
		// its target, from the list or from the main panel alike.
		if it.ActionID != "start-docker" && it.ActionID != "palette" {
			return m.act(it.ActionID, c.target)
		}
	case ui.SelectServer, ui.SelectGroup:
		if targetActions[it.ActionID] {
			return m.act(it.ActionID, c.target)
		}
	}
	scope := m.panes()[c.pane].Scope()
	for _, a := range m.env.Keys.InScope(scope, keymap.ScopeGlobal) {
		if a.ID == it.ActionID {
			return m.run(a)
		}
	}
	return nil
}

// withOverlays draws the toasts, the menu, the palette and the help or
// messages overlay over the screen.
func (m *model) withOverlays(out string) string {
	s, g := m.env.Styles, m.env.Glyphs
	m.toastAt = toastHit{}
	if t := overlay.Toasts(s, g, m.toasts, m.env.Now(), m.width, m.narrow()); t.View != "" {
		w, _ := boxSize(t.View)
		m.toastAt = toastHit{x: m.width - w, y: m.bodyTop(), w: w, stack: t}
		out = overlay.Place(out, t.View, m.width-w, m.bodyTop())
	}
	if c := m.menu; c != nil {
		// The highlighted item's reason can make the box taller: it stays
		// above the footer, and clicks follow it.
		_, h := c.Size()
		c.y = max(m.bodyTop(), min(c.y, m.height-1-h))
		out = overlay.Place(out, c.View(s, g), c.x, c.y)
	}
	if p := m.palette; p != nil {
		box := p.View(s, g, m.ascii(), m.width, m.height)
		w, _ := boxSize(box)
		p.x, p.y = max(0, (m.width-w)/2), max(0, min(m.height/5, m.height-lipgloss.Height(box)))
		out = overlay.Place(out, box, p.x, p.y)
	}
	switch {
	case m.help != nil:
		out = overlay.Center(out, m.help.View(s, g, m.env.Keys, m.helpScopes(), m.ascii(), m.width, m.height))
	case m.messages != nil:
		out = overlay.Center(out, m.messagesView())
	}
	return out
}

// helpScopes are the help's sections: the focused pane's scope, the
// global one, then the scopes of the other panes on screen.
func (m *model) helpScopes() []keymap.Scope {
	order := []keymap.Scope{m.focused().Scope(), keymap.ScopeGlobal}
	add := func(sc keymap.Scope) {
		for _, o := range order {
			if o == sc {
				return
			}
		}
		order = append(order, sc)
	}
	for _, p := range m.left {
		add(p.Scope())
	}
	// The main panel's tabs: logs, and the details, env and metrics.
	add(keymap.ScopeLogs)
	add(keymap.ScopeDetail)
	return order
}

// toastClick selects the server of the toast under the pointer; false
// when the pointer is not on a toast.
func (m *model) toastClick(x, y int) (tea.Cmd, bool) {
	t := m.toastAt
	if t.w == 0 || x < t.x || x >= t.x+t.w {
		return nil, false
	}
	for _, b := range t.stack.Boxes {
		if y >= t.y+b.Y0 && y < t.y+b.Y1 {
			if b.Item.Kind == ui.SelectServer && m.hasServer(b.Item.Name) {
				return m.selectServer(b.Item), true
			}
			if _, ok := containers.Find(m.env.State, b.Item); ok {
				return m.selectContainer(b.Item), true
			}
			return nil, true
		}
	}
	return nil, false
}

// boxSize is the width and height of a rendered box.
func boxSize(box string) (w, h int) {
	rows := strings.Split(box, "\n")
	for _, r := range rows {
		w = max(w, ansi.StringWidth(r))
	}
	return w, len(rows)
}

// messagesView renders the message history, newest last, as wide as its
// longest message (at least 40 cells, at most 100) and at most h-4 rows
// tall, with the highlighted message marked, and its
// keys: moving when there are messages, selecting the highlighted
// message's server or container when enter can, closing.
func (m *model) messagesView() string {
	s := overlay.Styles(m.env.Styles)
	widest := 0
	for _, e := range m.history {
		widest = max(widest, len("15:04:05  ")+ansi.StringWidth(plainText(e.Text)))
	}
	inner := max(min(100, m.width)-4, 1)
	inner = min(inner, max(widest, 36))
	l := m.messages
	rows := max(min(m.height-4, len(m.history)+4)-4, 1)
	l.cursor = max(0, min(l.cursor, len(m.history)-1))
	if l.cursor < l.offset {
		l.offset = l.cursor
	}
	if l.cursor >= l.offset+rows {
		l.offset = l.cursor - rows + 1
	}
	l.rows = rows
	var lines []string
	for i := l.offset; i < min(len(m.history), l.offset+rows); i++ {
		e := m.history[i]
		line := s.Muted.Render(e.At.Format("15:04:05")) + s.Surface.Render("  ") + levelStyle(s, e.Level).Render(plainText(e.Text))
		if i == l.cursor {
			line = overlay.Highlight(s, line, inner)
		}
		lines = append(lines, line)
	}
	if len(lines) == 0 {
		lines = append(lines, s.Muted.Render("No messages yet"))
	}
	var pairs [][2]string
	if len(m.history) > 1 {
		pairs = append(pairs, [2]string{"j/k", "move"})
	}
	if it, ok := m.messageTarget(); ok {
		pairs = append(pairs, [2]string{overlay.KeyLabel("enter", m.ascii()), "select " + plainText(it.Name)})
	}
	pairs = append(pairs, [2]string{"esc", "close"})
	lines = append(lines, "", overlay.HintFit(m.env.Styles, m.env.Glyphs, inner, pairs...))
	return overlay.Frame(m.env.Styles, m.env.Glyphs, "Messages", lines, inner)
}

// levelStyle is a notice level's color.
func levelStyle(s theme.Styles, l ui.Level) lipgloss.Style {
	switch l {
	case ui.LevelError:
		return s.Error
	case ui.LevelWarn:
		return s.Warn
	case ui.LevelSuccess:
		return s.Running
	}
	return s.Text2
}

// shownItemMenu is the menu of a container's Details tab: the shown
// container's actions, with their commands, since the list's keys are
// not bound there.
func (m *model) shownItemMenu() overlay.Menu {
	menu := m.itemMenu(keymap.ScopeContainers, m.env.Selected)
	for i := range menu.Items {
		menu.Items[i].Key = ":" + menu.Items[i].ActionID
	}
	return menu
}

// shownLogs is the log pane on screen: the shown container's (its logs or
// its job) or the shown server's; nil before the panes exist.
func (m *model) shownLogs() *logs.Pane {
	main, ok := m.right.(*containers.Main)
	if !ok {
		return nil
	}
	if m.env.Selected.Kind == ui.SelectContainer {
		return main.Container.Logs()
	}
	if d, ok := main.Server.(*detail.Pane); ok {
		return d.Logs()
	}
	return nil
}

// logsMenu is the Logs tab's menu for the source it shows: toggles with
// their state, attach (a shell for a container) with why it is off, and
// clearing only where there is a scrollback to clear.
func (m *model) logsMenu() overlay.Menu {
	sel := m.env.Selected
	menu := overlay.MenuFor(m.env.Keys, keymap.ScopeLogs, m.ascii())
	switch sel.Kind {
	case ui.SelectServer:
		menu.Title = "Logs of " + sel.Name
	case ui.SelectContainer:
		menu.Title = "Logs of container " + sel.Name
	}
	lp := m.shownLogs()
	for i := range menu.Items {
		it := &menu.Items[i]
		switch it.ActionID {
		case "follow", "wrap", "timestamps", "keep-matching":
			if lp != nil {
				it.Check = overlay.CheckOff
				if lp.On(it.ActionID) {
					it.Check = overlay.CheckOn
				}
			}
		case "attach":
			if sel.Kind == ui.SelectContainer {
				it.Label = "Shell in the container"
			}
			it.Off = m.logsOff(it.ActionID)
		case "clear-logs":
			it.Off = m.logsOff(it.ActionID)
		}
	}
	return menu
}
