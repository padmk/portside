package ports

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/padmk/portside/internal/client"
	"github.com/padmk/portside/internal/tui/keymap"
	"github.com/padmk/portside/internal/tui/theme"
	"github.com/padmk/portside/internal/tui/ui"
	"github.com/padmk/portside/internal/viewfmt"
)

const (
	// refresh is how often the open pane lists the ports again: the list
	// is not in the daemon's snapshot.
	refresh   = 3 * time.Second
	margin    = 2
	wheelRows = 3
)

// listMsg carries the listeners of fetch gen.
type listMsg struct {
	gen  int
	list []Listener
	err  error
}

// tickMsg asks for the next listing of the polling started with gen.
type tickMsg struct{ gen int }

// Pane is the ports list: one row per listening process and port, sorted
// by port, with a filter. The root model opens it on demand (Open) and
// closes it (Close); while open it lists the ports every few seconds.
type Pane struct {
	open    bool
	gen     int
	list    []Listener
	loaded  bool
	err     string
	cur     ui.Selection
	offset  int
	listH   int
	query   string
	editing bool
	// pending is a `:kill` argument waiting for the first list.
	pending string
	// focused is whether the last frame drew the pane focused: a list
	// that arrives then gives the cursor's row the target.
	focused bool
}

// New returns a closed ports pane.
func New() *Pane { return &Pane{listH: 10} }

func (p *Pane) Scope() keymap.Scope { return keymap.ScopePorts }

// Open starts listing the ports.
func (p *Pane) Open(env *ui.Env) tea.Cmd {
	p.open = true
	p.gen++
	return fetch(env.Client, p.gen)
}

// Close stops listing them; the last list stays for the next Open.
func (p *Pane) Close(env *ui.Env) {
	p.open = false
	p.gen++
	if p.editing {
		p.editing = false
		env.EnterMode(ui.ModeNormal)
	}
}

func fetch(c *client.Client, gen int) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		var body struct {
			Listeners []Listener `json:"listeners"`
		}
		err := c.Do(ctx, http.MethodGet, "/api/listeners", nil, &body)
		return listMsg{gen: gen, list: body.Listeners, err: err}
	}
}

// Title is the pane's title and count (ui.Titled).
func (p *Pane) Title(env *ui.Env) (title, count string) {
	if !p.loaded {
		return "Ports", ""
	}
	rows := p.rows()
	if p.query != "" {
		return "Ports", fmt.Sprintf("%d/%d", len(rows), len(p.list))
	}
	return "Ports", strconv.Itoa(len(p.list))
}

// rows are the listeners the filter keeps, by port, then PID.
func (p *Pane) rows() []Listener {
	terms := strings.Fields(strings.ToLower(p.query))
	var out []Listener
	for _, l := range p.list {
		hay := strings.ToLower(strings.Join([]string{":" + strconv.Itoa(l.Port), l.Command, strconv.Itoa(l.PID), Describe(l), Reach(l), l.CwdDisplay}, "\x00"))
		if !slices.ContainsFunc(terms, func(t string) bool { return !strings.Contains(hay, t) }) {
			out = append(out, l)
		}
	}
	slices.SortStableFunc(out, func(a, b Listener) int {
		if a.Port != b.Port {
			return a.Port - b.Port
		}
		return a.PID - b.PID
	})
	return out
}

func (p *Pane) index(rows []Listener) int {
	for i, l := range rows {
		if Item(l) == p.cur {
			return i
		}
	}
	for i, l := range rows {
		if l.Port >= p.cur.Port {
			return i
		}
	}
	return max(0, len(rows)-1)
}

// Cursor is the listener under the cursor, false while there is none.
func (p *Pane) Cursor() (Listener, bool) {
	rows := p.rows()
	if len(rows) == 0 {
		return Listener{}, false
	}
	l := rows[p.index(rows)]
	p.cur = Item(l)
	return l, true
}

