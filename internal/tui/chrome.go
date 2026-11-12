package tui

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/padmk/portside/internal/app"
	"github.com/padmk/portside/internal/supervisor"
	"github.com/padmk/portside/internal/tui/keymap"
	"github.com/padmk/portside/internal/tui/overlay"
	"github.com/padmk/portside/internal/tui/ui"
	"github.com/padmk/portside/internal/viewfmt"
)

// countOrder is the top bar's order of display states; each entry counts
// the states listed after its first, which also picks glyph and style.
var countOrder = [][]supervisor.Display{
	{supervisor.Display(supervisor.Running)},
	{supervisor.Display(supervisor.Starting), supervisor.Display(supervisor.Stopping)},
	{supervisor.DisplayUnready},
	{supervisor.Display(supervisor.Crashed), supervisor.Display(supervisor.Blocked)},
	{supervisor.Display(supervisor.Stopped), supervisor.Display(supervisor.Exited)},
	{supervisor.DisplayDone},
	{supervisor.DisplayExternal},
}

// countHit is where a top bar count is, and the states it counts.
type countHit struct {
	x0, x1 int
	states []supervisor.Display
}

// hintHit is where a footer key hint is, and its action.
type hintHit struct {
	x0, x1 int
	action keymap.Action
}

// topBar is the mark, name and port, the counts per state, and the
// computer and version or the reconnect countdown at the right.
func (m *model) topBar() string {
	s, g, st := m.env.Styles, m.env.Glyphs, m.env.State
	left := " "
	m.portAt = [2]int{}
	if !m.narrow() {
		mark := " " + s.AccentBold.Render(g.Logo) + " " + s.Bold.Render(app.Name)
		if st != nil && st.Daemon.Port > 0 {
			x := lipgloss.Width(mark) + 2
			mark += "  " + s.Port.Render(":"+strconv.Itoa(st.Daemon.Port))
			m.portAt = [2]int{x, lipgloss.Width(mark)}
		}
		left = mark + "   "
	}
	left += m.countsAt(lipgloss.Width(left))
	var right string
	switch {
	case m.restartingLabel() != "":
		right = m.restartingLabel()
	case !m.env.Connected && !m.retry.IsZero():
		secs := int((m.retry.Sub(m.env.Now()) + time.Second - 1) / time.Second)
		right = s.Warn.Render("reconnecting" + g.Ellipsis)
		if secs > 0 {
			right = s.Warn.Render(fmt.Sprintf("reconnecting in %ds", secs)) + s.Muted.Render(" (R now)")
		}
	case !m.env.Connected:
		right = s.Muted.Render("connecting" + g.Ellipsis)
	default:
		var parts []string
		if c := m.env.Session.Computer; c != "" {
			parts = append(parts, s.Text2.Render(plainText(c)))
		}
		if st != nil && st.Daemon.Version != "" {
			parts = append(parts, s.Muted.Render(versionLabel(st.Daemon.Version)))
		}
		right = strings.Join(parts, s.Muted.Render(m.env.Glyphs.Separator))
	}
	if c := m.env.Session.Computer; c != "" && !m.env.Connected {
		// The computer the session waits for, after :on or --on.
		right = s.Text2.Render(plainText(c)) + s.Muted.Render(m.env.Glyphs.Separator) + right
	}
	gap := m.width - lipgloss.Width(left) - lipgloss.Width(right) - 1
	if right == "" || gap < 1 {
		return left
	}
	return left + strings.Repeat(" ", gap) + right + " "
}

// countsAt is one glyph and number per display state present, starting at
// column x; it keeps where each is for clicks.
func (m *model) countsAt(x int) string {
	m.counts = m.counts[:0]
	st := m.env.State
	if st == nil {
		return ""
	}
	n := map[supervisor.Display]int{}
	for _, v := range st.Servers {
		n[v.Display()]++
	}
	s := m.env.Styles
	var parts []string
	for _, group := range countOrder {
		total := 0
		for _, d := range group {
			total += n[d]
		}
		if total == 0 {
			continue
		}
		style := s.State(string(group[0]))
		if !m.env.Connected {
			style = s.Muted
		}
		part := style.Render(m.env.Glyphs.State(group[0], m.env.Frame) + " " + strconv.Itoa(total))
		if len(parts) > 0 {
			x += 2
		}
		w := lipgloss.Width(part)
		m.counts = append(m.counts, countHit{x0: x, x1: x + w, states: group})
		x += w
		parts = append(parts, part)
	}
	return strings.Join(parts, "  ")
}

func versionLabel(v string) string {
	if v != "" && v[0] >= '0' && v[0] <= '9' {
		return "v" + v
	}
	return v
}

// banner is the line under the top bar for a config error or warnings.
func (m *model) banner() (string, bool) {
	st := m.env.State
	if st == nil {
		return "", false
	}
	s := m.env.Styles
	if st.Config.Error != "" {
		return s.BannerError.Render(ui.Fit(" config error: "+plainText(st.Config.Error), m.width)), true
	}
	if w := st.Config.Warnings; len(w) > 0 {
		text := " " + plainText(w[0])
		if len(w) > 1 {
			text += fmt.Sprintf(" (+%d more)", len(w)-1)
		}
		return s.Banner.Render(ui.Fit(text, m.width)), true
	}
	return "", false
}

