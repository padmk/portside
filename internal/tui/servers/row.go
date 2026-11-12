package servers

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/padmk/portside/internal/supervisor"
	"github.com/padmk/portside/internal/tui/theme"
	"github.com/padmk/portside/internal/tui/ui"
	"github.com/padmk/portside/internal/viewfmt"
)

const (
	// sparkWidth is the sparkline's width in cells.
	sparkWidth = 4
	// minName is the name width the list keeps before it adds the optional
	// columns; shorter names keep their own width.
	minName = 10
	// roomyName is how wide a name grows before the sparkline and the
	// badges get room; past it, a name grows only into what is left.
	roomyName = 20
)

// columns are the widths of a frame's server row columns, the same for
// every row, so ports, memory and badges line up across groups; 0 drops a
// column.
type columns struct {
	indent int  // under group headers
	lamp   int  // the widest state glyph
	mark   bool // a cell after the lamp for the Disabled or Stale mark
	icon   int  // the tool icon and its space
	name   int
	port   int // ":" and the widest port
	spark  int
	mem    int
	badges int // the widest row's badges
	// tail is the widest row's content after the name (port, sparkline
	// and memory, or the last exit): the badges start after it, in one
	// column, instead of at the far edge of a wide pane.
	tail int
}

// running reports whether the row shows CPU and memory instead of its last
// exit.
func running(v supervisor.View) bool {
	d := v.Display()
	return d == supervisor.Display(supervisor.Running) || d == supervisor.DisplayUnready
}

// lampGlyph is the row's state glyph: the spinner while an operation the
// UI started is pending, else the display state's glyph.
func lampGlyph(env *ui.Env, v supervisor.View) (string, bool) {
	if env.Pending[ui.ServerItem(v.Name)] != "" {
		return env.Glyphs.Spinner[env.Frame%len(env.Glyphs.Spinner)], true
	}
	return env.Glyphs.State(v.Display(), env.Frame), false
}

// markGlyph is what stands next to the lamp whatever the width: disabled,
// else a live process on an edited config; "" for neither.
func markGlyph(env *ui.Env, s theme.Styles, v supervisor.View) seg {
	switch {
	case !v.Enabled:
		return seg{env.Glyphs.Disabled, s.Muted}
	case v.Stale && v.HasProcess():
		return seg{env.Glyphs.Stale, s.Warn}
	}
	return seg{}
}

// layout sizes the columns for the rows at width w. The lamp, the state
// mark, minName cells of name and the port always stay; memory, the rest of
// a name up to roomyName, the sparkline, the badges and then the rest of a
// long name get what is left, in that order.
func layout(env *ui.Env, rows []row, w int) columns {
	var c columns
	nameW, badgeW := 0, 0
	for _, r := range rows {
		if r.header {
			c.indent = 2
			continue
		}
		v := r.view
		lamp, _ := lampGlyph(env, v)
		c.lamp = max(c.lamp, ansi.StringWidth(lamp))
		if markGlyph(env, env.Styles, v).text != "" {
			c.mark = true
		}
		nameW = max(nameW, ansi.StringWidth(viewfmt.PlainText(v.Name)))
		if v.Port > 0 {
			c.port = max(c.port, len(strconv.Itoa(v.Port))+1)
		}
		if running(v) {
			c.spark = sparkWidth
			c.mem = max(c.mem, len(viewfmt.HumanBytes(v.Mem)))
		}
		if len(env.Glyphs.Kind) > 0 {
			c.icon = 2
		}
		badgeW = max(badgeW, segsWidth(badgeSegs(env, env.Styles, v)))
	}
	gap := func(n int) int {
		if n > 0 {
			return n + 1
		}
		return 0
	}
	used := c.indent + c.lamp + 1 + c.icon
	if c.mark {
		used++
	}
	c.name = min(nameW, minName)
	if used+c.name+gap(c.port) > w {
		c.name = w - used - gap(c.port)
		if c.name < 3 { // too narrow for a port: the name comes first
			c.port, c.name = 0, w-used
		}
		c.name = max(1, c.name)
	}
	used += c.name + gap(c.port)
	grow := func(limit int) {
		n := max(0, min(limit-c.name, nameW-c.name, w-used))
		c.name += n
		used += n
	}
	fits := func(n *int) {
		if *n > 0 && used+gap(*n) <= w {
			used += gap(*n)
		} else {
			*n = 0
		}
	}
	fits(&c.mem)
	grow(roomyName)
	fits(&c.spark)
	fits(&badgeW)
	c.badges = badgeW
	grow(nameW)
	for _, r := range rows {
		if !r.header {
			c.tail = max(c.tail, tailWidth(env, r.view, c))
		}
	}
	return c
}

