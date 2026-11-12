// Package servers is the terminal UI's servers pane: the grouped list of
// servers with their state, port, CPU and memory, the filter and the server
// actions (design/tui.md "Layout", "Keys", "Mouse").
package servers

import (
	"fmt"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/padmk/portside/internal/supervisor"
	"github.com/padmk/portside/internal/tui/keymap"
	"github.com/padmk/portside/internal/tui/ui"
	"github.com/padmk/portside/internal/viewfmt"
)

const (
	// margin is how many rows stay visible above and below the cursor.
	margin = 2
	// wheelRows is how far one wheel step scrolls.
	wheelRows = 3
	// doubleClick is the longest gap between the clicks of a double click.
	doubleClick = 400 * time.Millisecond
)

// Pane is the servers list. The cursor is on env.Selected unless it is on a
// group header, which keeps the selected server in the right pane; the
// cursor's row is env.Target. While the selection or the target belongs to
// another pane (a container, a project, a port), the list leaves them
// alone and keeps its cursor on last, the server it was last on.
type Pane struct {
	// last is the server the cursor was last on, its row while the
	// selection is another pane's.
	last ui.Selection

	// collapsed holds the collapsed groups by name; "" is the header of the
	// ungrouped servers.
	collapsed map[string]bool
	// onHeader puts the cursor on headerGroup's header row.
	onHeader    bool
	headerGroup string

	offset int // first list row on screen
	listH  int // list rows on screen in the last frame

	query     string // the filter, kept after enter
	filtering bool   // the filter is being typed
	// states keeps only servers in these display states (:filter-state),
	// stateLabel names them.
	states     []supervisor.Display
	stateLabel string

	lastClick    time.Time
	lastClickRow int
}

// New creates the servers pane.
func New() *Pane {
	return &Pane{collapsed: map[string]bool{}, listH: 10, lastClickRow: -1}
}

func (p *Pane) Scope() keymap.Scope { return keymap.ScopeServers }

// Title is the pane's title and its count: the servers, or the shown ones
// over all of them while a filter is on (ui.Titled).
func (p *Pane) Title(env *ui.Env) (title, count string) {
	if env.State == nil {
		return "Servers", ""
	}
	total := len(env.State.Servers)
	count = fmt.Sprint(total)
	if p.query != "" || len(p.states) > 0 {
		shown := 0
		for _, r := range p.rows(env, true) {
			if !r.header {
				shown++
			}
		}
		count = fmt.Sprintf("%d/%d", shown, total)
	}
	return "Servers", count
}

// row is one line of the list: a group header or a server.
type row struct {
	header bool
	// group is the header's group or the server's; "" is the ungrouped
	// servers' header.
	group string
	// servers are a header's servers, all of them whatever the filter.
	servers   []supervisor.View
	collapsed bool // a header whose servers are hidden
	view      supervisor.View
}

// rows lists what the list shows: headers only when there are groups,
// servers of collapsed groups hidden unless all is set or a filter is on.
func (p *Pane) rows(env *ui.Env, all bool) []row {
	if env.State == nil {
		return nil
	}
	groups := viewfmt.Groups(env.State)
	grouped := false
	for _, g := range groups {
		if g.Name != "" {
			grouped = true
		}
	}
	terms := strings.Fields(strings.ToLower(p.query))
	filtered := len(terms) > 0 || len(p.states) > 0
	var out []row
	for _, g := range groups {
		var kept []supervisor.View
		for _, v := range g.Servers {
			if matches(v, terms) && inStates(v, p.states) {
				kept = append(kept, v)
			}
		}
		if len(kept) == 0 {
			continue
		}
		if grouped {
			hide := p.collapsed[g.Name] && !all && !filtered
			out = append(out, row{header: true, group: g.Name, servers: g.Servers, collapsed: hide})
			if hide {
				continue
			}
		}
		for _, v := range kept {
			out = append(out, row{group: g.Name, view: v})
		}
	}
	return out
}

// mine reports whether an item is the servers list's to move: a server, a
// group header or nothing.
func mine(it ui.Selection) bool {
	switch it.Kind {
	case ui.SelectNone, ui.SelectServer, ui.SelectGroup:
		return true
	}
	return false
}

// owns reports whether the list may move env.Selected and env.Target:
// neither belongs to another pane.
func owns(env *ui.Env) bool { return mine(env.Selected) && mine(env.Target) }

