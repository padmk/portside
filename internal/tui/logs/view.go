package logs

import (
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/padmk/portside/internal/tui/ui"
)

// The sequence is the lines shown: all of them, or only the matching ones
// while filtering. Positions in it are sequence indexes; lines keep their
// absolute indexes.

func (p *Pane) seqLen() int {
	if p.filtering() {
		return len(p.search.matches)
	}
	return p.buf.Len()
}

func (p *Pane) seqAt(k int) int {
	if p.filtering() {
		// With no matches nothing is shown; the first line stands in.
		if len(p.search.matches) == 0 {
			return p.base
		}
		return p.search.matches[min(max(k, 0), len(p.search.matches)-1)]
	}
	return p.base + min(max(k, 0), max(p.buf.Len()-1, 0))
}

// seqIndex is the position of the shown line at or above abs.
func (p *Pane) seqIndex(abs int) int {
	if p.filtering() {
		i, found := slices.BinarySearch(p.search.matches, abs)
		if !found {
			i--
		}
		return max(0, min(i, len(p.search.matches)-1))
	}
	return max(0, min(abs-p.base, p.buf.Len()-1))
}

func (p *Pane) line(abs int) Line { return p.buf.At(abs - p.base) }

// pos is a scroll position: the sequence index of the bottom line and how
// many of its rows are below the screen, so a wrapped line taller than the
// screen can be read from its first row.
type pos struct{ k, off int }

// before reports whether a shows earlier output than b.
func (a pos) before(b pos) bool { return a.k < b.k || a.k == b.k && a.off > b.off }

// bottomPos is the current position.
func (p *Pane) bottomPos() pos {
	n := p.seqLen()
	if p.follow || n == 0 {
		return pos{max(n-1, 0), 0}
	}
	k := p.seqIndex(p.anchor)
	off := p.rowOff
	if p.seqAt(k) != p.anchor {
		off = 0
	}
	return pos{k, max(0, min(off, p.rowCount(p.seqAt(k))-1))}
}

func (p *Pane) setPos(q pos) { p.anchor, p.rowOff = p.seqAt(q.k), q.off }

// anchorIndex is the sequence index of the bottom line.
func (p *Pane) anchorIndex() int { return p.bottomPos().k }

func (p *Pane) anchorAbs() int {
	if p.seqLen() == 0 {
		return p.base
	}
	return p.seqAt(p.anchorIndex())
}

// posWithTop is the position that shows the first row of the line at
// sequence index k at the top of the screen, or the end of the output
// when it does not fill the screen from there.
func (p *Pane) posWithTop(k int) pos {
	n, screen, rows := p.seqLen(), max(p.rows, 1), 0
	for j := max(k, 0); j < n; j++ {
		c := p.rowCount(p.seqAt(j))
		if rows+c >= screen {
			return pos{j, rows + c - screen}
		}
		rows += c
	}
	return pos{max(n-1, 0), 0}
}

// clampAnchor keeps a paused view inside the lines: never above the
// position showing the first line at the top.
func (p *Pane) clampAnchor() {
	if p.follow || p.seqLen() == 0 {
		return
	}
	q := p.bottomPos()
	if m := p.posWithTop(0); q.before(m) {
		q = m
	}
	p.setPos(q)
}

// up and down move a position by n screen rows.
func (p *Pane) up(q pos, n int) pos {
	for ; n > 0; n-- {
		switch {
		case q.off < p.rowCount(p.seqAt(q.k))-1:
			q.off++
		case q.k > 0:
			q.k, q.off = q.k-1, 0
		default:
			return q
		}
	}
	return q
}

func (p *Pane) down(q pos, n int) pos {
	for ; n > 0; n-- {
		switch {
		case q.off > 0:
			q.off--
		case q.k < p.seqLen()-1:
			q.k++
			q.off = p.rowCount(p.seqAt(q.k)) - 1
		default:
			return q
		}
	}
	return q
}

// scroll moves the view by delta screen rows; moving up pauses following.
func (p *Pane) scroll(delta int) {
	if p.seqLen() == 0 || delta == 0 || delta > 0 && p.follow {
		return
	}
	q := p.bottomPos()
	if delta < 0 {
		p.setFollow(false)
		q = p.up(q, -delta)
	} else {
		q = p.down(q, delta)
	}
	p.setPos(q)
	p.clampAnchor()
}

// reveal pauses and scrolls so the first match in the line at absolute
// index abs is in the middle of the screen: its row of a wrapped line, or
// with lines cut, the cells around it.
func (p *Pane) reveal(abs int) {
	p.setFollow(false)
	k := p.seqIndex(abs)
	l := p.line(abs)
	row := 0
	if loc := p.matchIn(l); loc != nil && l.Rule == nil {
		if p.wrap {
			row = len(Rows(l.Plain[:loc[1]], p.gutterText(l), p.width, true, 0, "")) - 1
		} else {
			avail := max(1, p.width-ansi.StringWidth(p.gutterText(l)))
			start, end := ansi.StringWidth(l.Plain[:loc[0]]), ansi.StringWidth(l.Plain[:loc[1]])
			if start < p.left || end > p.left+avail {
				p.left = max(0, start-avail/4)
			}
		}
	}
	q := pos{k, max(0, p.rowCount(abs)-1-row)}
	p.setPos(p.down(q, (max(p.rows, 1)-1)/2))
	p.clampAnchor()
}