// Listeners is the last list by port, without the filter; nil while
// the pane is closed.
func (p *Pane) Listeners() []Listener {
	if !p.open {
		return nil
	}
	out := slices.Clone(p.list)
	slices.SortStableFunc(out, func(a, b Listener) int {
		if a.Port != b.Port {
			return a.Port - b.Port
		}
		return a.PID - b.PID
	})
	return out
}

// Find is the listener an item names in the last list.
func (p *Pane) Find(it ui.Selection) (Listener, bool) {
	for _, l := range p.list {
		if Item(l) == it {
			return l, true
		}
	}
	return Listener{}, false
}

// Refresh lists the ports again now (after a kill or a takeover).
func (p *Pane) Refresh(env *ui.Env) tea.Cmd {
	if !p.open {
		return nil
	}
	p.gen++
	return fetch(env.Client, p.gen)
}

func (p *Pane) moveTo(env *ui.Env, rows []Listener, i int) {
	if len(rows) == 0 {
		return
	}
	i = max(0, min(i, len(rows)-1))
	p.cur = Item(rows[i])
	env.Target = p.cur
}

func (p *Pane) Update(env *ui.Env, msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case listMsg:
		if msg.gen != p.gen || !p.open {
			return nil
		}
		if msg.err != nil {
			p.err = msg.err.Error()
		} else {
			p.list, p.loaded, p.err = msg.list, true, ""
			// The cursor stays on its listener, or the row that took its
			// place when it went; the target follows while it is this
			// pane's, or the pane is focused (the first list after :ports).
			if rows := p.rows(); len(rows) > 0 && (env.Target.Kind == ui.SelectPort || p.focused) {
				p.moveTo(env, rows, p.index(rows))
			}
		}
		gen := p.gen
		tick := tea.Tick(refresh, func(time.Time) tea.Msg { return tickMsg{gen: gen} })
		if arg := p.pending; arg != "" {
			p.pending = ""
			if msg.err != nil {
				return tea.Batch(notice("kill "+arg+": "+msg.err.Error()), tick)
			}
			return tea.Batch(p.resolveKill(arg), tick)
		}
		return tick
	case tickMsg:
		if msg.gen != p.gen || !p.open {
			return nil
		}
		return fetch(env.Client, p.gen)
	case ui.OpDoneMsg:
		for _, it := range msg.Items {
			if it.Kind == ui.SelectPort || it.Kind == ui.SelectServer {
				return p.Refresh(env)
			}
		}
	case tea.KeyPressMsg:
		if p.editing {
			return p.filterKey(env, msg)
		}
	case tea.MouseClickMsg:
		rows := p.rows()
		if i := p.offset + msg.Y - 1; msg.Y >= 1 && msg.Y <= p.listH && i < len(rows) {
			p.moveTo(env, rows, i)
		}
	case tea.MouseWheelMsg:
		rows := p.rows()
		n := wheelRows
		if msg.Button == tea.MouseWheelUp {
			n = -n
		}
		p.moveTo(env, rows, p.index(rows)+n)
	}
	return nil
}

// filterKey edits the filter: enter keeps it, esc clears it.
func (p *Pane) filterKey(env *ui.Env, msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		p.editing, p.query = false, ""
		env.EnterMode(ui.ModeNormal)
	case "enter":
		p.editing = false
		p.query = strings.TrimSpace(p.query)
		env.EnterMode(ui.ModeNormal)
	case "up", "down":
		rows := p.rows()
		n := 1
		if msg.String() == "up" {
			n = -1
		}
		p.moveTo(env, rows, p.index(rows)+n)
	case "backspace":
		if r := []rune(p.query); len(r) > 0 {
			p.query = string(r[:len(r)-1])
		}
	case "ctrl+u":
		p.query = ""
	case "ctrl+w":
		q := strings.TrimRightFunc(p.query, unicode.IsSpace)
		p.query = q[:strings.LastIndexFunc(q, unicode.IsSpace)+1]
	default:
		if msg.Text != "" {
			p.query += viewfmt.PlainText(msg.Text)
		}
	}
	p.moveTo(env, p.rows(), p.index(p.rows()))
	return nil
}