// current is the server the cursor is on as the list sees it: the
// selection while it is a server, else the server the cursor was last on.
func (p *Pane) current(env *ui.Env) ui.Selection {
	if env.Selected.Kind == ui.SelectServer {
		p.last = env.Selected
	}
	return p.last
}

// index is the cursor's row: the header it is on, else the selected
// server, else the header of the collapsed group holding it.
func (p *Pane) index(env *ui.Env, rows []row) int {
	if p.onHeader {
		for i, r := range rows {
			if r.header && r.group == p.headerGroup {
				return i
			}
		}
	}
	if cur := p.current(env); cur.Kind == ui.SelectServer {
		group, found := "", false
		for i, r := range rows {
			if !r.header && r.view.Name == cur.Name {
				return i
			}
			if r.header {
				for _, v := range r.servers {
					if v.Name == cur.Name {
						group, found = r.group, true
					}
				}
			}
		}
		if found {
			for i, r := range rows {
				if r.header && r.group == group {
					return i
				}
			}
		}
	}
	return 0
}

// moveTo puts the cursor on row i, clamped, and makes it the target; a
// server row becomes the selection too. While the selection or the target
// is another pane's, only the cursor moves.
func (p *Pane) moveTo(env *ui.Env, rows []row, i int) tea.Cmd {
	if len(rows) == 0 {
		return nil
	}
	i = max(0, min(i, len(rows)-1))
	r := rows[i]
	own := owns(env)
	if r.header {
		p.onHeader, p.headerGroup = true, r.group
		if own {
			env.Target = ui.GroupItem(r.group)
		}
		return nil
	}
	p.onHeader = false
	sel := ui.ServerItem(r.view.Name)
	if !own {
		p.last = sel
		return nil
	}
	p.last = sel
	env.Target = sel
	if env.Selected == sel {
		return nil
	}
	env.Selected = sel
	return func() tea.Msg { return ui.SelectMsg{Item: sel} }
}

// onRow reports whether the cursor is exactly on row r.
func (p *Pane) onRow(env *ui.Env, r row) bool {
	if r.header {
		return p.onHeader && p.headerGroup == r.group
	}
	return !p.onHeader && p.current(env) == ui.ServerItem(r.view.Name)
}

// Cursor is the row the cursor is drawn on: a group header, a server, or
// nothing when the list shows no rows (a filter that matches nothing).
// The root model makes it the target whenever the list owns the input, so
// an action never reaches a row the list does not show.
func (p *Pane) Cursor(env *ui.Env) ui.Selection {
	rows := p.rows(env, false)
	if len(rows) == 0 {
		return ui.Selection{}
	}
	r := rows[p.index(env, rows)]
	if r.header {
		return ui.GroupItem(r.group)
	}
	return ui.ServerItem(r.view.Name)
}

// keepVisibleTarget puts the cursor on a row the list shows after the
// filter, a fold or a new snapshot changed it: the header of the collapsed
// group holding the selected server, else the first server shown, else the
// first row.
func (p *Pane) keepVisibleTarget(env *ui.Env) tea.Cmd {
	rows := p.rows(env, false)
	if len(rows) == 0 {
		if owns(env) {
			env.Target = ui.Selection{}
		}
		return nil
	}
	i := p.index(env, rows)
	if p.onRow(env, rows[i]) {
		target := ui.ServerItem(rows[i].view.Name)
		if rows[i].header {
			target = ui.GroupItem(rows[i].group)
		}
		if owns(env) {
			env.Target = target
		}
		return nil
	}
	r := rows[i]
	if keep := (!r.header && p.current(env) == ui.ServerItem(r.view.Name)) || (r.header && r.collapsed && p.holdsSelected(env, r)); !keep {
		i = max(0, slices.IndexFunc(rows, func(r row) bool { return !r.header }))
	}
	return p.moveTo(env, rows, i)
}

// holdsSelected reports whether header row r is the group of the selected
// server.
func (p *Pane) holdsSelected(env *ui.Env, r row) bool {
	cur := p.current(env)
	return slices.ContainsFunc(r.servers, func(v supervisor.View) bool { return cur == ui.ServerItem(v.Name) })
}

// selectedView is the server on the cursor's visible row; false on a
// header, in a collapsed group or when the filter hides it, so an action
// never reaches a server the list does not show.
func (p *Pane) selectedView(env *ui.Env) (supervisor.View, bool) {
	if p.onHeader || env.Selected.Kind != ui.SelectServer {
		return supervisor.View{}, false
	}
	rows := p.rows(env, false)
	i := p.index(env, rows)
	if i >= len(rows) || rows[i].header || rows[i].view.Name != env.Selected.Name {
		return supervisor.View{}, false
	}
	return rows[i].view, true
}