// footerRows are the bottom rows: the command line while it is open (two
// rows with its completions), else the footer.
func (m *model) footerRows() []string {
	m.hints = m.hints[:0]
	if m.cmdLine != nil {
		return strings.Split(m.cmdLine.View(m.env.Styles, m.width), "\n")
	}
	return []string{m.footer()}
}

// footer is the input mode's pill outside the normal mode, then a notice,
// the hints of an open overlay or a mode, or the key hints, each kept with
// its place for clicks. The hints always keep room for x (when the
// focused pane has a menu) and ?, and in a narrow terminal for the way
// back to the list; the focused pane's primary actions, then the global
// ones, fill the rest in table order.
func (m *model) footer() string {
	s := m.env.Styles
	line := ""
	if m.mode != ui.ModeNormal {
		line = s.Mode.Render(m.mode.String()) + " "
	}
	if m.notice != nil {
		return line + levelStyle(s, m.notice.Level).Render(plainText(m.notice.Text))
	}
	if fixed, ok := m.fixedHints(); ok {
		parts := make([]string, len(fixed))
		for i, h := range fixed {
			parts[i] = s.Key.Render(h[0]) + " " + s.KeyHelp.Render(h[1])
		}
		return line + strings.Join(parts, s.Muted.Render(m.env.Glyphs.Separator))
	}
	type hint struct {
		a    keymap.Action
		text string
	}
	render := func(a keymap.Action, word string) hint {
		return hint{a, s.Key.Render(overlay.KeyLabel(a.Keys[0], m.ascii())) + " " + s.KeyHelp.Render(word)}
	}
	var reserved, primary []hint
	if m.narrow() && m.focus != 0 {
		if back, ok := m.env.Keys.Lookup("esc", m.focused().Scope()); ok {
			reserved = append(reserved, render(back, "back"))
		}
	}
	for _, id := range []string{"menu", "help"} {
		a, _ := m.env.Keys.ByID(id)
		if id == "menu" && !m.hasMenu() {
			continue
		}
		reserved = append(reserved, render(a, a.Short))
	}
	for _, a := range m.env.Keys.InScope(m.focused().Scope(), keymap.ScopeGlobal) {
		if !a.Footer || len(a.Keys) == 0 || a.ID == "menu" || a.ID == "help" || !m.applies(a) {
			continue
		}
		primary = append(primary, render(a, m.hintShort(a)))
	}
	width := func(hs []hint) int {
		w := 0
		for i, h := range hs {
			if i > 0 {
				w += 2
			}
			w += lipgloss.Width(h.text)
		}
		return w
	}
	avail := m.width - lipgloss.Width(line) - 1
	room := avail - width(reserved) - 2
	var shown []hint
	for _, h := range primary {
		if width(append(shown, h)) > room {
			break
		}
		shown = append(shown, h)
	}
	shown = append(shown, reserved...)
	for i, h := range shown {
		if i > 0 {
			line += "  "
		}
		x := lipgloss.Width(line)
		line += h.text
		m.hints = append(m.hints, hintHit{x0: x, x1: lipgloss.Width(line), action: h.a})
	}
	return line
}

// fixedHints are the footer's key and label pairs while an overlay or a
// mode other than normal takes the keys; ok is false when the action hints
// apply.
func (m *model) fixedHints() (hints [][2]string, ok bool) {
	enter := overlay.KeyLabel("enter", m.ascii())
	switch {
	case m.confirm != nil:
		return [][2]string{{"tab", "choose"}, {enter, "run the selected"}, {"y", "yes"}, {"n", "no"}}, true
	case m.edit.Open():
		return m.edit.Hints(m.ascii()), true
	case m.sys.Open():
		return m.sys.Hints(&m.env, m.ascii()), true
	case m.help != nil:
		return [][2]string{{"j/k", "scroll"}, {"esc", "close"}}, true
	case m.messages != nil:
		if len(m.history) == 0 {
			return [][2]string{{"esc", "close"}}, true
		}
		return [][2]string{{"j/k", "move"}, {enter, "select its server"}, {"esc", "close"}}, true
	case m.menu != nil:
		return [][2]string{{"j/k", "move"}, {enter, "run"}, {"esc", "close"}}, true
	case m.palette != nil:
		return [][2]string{{"type", "to find"}, {enter, "run"}, {"esc", "close"}}, true
	}
	switch m.mode {
	case ui.ModeFilter, ui.ModeSearch:
		return [][2]string{{enter, "keep"}, {"esc", "clear"}}, true
	case ui.ModeSelect:
		return [][2]string{{"j/k", "select"}, {"y", "copy"}, {"esc", "cancel"}}, true
	case ui.ModeCommand, ui.ModeInsert:
		return nil, true
	}
	return nil, false
}

// plainText makes text from the daemon or a program safe to draw on one
// line (viewfmt.PlainText).
func plainText(s string) string { return viewfmt.PlainText(s) }