// Kill is `:kill <port or pid>`: it asks the root model to kill the
// process listening on that port, else the listening process with that
// PID, after the list it needs; the pane opens for it when closed.
func (p *Pane) Kill(env *ui.Env, arg string) tea.Cmd {
	if p.open && p.loaded {
		return p.resolveKill(arg)
	}
	p.pending = arg
	if p.open {
		return nil
	}
	return p.Open(env)
}

// resolveKill finds the listener `:kill` names: a port with one process,
// else a PID; a port several processes listen on asks for the PID.
func (p *Pane) resolveKill(arg string) tea.Cmd {
	n, err := strconv.Atoi(strings.TrimPrefix(arg, ":"))
	if err != nil || n <= 0 {
		return notice(fmt.Sprintf("kill: %q is not a port or a PID", arg))
	}
	var onPort, ofPID []Listener
	for _, l := range p.Listeners() {
		if l.Port == n && !slices.ContainsFunc(onPort, func(o Listener) bool { return o.PID == l.PID }) {
			onPort = append(onPort, l)
		}
		if l.PID == n {
			ofPID = append(ofPID, l)
		}
	}
	var l Listener
	switch {
	case len(onPort) == 1:
		l = onPort[0]
	case len(onPort) > 1:
		pids := make([]string, len(onPort))
		for i, o := range onPort {
			pids[i] = fmt.Sprintf("%s %d", o.Command, o.PID)
		}
		return notice(fmt.Sprintf("kill %d: several processes listen on port %d (%s); :kill <pid> picks one", n, n, strings.Join(pids, ", ")))
	case len(ofPID) > 0:
		l = ofPID[0]
	default:
		return notice(fmt.Sprintf("kill %d: nothing listens on port %d, and no listening process has PID %d", n, n, n))
	}
	act := ui.ActMsg{ID: "kill", Item: Item(l)}
	return func() tea.Msg { return act }
}

// notice is an error notice.
func notice(text string) tea.Cmd {
	return func() tea.Msg { return ui.NoticeMsg{Text: text, Level: ui.LevelError} }
}

// Do runs the list's keys; kill, takeover, open and copy-url go to the
// root model as ui.ActMsg on the cursor's listener, and esc clears the
// filter, then closes the pane.
func (p *Pane) Do(env *ui.Env, a keymap.Action) tea.Cmd {
	rows := p.rows()
	cur := p.index(rows)
	switch a.ID {
	case "down":
		p.moveTo(env, rows, cur+1)
	case "up":
		p.moveTo(env, rows, cur-1)
	case "next-page":
		p.moveTo(env, rows, cur+max(1, p.listH-margin))
	case "prev-page":
		p.moveTo(env, rows, cur-max(1, p.listH-margin))
	case "top":
		p.moveTo(env, rows, 0)
	case "bottom":
		p.moveTo(env, rows, len(rows)-1)
	case "filter":
		p.editing = true
		env.EnterMode(ui.ModeFilter)
	case "back":
		if p.query != "" {
			p.query = ""
			return nil
		}
		return func() tea.Msg { return ui.HidePaneMsg{Scope: keymap.ScopePorts} }
	default:
		if len(rows) == 0 {
			return func() tea.Msg { return ui.NoticeMsg{Text: a.ID + ": no port here", Level: ui.LevelWarn} }
		}
		act := ui.ActMsg{ID: a.ID, Item: Item(rows[cur])}
		return func() tea.Msg { return act }
	}
	return nil
}

