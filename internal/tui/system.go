package tui

import (
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/padmk/portside/internal/client"
	"github.com/padmk/portside/internal/tui/edit"
	"github.com/padmk/portside/internal/tui/keymap"
	"github.com/padmk/portside/internal/tui/live"
	"github.com/padmk/portside/internal/tui/overlay"
	"github.com/padmk/portside/internal/tui/remote"
	"github.com/padmk/portside/internal/tui/ui"
)

// systemGlobals are the global actions about other computers, the daemon
// and the coding agents (internal/tui/remote), by id.
func (m *model) systemGlobals() map[string]func() tea.Cmd {
	return map[string]func() tea.Cmd{
		"computers":      m.sys.Computers,
		"on":             func() tea.Cmd { return m.switchTo("") },
		"pair":           m.sys.Computers,
		"daemon":         func() tea.Cmd { m.sys.Daemon(); return nil },
		"upgrade":        func() tea.Cmd { return m.sys.Upgrade(&m.env) },
		"restart-daemon": func() tea.Cmd { return m.sys.RestartDaemon(&m.env) },
		"setup":          func() tea.Cmd { return m.sys.Setup(&m.env) },
		"doctor":         func() tea.Cmd { return m.sys.Doctor(&m.env) },
	}
}

// systemCommand runs the system commands that take an argument: `:on
// <computer>` and `:pair <computer>`; ok is false for the others.
func (m *model) systemCommand(a keymap.Action, arg string) (tea.Cmd, bool) {
	switch a.ID {
	case "on":
		return m.switchTo(arg), true
	case "pair":
		if arg != "" {
			return m.sys.AskPair(remote.Peer{Name: arg, DNSName: arg}), true
		}
	}
	return nil, false
}

// systemOff is why a system action does nothing here, "" when it acts.
func (m *model) systemOff(id string) string {
	switch id {
	case "upgrade":
		return remote.UpgradeOff(&m.env)
	case "restart-daemon":
		return remote.RestartOff(&m.env)
	case "setup", "doctor":
		return remote.SetupOff(&m.env)
	}
	return ""
}

// withSystemMenu adds the computers, the daemon and the agent setup to
// the servers' menu on a server or an empty list, after the config.
func (m *model) withSystemMenu(menu overlay.Menu, t ui.Selection) overlay.Menu {
	if t.Kind == ui.SelectGroup {
		return menu
	}
	for _, a := range m.env.Keys.InScope(keymap.ScopeGlobal) {
		if a.Group != "Computers and daemon" || !a.Menu {
			continue
		}
		if slices.ContainsFunc(menu.Items, func(it overlay.MenuItem) bool { return it.ActionID == a.ID }) {
			continue
		}
		it := overlay.ItemFor(a, m.ascii())
		it.Off = m.systemOff(a.ID)
		menu.Items = append(menu.Items, it)
	}
	return menu
}

// switchTo switches the session to the computer called name, or to this
// one for "" and "local" (`:on`).
func (m *model) switchTo(name string) tea.Cmd {
	s := m.env.Session
	switch {
	case remote.IsLocal(name) && !s.Remote():
		return m.flash(ui.NoticeMsg{Text: "this session is on this computer; :computers lists the others", Level: ui.LevelInfo}, noticeTime)
	case !remote.IsLocal(name) && s.Remote() && s.Conn != nil && s.Conn.Is(name):
		return m.flash(ui.NoticeMsg{Text: "this session is on " + s.Computer + " already", Level: ui.LevelInfo}, noticeTime)
	}
	where := name
	if remote.IsLocal(name) {
		where = "this computer"
	}
	return tea.Batch(m.flash(ui.NoticeMsg{Text: "switching to " + where + m.env.Glyphs.Ellipsis, Level: ui.LevelInfo}, noticeTime),
		remote.Switch(m.sys.Local, name))
}

// switched takes the outcome of a switch: a computer that needs pairing
// opens the code prompt, another failure keeps the session.
func (m *model) switched(msg remote.SwitchedMsg) tea.Cmd {
	if msg.Err != nil {
		fail := m.fail("on " + msg.Computer + ": " + msg.Err.Error())
		if msg.Pair.Name != "" {
			return tea.Batch(fail, m.sys.AskPair(msg.Pair))
		}
		return fail
	}
	m.sys.Close()
	return m.replaceSession(msg.Client, ui.Session{Computer: msg.Computer, Conn: msg.Conn})
}

// replaceSession makes the session target client c on another computer
// (or this one): a new generation, so late replies of the previous one
// are dropped (remote.Tag), fresh panes, selection, operations and
// toasts, and a live stream to the new computer.
func (m *model) replaceSession(c *client.Client, s ui.Session) tea.Cmd {
	m.gen++
	if cl, ok := m.right.(interface{ Close() }); ok {
		cl.Close()
	}
	m.env.Client, m.env.Session = c, s
	m.env.State, m.env.Connected = nil, false
	m.env.Selected, m.env.Target = ui.Selection{}, ui.Selection{}
	m.env.Pending = map[ui.Selection]string{}
	m.ops = map[ui.Selection]op{}
	m.placed, m.stateFilter, m.connErr, m.retry = false, "", "", time.Time{}
	m.menu, m.palette, m.help, m.messages, m.cmdLine = nil, nil, nil, nil, nil
	m.confirm, m.dialog = nil, nil
	m.edit = edit.Editor{}
	m.toasting = toasting{}
	m.itemPanes = itemPanes{}
	m.left, m.right = newPanes()
	m.initLeft()
	m.focus, m.leftActive = 0, 0
	m.setMode(ui.ModeNormal, -1)
	m.spinning, m.clocking = false, false
	m.stream = live.New()
	if m.streams != nil {
		m.streams.Start(m.stream, c, m.gen)
	}
	m.layout()
	where := "this computer"
	if s.Remote() {
		where = s.Computer
	}
	text := "session on " + where
	if s.Remote() {
		text += "; :on local returns"
	}
	return tea.Batch(m.broadcast(tea.WindowSizeMsg{Width: m.width, Height: m.height}),
		m.show(ui.NoticeMsg{Text: text, Level: ui.LevelInfo}, noticeTime), m.clock(),
		m.sys.WatchConnection(&m.env))
}

// portHit reports whether x on the top bar is on the daemon's port.
func (m *model) portHit(x int) bool {
	return m.portAt[1] > m.portAt[0] && x >= m.portAt[0] && x < m.portAt[1]
}

// restartingLabel is the top bar's right end while the daemon restarts.
func (m *model) restartingLabel() string {
	r := m.sys.Restarting()
	if r == "" {
		return ""
	}
	return m.env.Styles.Warn.Render(strings.TrimSpace(r) + m.env.Glyphs.Ellipsis)
}
