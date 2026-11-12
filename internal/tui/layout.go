package tui

import (
	"cmp"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/padmk/portside/internal/app"
	"github.com/padmk/portside/internal/tui/overlay"
	"github.com/padmk/portside/internal/tui/theme"
	"github.com/padmk/portside/internal/tui/ui"
)

// Layout sizes (design/tui.md "Layout").
const (
	wideCols   = 120
	narrowCols = 80
	narrowRows = 24
	mediumLeft = 38
	minLeft    = 34
	maxLeft    = 60
	minRight   = 50
)

// rect is a pane's place on the screen; pane indexes panes(). A compact
// rect is a left-column pane collapsed to its header row.
type rect struct {
	x, y, w, h, pane int
	compact          bool
}

func (r rect) contains(x, y int) bool {
	return x >= r.x && x < r.x+r.w && y >= r.y && y < r.y+r.h
}

func (m *model) wide() bool   { return !m.narrow() && m.width >= wideCols }
func (m *model) narrow() bool { return m.width < narrowCols || m.height < narrowRows }

// leftWidth is the left column's width in the wide and medium layouts:
// half the screen in the half screen mode.
func (m *model) leftWidth() int {
	switch {
	case m.screenMode == screenHalf:
		return m.width / 2
	case !m.wide():
		return mediumLeft
	}
	return max(minLeft, min(m.width*40/100, maxLeft, m.width-minRight))
}

// bodyTop is the first row below the top bar and the banner.
func (m *model) bodyTop() int {
	if _, ok := m.banner(); ok {
		return 2
	}
	return 1
}

// footHeight is the rows the footer takes: two while the command line
// shows completions, else one.
func (m *model) footHeight() int {
	if m.cmdLine != nil {
		return m.cmdLine.Rows()
	}
	return 1
}

// bodyHeight is the rows between the top bar (and the banner) and the
// footer.
func (m *model) bodyHeight() int { return max(0, m.height-m.bodyTop()-m.footHeight()) }

// layout places the panes between the bars: one pane over the whole body
// when narrow, in the full screen mode, or for the main panel in the half
// one, else the left column next to the right pane. In the left column the
// active pane (the focused one, or the last focused) takes the height the
// others leave: each of them is a one-row header with its count, as
// lazygit collapses side panels, until the column has room for them all.
func (m *model) layout() {
	top := m.bodyTop()
	h := m.bodyHeight()
	m.rects = m.rects[:0]
	alone := m.screenMode == screenFull || (m.screenMode == screenHalf && m.focus == len(m.left))
	if alone || m.narrow() {
		m.rects = append(m.rects, rect{x: 0, y: top, w: m.width, h: h, pane: m.focus})
		return
	}
	lw := m.leftWidth()
	n := len(m.left)
	active := min(m.leftActive, n-1)
	y := top
	for i := range m.left {
		ph := 1
		if i == active {
			ph = max(1, h-(n-1))
		}
		m.rects = append(m.rects, rect{x: 0, y: y, w: lw, h: ph, pane: i, compact: i != active})
		y += ph
	}
	m.rects = append(m.rects, rect{x: lw, y: top, w: m.width - lw, h: h, pane: n})
}