func (p *Pane) View(env *ui.Env, w, h int, focused bool) string {
	p.focused = focused
	s := env.Styles
	inner := max(0, w-4)
	p.listH = max(1, h-2)
	if p.editing {
		p.listH = max(1, p.listH-1)
	}
	title, count := p.Title(env)
	position := ""
	rows := p.rows()
	fill := map[int]lipgloss.Style{}
	var lines []string
	switch {
	case !p.loaded && p.err == "":
		lines = []string{s.Muted.Render("Listing the ports" + env.Glyphs.Ellipsis)}
	case !p.loaded:
		lines = []string{s.Error.Render(ansi.Truncate(p.err, inner, env.Glyphs.Ellipsis))}
	case len(rows) == 0 && p.query != "":
		lines = []string{s.Muted.Render("No port matches " + p.query)}
	case len(rows) == 0:
		lines = []string{s.Muted.Render("Nothing is listening.")}
	default:
		cur := p.index(rows)
		p.keepVisible(cur, len(rows))
		pw := 0
		for _, l := range rows {
			pw = max(pw, len(strconv.Itoa(l.Port))+1)
		}
		sel := s.OnRaised()
		for i := p.offset; i < len(rows) && i < p.offset+p.listH; i++ {
			if i == cur {
				fill[len(lines)] = s.Selected
				lines = append(lines, sel.Accent.Render(env.Glyphs.Cursor)+renderRow(env, sel, rows[i], pw, inner-1))
				continue
			}
			lines = append(lines, " "+renderRow(env, s, rows[i], pw, inner-1))
		}
		if len(rows) > p.listH {
			position = fmt.Sprintf("%d/%d", cur+1, len(rows))
		}
	}
	// The header keeps the count; the refresh state, the filter and the
	// position go to the bottom border, as in the servers list.
	var status []string
	if p.err != "" && p.loaded {
		status = append(status, "refresh failed")
	}
	if p.editing {
		for len(lines) < p.listH {
			lines = append(lines, "")
		}
		lines = append(lines[:p.listH], s.Accent.Render("/")+s.Text.Render(p.query)+s.Accent.Render(env.Glyphs.Caret))
	} else if p.query != "" {
		status = append(status, "filter: "+p.query)
	}
	if position != "" {
		status = append(status, position)
	}
	box := ui.BoxRows(env, w, h, focused, title, count, lines, fill)
	return ui.WithBottom(env, box, w, focused, strings.Join(status, env.Glyphs.Separator))
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

// renderRow is a listener's port, its process and PID, what it is and
// where it is reachable from, in w cells. A narrow row drops the reach,
// then the PID, then cuts the process name; the port and what it is stay.
func renderRow(env *ui.Env, s theme.Styles, l Listener, pw, w int) string {
	roleStyle := s.Text2
	switch RoleOf(l) {
	case RoleManaged:
		roleStyle = s.Running
	case RoleContainer:
		roleStyle = s.Accent
	case RoleBlocks:
		roleStyle = s.Warn
	}
	reach := Reach(l)
	reachStyle := s.Muted
	if reach != "local" {
		reachStyle = s.Warn
	}
	port := s.Port.Render(fmt.Sprintf("%-*s", pw, ":"+strconv.Itoa(l.Port))) + s.Surface.Render(" ")
	cmd := viewfmt.PlainText(l.Command)
	pid := strconv.Itoa(l.PID)
	role := roleStyle.Render(Describe(l))
	sp := s.Surface.Render(" ")
	for _, v := range []struct {
		pid, reach bool
	}{{true, true}, {true, false}, {false, false}} {
		right := role
		if v.reach && reach != "" {
			right += sp + reachStyle.Render(reach)
		}
		left := port + s.Text.Render(cmd)
		if v.pid {
			left += sp + s.Muted.Render(pid)
		}
		lw, rw := ansi.StringWidth(left), ansi.StringWidth(right)
		if lw+1+rw <= w {
			return left + s.Surface.Render(strings.Repeat(" ", w-lw-rw)) + right
		}
	}
	room := w - ansi.StringWidth(port) - 1 - ansi.StringWidth(role)
	if room < 3 {
		return ansi.Truncate(port+role, w, env.Glyphs.Ellipsis)
	}
	return port + s.Text.Render(ansi.Truncate(cmd, room, env.Glyphs.Ellipsis)) + sp + role
}