func (p *Pane) matchIn(l Line) []int {
	if p.search.re == nil {
		return nil
	}
	return p.search.re.FindStringIndex(l.Plain)
}

// scrollLeft moves the cut lines by a quarter of the width (dir 1 to the
// right, -1 back).
func (p *Pane) scrollLeft(dir int) tea.Cmd {
	if p.wrap {
		return notice("lines are wrapped; w cuts them for scrolling sideways", ui.LevelInfo)
	}
	step := max(4, p.width/4)
	p.left = max(0, p.left+dir*step)
	if widest := p.widest(); p.left > widest-step {
		p.left = max(0, min(p.left, widest-step))
	}
	return nil
}

// widest is the width of the widest line on screen.
func (p *Pane) widest() int {
	w := 0
	q := p.bottomPos()
	for k := q.k; k >= 0 && k > q.k-max(p.rows, 1); k-- {
		if k < p.seqLen() {
			w = max(w, ansi.StringWidth(p.line(p.seqAt(k)).Plain))
		}
	}
	return w
}

func (p *Pane) rowCount(abs int) int {
	if !p.wrap || p.buf.Len() == 0 {
		return 1
	}
	return len(p.lineRows(nil, abs))
}

// visibleRows renders the rows on screen: upward from the bottom line's
// last row on screen, then below it while the first lines do not fill
// the screen.
func (p *Pane) visibleRows(env *ui.Env, h int) []string {
	q := p.bottomPos()
	r := p.lineRows(env, p.seqAt(q.k))
	cut := max(0, len(r)-q.off)
	rows, below := r[:cut], r[cut:]
	top := q.k
	for k := q.k - 1; k >= 0 && len(rows) < h; k-- {
		rows = append(p.lineRows(env, p.seqAt(k)), rows...)
		top = k
	}
	p.top = top
	if len(rows) > h {
		rows = rows[len(rows)-h:]
	}
	rows = append(rows, below...)
	for j := q.k + 1; j < p.seqLen() && len(rows) < h; j++ {
		rows = append(rows, p.lineRows(env, p.seqAt(j))...)
	}
	rows = rows[:min(len(rows), h)]
	for len(rows) < h {
		rows = append(rows, strings.Repeat(" ", p.width))
	}
	return rows
}

// gutterText is a line's time column, unstyled; "" when it is hidden.
func (p *Pane) gutterText(l Line) string {
	if !p.showStamps() || l.Rule != nil {
		return ""
	}
	if l.At.IsZero() || strings.TrimSpace(l.Plain) == "" {
		return strings.Repeat(" ", gutterWidth)
	}
	return l.At.Format("15:04:05") + " "
}

// lineRows renders one line in the pane's width; without env (for
// counting rows) nothing is styled.
func (p *Pane) lineRows(env *ui.Env, abs int) []string {
	l := p.line(abs)
	if l.Rule != nil {
		return p.ruleRows(env, abs, l.Rule)
	}
	gutter := p.gutterText(l)
	if env == nil {
		return Rows(l.Plain, gutter, p.width, p.wrap, p.left, "")
	}
	ellipsis := env.Glyphs.Ellipsis
	if p.visual && p.selected(abs) {
		var out []string
		for _, r := range Rows(l.Plain, gutter, p.width, p.wrap, p.left, ellipsis) {
			out = append(out, env.Styles.Selected.Render(r))
		}
		return out
	}
	if gutter != "" {
		gutter = env.Styles.Muted.Render(strings.TrimSuffix(gutter, " ")) + " "
	}
	text := highlight(env.Styles, l, p.search.re, abs == p.search.current)
	rows := Rows(text, gutter, p.width, p.wrap, p.left, ellipsis)
	for i, r := range rows {
		if strings.Contains(r, "\x1b") {
			// Styles and links end with their row, not in the border.
			rows[i] = r + "\x1b]8;;\x1b\\\x1b[m"
		}
	}
	return rows
}

// ruleRows renders a marker line as a rule across the pane, without the
// time column.
func (p *Pane) ruleRows(env *ui.Env, abs int, r *Rule) []string {
	if env == nil {
		return []string{""}
	}
	row := ruleRow(r.Label, p.width, env.Glyphs.Rule, env.Glyphs.Ellipsis)
	switch {
	case p.visual && p.selected(abs):
		return []string{env.Styles.Selected.Render(row)}
	case r.Failed:
		return []string{env.Styles.Error.Render(row)}
	}
	return []string{env.Styles.Muted.Render(row)}
}