// mouse passes a mouse message to the pane under the pointer, with
// coordinates relative to the pane; a click also focuses the pane and a
// right click opens its context menu. Open overlays and the bars come
// first.
func (m *model) mouse(msg tea.MouseMsg) tea.Cmd {
	ms := msg.Mouse()
	click, isClick := msg.(tea.MouseClickMsg)
	switch {
	case m.confirm != nil:
		if isClick && ms.Button == tea.MouseLeft {
			return m.dialogClick(ms.X, ms.Y)
		}
		return nil
	case m.edit.Open():
		return m.edit.Mouse(&m.env, msg)
	case m.sys.Open():
		return m.sys.Mouse(msg)
	case m.palette != nil:
		if isClick {
			return m.paletteClick(ms)
		}
		return nil
	case m.help != nil || m.messages != nil:
		if _, ok := msg.(tea.MouseWheelMsg); ok {
			m.scrollOverlay(map[tea.MouseButton]int{tea.MouseWheelDown: 3, tea.MouseWheelUp: -3}[ms.Button])
		}
		return nil
	case m.menu != nil:
		if isClick {
			return m.menuClick(click.Mouse())
		}
		if _, ok := msg.(tea.MouseWheelMsg); ok {
			switch ms.Button {
			case tea.MouseWheelDown:
				m.menu.Down()
			case tea.MouseWheelUp:
				m.menu.Up()
			}
		}
		return nil
	case isClick && ms.Button == tea.MouseLeft && m.cmdLine == nil:
		if cmd, ok := m.toastClick(ms.X, ms.Y); ok {
			return cmd
		}
		if cmd, ok := m.barClick(ms.X, ms.Y); ok {
			return cmd
		}
	}
	i := slices.IndexFunc(m.rects, func(r rect) bool { return r.contains(ms.X, ms.Y) })
	if i < 0 {
		return nil
	}
	r := m.rects[i]
	if r.compact {
		if isClick && m.mode == ui.ModeNormal {
			m.focusPane(r.pane)
		}
		return nil
	}
	// An input mode belongs to its pane; clicks elsewhere wait until it
	// ends, so its esc still reaches it.
	if isClick && m.mode != ui.ModeNormal && r.pane != m.modeOwner {
		return nil
	}
	px, py := ms.X, ms.Y
	ms.X -= r.x
	ms.Y -= r.y
	if isClick && ms.Button == tea.MouseRight {
		// The row under the pointer becomes the target first, from any
		// pane: focus, cursor and target are reconciled before the menu
		// is built, and the menu keeps that target.
		m.focusPane(r.pane)
		cmd := m.update(r.pane, tea.MouseClickMsg(ms))
		sync := m.followSelection()
		return tea.Batch(cmd, sync, m.openMenu(r.pane, px, py))
	}
	var out tea.Msg
	switch msg.(type) {
	case tea.MouseClickMsg:
		m.focusPane(r.pane)
		out = tea.MouseClickMsg(ms)
	case tea.MouseReleaseMsg:
		out = tea.MouseReleaseMsg(ms)
	case tea.MouseWheelMsg:
		out = tea.MouseWheelMsg(ms)
	case tea.MouseMotionMsg:
		out = tea.MouseMotionMsg(ms)
	default:
		return nil
	}
	return m.update(r.pane, out)
}

// barClick runs what a click on the bars names: a state count filters the
// servers to that state (a second click clears it), a footer hint runs its
// action.
func (m *model) barClick(x, y int) (tea.Cmd, bool) {
	switch y {
	case 0:
		if m.portHit(x) {
			m.sys.Daemon()
			return nil, true
		}
		for _, h := range m.counts {
			if x >= h.x0 && x < h.x1 {
				if m.stateFilter == string(h.states[0]) {
					return m.setStateFilter(nil, ""), true
				}
				return m.setStateFilter(h.states, string(h.states[0])), true
			}
		}
	case m.height - 1:
		for _, h := range m.hints {
			if x >= h.x0 && x < h.x1 {
				return m.run(h.action), true
			}
		}
	}
	return nil, false
}

func (m *model) View() tea.View {
	v := tea.NewView(m.screen())
	v.AltScreen = true
	v.MouseMode = tea.MouseModeCellMotion
	v.WindowTitle = app.Name
	if c := m.env.Session.Computer; c != "" {
		v.WindowTitle += " · " + c
	}
	return v
}

