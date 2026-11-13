package containers

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/padmk/portside/internal/docker"
	"github.com/padmk/portside/internal/tui/keymap"
	"github.com/padmk/portside/internal/tui/theme"
	"github.com/padmk/portside/internal/tui/ui"
	"github.com/padmk/portside/internal/viewfmt"
)

const (
	margin      = 2
	wheelRows   = 3
	doubleClick = 400 * time.Millisecond
)

// Pane is the containers list in the left column: a header per Compose
// project, then its containers, and the containers outside any project
// last. Its cursor is its own; the root model makes the container under
// it the selection while the pane is focused (Cursor).
type Pane struct {
	cur       ui.Selection
	collapsed map[string]bool
	offset    int
	listH     int

	lastClick    time.Time
	lastClickRow int
}

// New returns an empty containers list.
func New() *Pane { return &Pane{collapsed: map[string]bool{}, listH: 10, lastClickRow: -1} }

func (p *Pane) Scope() keymap.Scope { return keymap.ScopeContainers }

// Title is the pane's title and count: the running containers over all
// of them, or the engine's state while it is not ready (ui.Titled).
func (p *Pane) Title(env *ui.Env) (title, count string) {
	if env.State == nil {
		return "Containers", ""
	}
	d := env.State.Docker
	if d.Status != docker.Ready {
		return "Containers", "Docker off"
	}
	n := 0
	for _, c := range d.Containers {
		if c.State == "running" {
			n++
		}
	}
	return "Containers", fmt.Sprintf("%d/%d", n, len(d.Containers))
}

// row is a project header or a container.
type row struct {
	header    bool
	project   Project
	collapsed bool
	c         docker.Container
	// indent is set under headers.
	indent bool
}

func (r row) item() ui.Selection {
	if r.header {
		return ui.ProjectItem(r.project.Name)
	}
	return Item(r.c)
}

// rows lists the projects and their containers; headers show once there
// is a project, and a collapsed project hides its containers.
func (p *Pane) rows(env *ui.Env) []row {
	projects := Projects(env.State)
	titled := len(projects) > 1 || (len(projects) == 1 && projects[0].Name != "")
	var out []row
	for _, pr := range projects {
		if titled {
			fold := p.collapsed[pr.Name]
			out = append(out, row{header: true, project: pr, collapsed: fold})
			if fold {
				continue
			}
		}
		for _, c := range pr.Containers {
			out = append(out, row{c: c, project: pr, indent: titled})
		}
	}
	return out
}

// index is the cursor's row: its item, the same container by name after
// a recreation, its project's header when folded, else 0.
func (p *Pane) index(rows []row) int {
	for i, r := range rows {
		if r.item() == p.cur {
			return i
		}
	}
	if p.cur.Kind == ui.SelectNone {
		// A new list opens on its first container.
		for i, r := range rows {
			if !r.header {
				return i
			}
		}
	}
	if p.cur.Kind == ui.SelectContainer {
		for i, r := range rows {
			if !r.header && r.c.Name == p.cur.Name {
				return i
			}
		}
		for i, r := range rows {
			if r.header && r.collapsed {
				for _, c := range r.project.Containers {
					if c.Name == p.cur.Name {
						return i
					}
				}
			}
		}
	}
	return 0
}

// Cursor is the item under the cursor, kept on a row that exists: a
// container or a project header; none while the list is empty.
func (p *Pane) Cursor(env *ui.Env) ui.Selection {
	rows := p.rows(env)
	if len(rows) == 0 {
		return ui.Selection{}
	}
	p.cur = rows[p.index(rows)].item()
	return p.cur
}

// moveTo puts the cursor on row i, clamped; a container becomes the
// selection and the target, a header the target.
func (p *Pane) moveTo(env *ui.Env, rows []row, i int) tea.Cmd {
	if len(rows) == 0 {
		return nil
	}
	i = max(0, min(i, len(rows)-1))
	p.cur = rows[i].item()
	env.Target = p.cur
	if rows[i].header || env.Selected == p.cur {
		return nil
	}
	env.Selected = p.cur
	sel := p.cur
	return func() tea.Msg { return ui.SelectMsg{Item: sel} }
}