func (p *Pane) Update(env *ui.Env, msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case ui.SelectMsg:
		if msg.Item.Kind == ui.SelectServer {
			p.onHeader = false
		}
		return p.keepVisibleTarget(env)
	case ui.StateMsg:
		return p.keepVisibleTarget(env)
	case ui.StateFilterMsg:
		p.states, p.stateLabel = msg.States, msg.Label
		return p.keepVisibleTarget(env)
	case tea.KeyPressMsg:
		if p.filtering {
			return p.filterKey(env, msg)
		}
	case tea.MouseClickMsg:
		return p.click(env, tea.Mouse(msg))
	case tea.MouseWheelMsg:
		switch msg.Button {
		case tea.MouseWheelUp:
			return p.scroll(env, -wheelRows)
		case tea.MouseWheelDown:
			return p.scroll(env, wheelRows)
		}
	}
	return nil
}

// click selects the row under the pointer; a left click on a header
// toggles the group, and a second left click on a server row soon after
// opens the detail. A right click only selects, for the menu it opens.
func (p *Pane) click(env *ui.Env, m tea.Mouse) tea.Cmd {
	rows := p.rows(env, false)
	i := p.offset + m.Y - 1
	if m.Y < 1 || m.Y > p.listH || i >= len(rows) {
		return nil
	}
	if m.Button == tea.MouseRight {
		return p.moveTo(env, rows, i)
	}
	if m.Button != tea.MouseLeft {
		return nil
	}
	now := env.Now()
	double := i == p.lastClickRow && now.Sub(p.lastClick) <= doubleClick
	p.lastClick, p.lastClickRow = now, i
	cmd := p.moveTo(env, rows, i)
	if rows[i].header {
		p.collapsed[rows[i].group] = !p.collapsed[rows[i].group]
		p.lastClickRow = -1
		return tea.Batch(cmd, p.keepVisibleTarget(env))
	}
	if double {
		p.lastClickRow = -1
		return tea.Batch(cmd, p.openDetail(env))
	}
	return cmd
}

// scroll moves the view by n rows and brings the cursor along when it
// would leave it.
func (p *Pane) scroll(env *ui.Env, n int) tea.Cmd {
	rows := p.rows(env, false)
	p.offset = max(0, min(p.offset+n, len(rows)-p.listH))
	lo, hi := p.offset+margin, p.offset+p.listH-1-margin
	if p.offset == 0 {
		lo = 0
	}
	if p.offset+p.listH >= len(rows) {
		hi = len(rows) - 1
	}
	cur := p.index(env, rows)
	switch {
	case cur < lo:
		return p.moveTo(env, rows, lo)
	case cur > hi:
		return p.moveTo(env, rows, hi)
	}
	return nil
}

// openDetail is enter and a double click on a server: the root model moves
// the focus to the detail pane.
func (p *Pane) openDetail(env *ui.Env) tea.Cmd {
	if _, ok := p.selectedView(env); !ok {
		return nil
	}
	return func() tea.Msg { return ui.FocusDetailMsg{} }
}

// keepVisible scrolls so that the cursor stays margin rows from the edges.
func (p *Pane) keepVisible(cur, n, h int) {
	m := min(margin, max(0, (h-1)/2))
	if cur < p.offset+m {
		p.offset = cur - m
	}
	if cur > p.offset+h-1-m {
		p.offset = cur - h + 1 + m
	}
	p.offset = max(0, min(p.offset, n-h))
}