// screen renders exactly the terminal's size: the bars and the panes,
// sized around the footer's rows; under a modal overlay all but the
// footer is dimmed once (overlay.Dimmed), then the toasts and the overlays
// are drawn over it.
func (m *model) screen() string {
	if m.width <= 0 || m.height <= 0 {
		return ""
	}
	top := m.bodyTop()
	// The command line's completions take a row from the body, so the
	// panes are laid out again around the footer.
	m.layout()
	foot := m.footerRows()
	lines := []string{m.topBar()}
	if b, ok := m.banner(); ok {
		lines = append(lines, b)
	}
	lines = append(lines, m.body()...)
	lines = append(lines, foot...)
	if len(lines) > m.height {
		keep := max(0, m.height-len(foot))
		lines = append(lines[:keep], foot[max(0, len(foot)-(m.height-keep)):]...)
	}
	// The bars sit on the screen's background; the panes paint their own.
	body := len(lines) - len(foot)
	for i := range lines {
		if i == 0 || i >= body || (i == 1 && top > 1) {
			lines[i] = theme.Fill(m.env.Styles.Screen, lines[i], m.width)
			continue
		}
		lines[i] = ui.Fit(lines[i], m.width)
	}
	if m.modal() {
		// The footer stays as it is: it holds the overlay's keys.
		dim := strings.Split(overlay.Dimmed(m.env.Styles, strings.Join(lines[:body], "\n"), m.width), "\n")
		lines = append(dim, lines[body:]...)
	}
	out := strings.Join(lines, "\n")
	out = m.withOverlays(out)
	if m.edit.Open() {
		out = m.edit.View(&m.env, out, m.width, m.height)
	}
	if m.sys.Open() {
		out = m.sys.View(&m.env, out, m.width, m.height)
	}
	if m.confirm != nil {
		out = m.withDialog(out)
	}
	return out
}

// modal reports whether an overlay that takes the keys is open: the menu,
// the palette, help, messages, a confirmation, a form or a system overlay.
// The command line and the toasts are not modal.
func (m *model) modal() bool {
	return m.menu != nil || m.palette != nil || m.help != nil || m.messages != nil ||
		m.confirm != nil || m.edit.Open() || m.sys.Open()
}

// body renders the panes row by row.
func (m *model) body() []string {
	panes := m.panes()
	rects := slices.Clone(m.rects)
	slices.SortStableFunc(rects, func(a, b rect) int { return cmp.Compare(a.x, b.x) })
	views := make(map[int][]string, len(rects))
	for _, r := range rects {
		if r.compact {
			views[r.pane] = []string{m.compactHeader(r)}
			continue
		}
		views[r.pane] = strings.Split(panes[r.pane].View(&m.env, r.w, r.h, r.pane == m.focus), "\n")
	}
	top := m.bodyTop()
	h := m.bodyHeight()
	rows := make([]string, h)
	for y := range h {
		var b strings.Builder
		for _, r := range rects {
			if y+top < r.y || y+top >= r.y+r.h {
				continue
			}
			line := ""
			if lines := views[r.pane]; y+top-r.y < len(lines) {
				line = lines[y+top-r.y]
			}
			b.WriteString(ui.Fit(line, r.w))
		}
		rows[y] = b.String()
	}
	return rows
}

// compactHeader is a collapsed left-column pane's row: its jump key,
// its title and count (ui.Titled), else its scope's name.
func (m *model) compactHeader(r rect) string {
	p := m.panes()[r.pane]
	title, count := p.Scope().Title(), ""
	if t, ok := p.(ui.Titled); ok {
		title, count = t.Title(&m.env)
	}
	return ui.HeaderRow(&m.env, r.w, ui.JumpKey(&m.env, p.Scope()), title, count)
}

// withDialog draws the confirmation dialog centered over the screen and
// keeps its place for clicks.
func (m *model) withDialog(screen string) string {
	d := m.dialog
	box := d.View(m.env.Styles, m.env.Glyphs, m.width)
	bw, bh := boxSize(box)
	m.dialogAt = [2]int{max(0, (m.width-bw)/2), max(0, (m.height-bh)/2)}
	dialog := lipgloss.NewLayer(box).X(m.dialogAt[0]).Y(m.dialogAt[1]).Z(1)
	canvas := lipgloss.NewCanvas(m.width, m.height)
	canvas.Compose(lipgloss.NewCompositor(lipgloss.NewLayer(screen), dialog))
	return canvas.Render()
}

// dialogClick runs the choice under the pointer.
func (m *model) dialogClick(x, y int) tea.Cmd {
	switch m.dialog.Hit(x-m.dialogAt[0], y-m.dialogAt[1]) {
	case 0:
		return m.decide(false)
	case 1:
		return m.decide(true)
	}
	return nil
}