func (p *Pane) Update(env *ui.Env, msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case ui.SelectMsg:
		if msg.Item.Kind == ui.SelectContainer {
			p.cur = msg.Item
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

func (p *Pane) click(env *ui.Env, m tea.Mouse) tea.Cmd {
	rows := p.rows(env)
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
		p.collapsed[rows[i].project.Name] = !p.collapsed[rows[i].project.Name]
		p.lastClickRow = -1
		return cmd
	}
	if double {
		p.lastClickRow = -1
		return tea.Batch(cmd, func() tea.Msg { return ui.FocusDetailMsg{} })
	}
	return cmd
}

func (p *Pane) scroll(env *ui.Env, n int) tea.Cmd {
	rows := p.rows(env)
	p.offset = max(0, min(p.offset+n, len(rows)-p.listH))
	cur := p.index(rows)
	lo, hi := p.offset, p.offset+p.listH-1
	switch {
	case cur < lo:
		return p.moveTo(env, rows, lo)
	case cur > hi:
		return p.moveTo(env, rows, hi)
	}
	return nil
}

// Do runs the list's keys; the container and project actions go to the
// root model as ui.ActMsg on the cursor's item, which runs them as a key
// on a server would.
func (p *Pane) Do(env *ui.Env, a keymap.Action) tea.Cmd {
	rows := p.rows(env)
	cur := p.index(rows)
	switch a.ID {
	case "down":
		return p.moveTo(env, rows, cur+1)
	case "up":
		return p.moveTo(env, rows, cur-1)
	case "next-page":
		return p.moveTo(env, rows, cur+max(1, p.listH-margin))
	case "prev-page":
		return p.moveTo(env, rows, cur-max(1, p.listH-margin))
	case "top":
		return p.moveTo(env, rows, 0)
	case "bottom":
		return p.moveTo(env, rows, len(rows)-1)
	case "open-detail":
		if len(rows) == 0 {
			return nil
		}
		if r := rows[cur]; r.header {
			p.collapsed[r.project.Name] = !p.collapsed[r.project.Name]
			return nil
		}
		return func() tea.Msg { return ui.FocusDetailMsg{} }
	}
	if len(rows) == 0 {
		return func() tea.Msg { return ui.NoticeMsg{Text: a.ID + ": no container here", Level: ui.LevelWarn} }
	}
	act := ui.ActMsg{ID: a.ID, Item: rows[cur].item()}
	return func() tea.Msg { return act }
}

func (p *Pane) keepVisible(cur, n int) {
	m := min(margin, max(0, (p.listH-1)/2))
	if cur < p.offset+m {
		p.offset = cur - m
	}
	if cur > p.offset+p.listH-1-m {
		p.offset = cur - p.listH + 1 + m
	}
	p.offset = max(0, min(p.offset, n-p.listH))
}

func (p *Pane) View(env *ui.Env, w, h int, focused bool) string {
	s := env.Styles
	inner := max(0, w-4)
	p.listH = max(1, h-2)
	title, count := p.Title(env)
	position := ""
	var lines []string
	fill := map[int]lipgloss.Style{}
	rows := p.rows(env)
	switch {
	case env.State == nil:
		lines = []string{s.Muted.Render("connecting" + env.Glyphs.Ellipsis)}
	case env.State.Docker.Status != docker.Ready:
		lines = EngineLines(env, inner)
	case len(rows) == 0:
		lines = []string{s.Muted.Render("No containers")}
	default:
		cur := p.index(rows)
		p.keepVisible(cur, len(rows))
		sel := s.OnSelected()
		for i := p.offset; i < len(rows) && i < p.offset+p.listH; i++ {
			if i == cur {
				fill[len(lines)] = s.Selected
				lines = append(lines, sel.Accent.Render(env.Glyphs.Cursor)+renderRow(env, sel, rows[i], inner-1))
				continue
			}
			lines = append(lines, " "+renderRow(env, s, rows[i], inner-1))
		}
		if len(rows) > p.listH {
			position = fmt.Sprintf("%d/%d", cur+1, len(rows))
		}
	}
	// The header keeps the count; the position goes to the bottom border,
	// as in the servers list.
	return ui.WithBottom(env, ui.BoxRows(env, w, h, focused, ui.PaneTitle(env, keymap.ScopeContainers, title, focused), count, lines, fill), w, focused, position)
}

// EngineLines say why there are no containers while the engine is not
// ready: connecting, starting, or not running with how to start it and
// the reason the daemon gives.
func EngineLines(env *ui.Env, w int) []string {
	s := env.Styles
	d := env.State.Docker
	wrap := func(text string, style lipgloss.Style) []string {
		var out []string
		for part := range strings.SplitSeq(ansi.Wordwrap(text, max(1, w), ""), "\n") {
			out = append(out, style.Render(part))
		}
		return out
	}
	switch {
	case d.Status == docker.Connecting:
		return wrap("Connecting to Docker"+env.Glyphs.Ellipsis, s.Muted)
	case d.Starting:
		return wrap("Starting Docker"+env.Glyphs.Ellipsis, s.Starting)
	}
	out := wrap("Docker is not running.", s.Warn)
	engine := d.Engine
	if engine == "" {
		engine = "Docker"
	}
	out = append(out, wrap("Containers show up here once the engine ("+engine+") answers. :start-docker starts it.", s.Muted)...)
	if d.Error != "" && d.Error != "Docker is not running" {
		out = append(out, wrap(d.Error, s.Error)...)
	}
	return out
}

// seg is a styled piece of a row.
type seg struct {
	text  string
	style lipgloss.Style
}

func width(segs []seg) int {
	n := 0
	for _, s := range segs {
		n += ansi.StringWidth(s.text)
	}
	return n
}

// paint joins segs cut to w cells.
func paint(env *ui.Env, segs []seg, w int) string {
	var b strings.Builder
	used := 0
	for _, s := range segs {
		if s.text == "" || used >= w {
			continue
		}
		t := s.text
		if used+ansi.StringWidth(t) > w {
			t = ansi.Truncate(t, w-used, env.Glyphs.Ellipsis)
		}
		used += ansi.StringWidth(t)
		b.WriteString(s.style.Render(t))
	}
	return b.String()
}

// line joins left and right segments in w cells, right at the right end
// when both fit, else left alone.
func line(env *ui.Env, s theme.Styles, left, right []seg, w int) string {
	// Names come from the engine: made safe before they are measured.
	for _, segs := range [][]seg{left, right} {
		for i := range segs {
			segs[i].text = viewfmt.PlainText(segs[i].text)
		}
	}
	lw, rw := width(left), width(right)
	if rw == 0 || lw+1+rw > w {
		return paint(env, left, w)
	}
	return paint(env, append(append(left, seg{strings.Repeat(" ", w-lw-rw), s.Surface}), right...), w)
}

func renderRow(env *ui.Env, s theme.Styles, r row, w int) string {
	if r.header {
		return renderHeader(env, s, r, w)
	}
	return renderContainer(env, s, r, w)
}

// renderHeader is a project's arrow, name and owner server, a spinner
// while a compose job runs in it, and its running count.
func renderHeader(env *ui.Env, s theme.Styles, r row, w int) string {
	g := env.Glyphs
	arrow := g.Expanded
	if r.collapsed {
		arrow = g.Collapsed
	}
	name, style := r.project.Name, s.Bold
	if name == "" {
		name, style = "other containers", s.Muted
	}
	left := []seg{{arrow, s.Muted}, {" ", s.Surface}, {name, style}}
	if r.project.Server != "" && r.project.Name != "" {
		left = append(left, seg{" ", s.Surface}, seg{"[" + r.project.Server + "]", s.Accent})
	}
	var right []seg
	pending := env.Pending[ui.ProjectItem(r.project.Name)]
	if pending == "" {
		for _, c := range r.project.Containers {
			if c.Job != nil && c.Job.Running {
				pending = c.Job.Kind
			}
		}
	}
	if pending != "" {
		right = append(right, seg{g.Spinner[env.Frame%len(g.Spinner)] + " " + pending, s.Starting}, seg{" ", s.Surface})
	}
	right = append(right, seg{RunningCount(r.project), s.Muted})
	return line(env, s, left, right, w)
}

// renderContainer is a container's state glyph, its name (the service in
// a project), what is wrong (health, exit code, out of memory), its
// published ports and, outside its project's header, its owner server.
func renderContainer(env *ui.Env, s theme.Styles, r row, w int) string {
	g := env.Glyphs
	c := r.c
	st := StatusOf(c)
	style, wordStyle := s.State(string(st.Display)), s.StateWord(string(st.Display))
	glyph := g.State(st.Display, env.Frame)
	if env.Pending[Item(c)] != "" || (c.Job != nil && c.Job.Running) {
		glyph, style, wordStyle = g.Spinner[env.Frame%len(g.Spinner)], s.State("starting"), s.Starting
	}
	var left []seg
	if r.indent {
		left = append(left, seg{"  ", s.Surface})
	}
	name := c.Name
	if r.indent && r.project.Name != "" {
		name = ShortName(c)
	}
	left = append(left, seg{glyph, style.Bold(true)}, seg{" ", s.Surface}, seg{name, s.Text})
	if st.Label != "running" {
		word := st.Label
		if c.Failed && c.ExitCode != nil {
			word += " (" + strconv.Itoa(*c.ExitCode) + ")"
		}
		left = append(left, seg{" ", s.Surface}, seg{word, wordStyle})
	}
	if c.OOMKilled {
		left = append(left, seg{" ", s.Surface}, seg{"OOM", s.Error})
	}
	var right []seg
	if ports := portList(c); ports != "" {
		right = append(right, seg{ports, s.Port})
	}
	if c.Server != "" && (r.project.Name == "" || c.Server != r.project.Server) {
		if len(right) > 0 {
			right = append(right, seg{" ", s.Surface})
		}
		right = append(right, seg{"[" + c.Server + "]", s.Accent})
	}
	return line(env, s, left, right, w)
}

// portList is a container's published TCP ports, ":5432 :8080".
func portList(c docker.Container) string {
	var out []string
	for _, p := range c.Ports {
		if p.Proto == "tcp" && p.Public > 0 {
			out = append(out, ":"+strconv.Itoa(p.Public))
		}
	}
	return strings.Join(out, " ")
}