func (p *Pane) View(env *ui.Env, w, h int, focused bool) string {
	inner, listH := w-2, h-2
	cw := inner - 2 // one cell of padding each side; the left one holds the cursor
	if p.filtering {
		listH--
	}
	p.listH = max(1, listH)
	rows := p.rows(env, false)
	_, right := p.Title(env)
	s := env.Styles
	// body is one content line across the pane's inner width, padding and
	// background included.
	var body []string
	text := func(lines []string) {
		for _, l := range lines {
			body = append(body, s.Row(" "+l, inner))
		}
	}
	position := ""
	switch {
	case env.State == nil:
		text(centered(env, listH, cw, seg{"connecting" + env.Glyphs.Ellipsis, s.Muted}))
	case len(env.State.Servers) == 0:
		text(centered(env, listH, cw,
			seg{"No servers yet", s.Text},
			seg{"n new server" + env.Glyphs.Separator + ":scan proposals" + env.Glyphs.Separator + ":config", s.Muted}))
	case len(rows) == 0:
		text(centered(env, listH, cw, seg{"no matches", s.Muted}))
	default:
		cur := p.index(env, rows)
		p.keepVisible(cur, len(rows), p.listH)
		cols := layout(env, rows, cw)
		sel := s.OnSelected()
		for i := p.offset; i < len(rows) && i < p.offset+p.listH; i++ {
			if i == cur {
				line := sel.Accent.Render(env.Glyphs.Cursor) + renderRow(env, sel, rows[i], cols, cw)
				body = append(body, s.SelectedRow(line, inner))
				continue
			}
			body = append(body, s.Row(" "+renderRow(env, s, rows[i], cols, cw), inner))
		}
		if len(rows) > p.listH {
			position = fmt.Sprintf("%d/%d", cur+1, len(rows))
		}
	}
	if p.filtering {
		for len(body) < listH {
			body = append(body, s.Row("", inner))
		}
		body = append(body[:max(0, listH)], s.Row(" "+p.filterInput(env, cw), inner))
	}
	box := ui.BoxRows(env, w, h, focused, "Servers", right, nil, nil)
	lines := strings.Split(box, "\n")
	if w < 4 || len(lines) < 3 {
		return box
	}
	for i := 1; i < len(lines)-1; i++ {
		b := s.Row("", inner)
		if i-1 < len(body) {
			b = body[i-1]
		}
		lines[i] = ansi.Cut(lines[i], 0, 1) + b + ansi.Cut(lines[i], w-1, w)
	}
	label := p.filterLabel(env)
	if position != "" {
		if label != "" {
			label += env.Glyphs.Separator
		}
		label += position
	}
	if label != "" {
		lines[len(lines)-1] = bottomLabel(env, lines[len(lines)-1], w, label)
	}
	return strings.Join(lines, "\n")
}

// filterInput is the filter being typed: "/", the query and the caret,
// showing the query's end when it is wider than w.
func (p *Pane) filterInput(env *ui.Env, w int) string {
	s := env.Styles
	q := viewfmt.PlainText(p.query)
	room := max(0, w-1-ansi.StringWidth(env.Glyphs.Caret))
	if n := ansi.StringWidth(q); n > room {
		q = ansi.TruncateLeft(q, n-room+ansi.StringWidth(env.Glyphs.Ellipsis), env.Glyphs.Ellipsis)
	}
	return s.Accent.Render("/") + s.Text.Render(q) + s.Accent.Render(env.Glyphs.Caret)
}

// filterLabel names the filters on for the bottom border: the state, then
// the kept text filter.
func (p *Pane) filterLabel(env *ui.Env) string {
	var parts []string
	if p.stateLabel != "" {
		parts = append(parts, "state: "+p.stateLabel)
	}
	if p.query != "" && !p.filtering {
		parts = append(parts, "filter: "+viewfmt.PlainText(p.query))
	}
	return strings.Join(parts, env.Glyphs.Separator)
}

// centered places lines, wrapped to w columns, in the middle of an area of
// h rows.
func centered(env *ui.Env, h, w int, lines ...seg) []string {
	var wrapped []seg
	for _, l := range lines {
		for part := range strings.SplitSeq(ansi.Wordwrap(l.text, w, ""), "\n") {
			wrapped = append(wrapped, seg{part, l.style})
		}
	}
	out := make([]string, max(0, (h-len(wrapped))/2))
	for _, l := range wrapped {
		t := ansi.Truncate(l.text, w, env.Glyphs.Ellipsis)
		out = append(out, strings.Repeat(" ", max(0, (w-ansi.StringWidth(t))/2))+l.style.Render(t))
	}
	return out
}

// bottomLabel redraws a box's bottom border, w cells, with label near its
// right end; the corners and the rule come from the border as drawn.
func bottomLabel(env *ui.Env, border string, w int, label string) string {
	if w < 8 {
		return border
	}
	label = ansi.Truncate(label, w-6, env.Glyphs.Ellipsis)
	tail := " " + env.Styles.Accent.Render(label) + " "
	fill := w - 3 - ansi.StringWidth(tail)
	if fill < 0 {
		return border
	}
	return ansi.Cut(border, 0, 1+fill) + tail + ansi.Cut(border, w-2, w)
}