// tailWidth is how wide row v's content after the name is, uncut, in
// columns c (renderServer draws it).
func tailWidth(env *ui.Env, v supervisor.View, c columns) int {
	n := 0
	if c.port > 0 && (v.Port > 0 || running(v)) {
		n += 1 + c.port
	}
	if running(v) {
		if c.spark > 0 {
			n += 1 + c.spark
		}
		if c.mem > 0 {
			n += 1 + c.mem
		}
	} else if long, _, _ := lastExit(env, env.Styles, v); long != "" {
		n += 1 + ansi.StringWidth(long)
	}
	return n
}

// seg is a piece of a row and its style.
type seg struct {
	text  string
	style lipgloss.Style
}

func segsWidth(segs []seg) int {
	n := 0
	for _, s := range segs {
		n += ansi.StringWidth(s.text)
	}
	return n
}

// paint joins segs, cut to w cells with the ellipsis.
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

// pad is n spaces on the row's background.
func pad(s theme.Styles, n int) seg { return seg{strings.Repeat(" ", max(0, n)), s.Surface} }

// renderRow draws row r in w cells with styles s (OnSelected for the
// selected row); the caller fills the background.
func renderRow(env *ui.Env, s theme.Styles, r row, c columns, w int) string {
	if r.header {
		return renderHeader(env, s, r, w)
	}
	return renderServer(env, s, r.view, c, w)
}

// groupCount is a header's summary: the crashed members first, so a folded
// group still shows a crash, then running/services for the services and
// done/tasks for the tasks (a finished task is not a missing service).
// The long form names the services' count ("1/3 up"), the short one
// leaves the word out.
func groupCount(env *ui.Env, s theme.Styles, servers []supervisor.View, long bool) []seg {
	g := env.Glyphs
	services, up, tasks, done, crashed := 0, 0, 0, 0, 0
	for _, v := range servers {
		if v.Oneshot {
			tasks++
			if v.Done {
				done++
			}
		} else {
			services++
			if v.Status == supervisor.Running {
				up++
			}
		}
		if d := v.Display(); d == supervisor.Display(supervisor.Crashed) || d == supervisor.Display(supervisor.Blocked) {
			crashed++
		}
	}
	var out []seg
	add := func(text string, style lipgloss.Style) {
		if len(out) > 0 {
			out = append(out, seg{g.Separator, s.Muted})
		}
		out = append(out, seg{text, style})
	}
	if crashed > 0 {
		add(fmt.Sprintf("%s %d", g.Crashed, crashed), s.Crashed)
	}
	if services > 0 {
		text := fmt.Sprintf("%d/%d", up, services)
		if long {
			text += " up"
		}
		add(text, s.Text2)
	}
	if tasks > 0 {
		add(fmt.Sprintf("%s %d/%d", g.Done, done, tasks), s.Text2)
	}
	return out
}

// renderHeader is a group's arrow and name, its directory when all of its
// servers share one and there is room, and its count at the right, in
// its long form when that fits beside the name. The
// ungrouped servers' header is a muted "other" without a directory.
func renderHeader(env *ui.Env, s theme.Styles, r row, w int) string {
	g := env.Glyphs
	arrow := g.Expanded
	if r.collapsed {
		arrow = g.Collapsed
	}
	name, nameStyle := viewfmt.PlainText(r.group), s.Bold
	dir := r.servers[0].DirDisplay
	for _, v := range r.servers {
		if v.DirDisplay != dir {
			dir = ""
		}
	}
	if r.group == "" {
		name, nameStyle, dir = "other", s.Muted, ""
	}
	left := []seg{{arrow, s.Muted}, {" ", s.Surface}, {name, nameStyle}}
	count := groupCount(env, s, r.servers, true)
	if segsWidth(left)+1+segsWidth(count) > w {
		count = groupCount(env, s, r.servers, false)
	}
	cw := segsWidth(count)
	room := w - cw - 1
	if dir != "" && segsWidth(left)+2+ansi.StringWidth(viewfmt.PlainText(dir)) <= room {
		left = append(left, pad(s, 2), seg{viewfmt.PlainText(dir), s.Muted})
	}
	lw := segsWidth(left)
	if lw+1+cw > w {
		return paint(env, left, w)
	}
	return paint(env, append(append(left, pad(s, w-lw-cw)), count...), w)
}

// renderServer is the signature row: lamp and mark, tool icon, name, port,
// CPU sparkline and memory or the last exit, then the badges in their
// column after the widest of those.
func renderServer(env *ui.Env, s theme.Styles, v supervisor.View, c columns, w int) string {
	g := env.Glyphs
	state := s.State(string(v.Display()))
	glyph, pending := lampGlyph(env, v)
	lampStyle := state.Bold(true)
	if pending {
		lampStyle = s.Starting.Bold(true)
	}
	segs := []seg{pad(s, c.indent), {glyph, lampStyle}, pad(s, c.lamp-ansi.StringWidth(glyph))}
	if c.mark {
		m := markGlyph(env, s, v)
		if m.text == "" {
			m = pad(s, 1)
		}
		segs = append(segs, m)
	}
	segs = append(segs, pad(s, 1))
	if c.icon > 0 {
		icon := " "
		if k := g.Kind[v.Kind]; k != "" {
			icon = k
		}
		segs = append(segs, seg{icon + " ", s.Text2})
	}
	name := ansi.Truncate(viewfmt.PlainText(v.Name), c.name, g.Ellipsis)
	segs = append(segs, seg{name, s.Text}, pad(s, c.name-ansi.StringWidth(name)))
	used := segsWidth(segs)

	var badges []seg
	if c.badges > 0 {
		badges = badgeSegs(env, s, v)
	}
	bw := segsWidth(badges)
	if bw > 0 {
		bw++
	}
	// The tail is everything between the name and the badges.
	var tail []seg
	// A row without a port that shows its last exit starts it in the port
	// column, so "exit 1" keeps its code in narrow rows.
	if c.port > 0 && (v.Port > 0 || running(v)) {
		port := ""
		if v.Port > 0 {
			port = ":" + strconv.Itoa(v.Port)
		}
		tail = append(tail, pad(s, 1+c.port-len(port)), seg{port, s.Port})
	}
	if running(v) {
		if c.spark > 0 {
			tail = append(tail, pad(s, 1), seg{sparkline(g.Spark, v.CPUHistory, c.spark, theme.CPUFloor), state})
		}
		if c.mem > 0 {
			mem := viewfmt.HumanBytes(v.Mem)
			tail = append(tail, pad(s, 1+c.mem-len(mem)), seg{mem, s.Text2})
		}
	} else if long, short, style := lastExit(env, s, v); long != "" {
		room := w - used - bw - segsWidth(tail) - 1
		word := long
		if ansi.StringWidth(long) > room {
			word = ansi.Truncate(short, room, g.Ellipsis)
		}
		if word != "" && room > 0 {
			tail = append(tail, pad(s, 1), seg{word, style})
		}
	}
	segs = append(segs, tail...)
	if bw > 0 {
		at := min(w-bw+1, used+c.tail+1)
		segs = append(segs, pad(s, max(1, at-segsWidth(segs))))
		segs = append(segs, badges...)
	}
	return paint(env, segs, w)
}

// sparkline draws the last samples of vals in width cells, scaled to their
// peak or to floor, whichever is higher. Levels draw one sample a cell;
// Braille two, and cells before the first sample are spaces, not blank
// braille.
func sparkline(p theme.Plot, vals []float64, width int, floor float64) string {
	switch p := p.(type) {
	case theme.Levels:
		vals = vals[max(0, len(vals)-width):]
		peak := floor
		for _, v := range vals {
			peak = max(peak, v)
		}
		cells := []rune(strings.Repeat(string(p.Blank), width-len(vals)))
		for _, v := range vals {
			f := 0.0
			if peak > 0 {
				f = v / peak
			}
			cells = append(cells, p.Step(f))
		}
		return string(cells)
	}
	return strings.ReplaceAll(viewfmt.Sparkline(vals, width, floor), "⠀", " ")
}

// lastExit is what a server that does not run shows, in full and in the
// short form a narrow row falls back to: a done task and when, a stop, or
// the exit code and when, else its status word. A stop has no short form:
// the lamp says it.
func lastExit(env *ui.Env, s theme.Styles, v supervisor.View) (long, short string, style lipgloss.Style) {
	d := v.Display()
	style = s.StateWord(string(d))
	now := env.Now()
	switch {
	case d == supervisor.DisplayDone:
		if v.ExitedAt != nil {
			return "done " + ago(*v.ExitedAt, now), "done", style
		}
		return "done", "done", style
	case d == supervisor.Display(supervisor.Stopped):
		return "stopped", "", style
	case v.ExitCode != nil && v.ExitedAt != nil && (d == supervisor.Display(supervisor.Exited) || d == supervisor.Display(supervisor.Crashed)):
		code := fmt.Sprintf("exit %d", *v.ExitCode)
		return code + env.Glyphs.Separator + ago(*v.ExitedAt, now), code, style
	}
	word := viewfmt.StatusWord(v)
	return word, word, style
}

// ago says how long before now t was.
func ago(t, now time.Time) string {
	d := now.Sub(t)
	switch {
	case d < 10*time.Second:
		return "just now"
	case d < time.Minute:
		return fmt.Sprintf("%d s ago", int(d/time.Second))
	case d < time.Hour:
		return fmt.Sprintf("%d min ago", int(d/time.Minute))
	case d < 48*time.Hour:
		return fmt.Sprintf("%d h ago", int(d/time.Hour))
	}
	return fmt.Sprintf("%d d ago", int(d/(24*time.Hour)))
}

// badgeSegs are the row's badges, state first (unready, config changed,
// disabled), then network exposure, task, schedule, watch and temporary
// copy: icons in a set that has them, else words.
func badgeSegs(env *ui.Env, s theme.Styles, v supervisor.View) []seg {
	g := env.Glyphs
	icons := g.Task != ""
	badge := func(icon, word string) string {
		if icons {
			return icon
		}
		return word
	}
	var out []seg
	add := func(text string, style lipgloss.Style) {
		if len(out) > 0 {
			out = append(out, pad(s, 1))
		}
		out = append(out, seg{text, style})
	}
	if v.Unready {
		add("unready", s.Starting)
	}
	if v.Stale && v.HasProcess() {
		add("changed", s.Warn)
	}
	if !v.Enabled {
		add("disabled", s.Muted)
	}
	if v.NetworkWarning {
		add(badge(g.Network, "network"), s.Warn)
	}
	if v.Oneshot && v.NextRun == nil {
		add(badge(g.Task, "task"), s.Muted)
	}
	if v.NextRun != nil {
		add(badge(g.Schedule, "next")+" "+viewfmt.NextRunLabel(*v.NextRun, env.Now()), s.Muted)
	}
	if len(v.Watch) > 0 {
		add(badge(g.Watch, "watch"), s.Muted)
	}
	if v.Temporary {
		add(badge(g.Worktree, "copy"), s.Muted)
	}
	return out
}
