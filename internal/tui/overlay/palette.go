package overlay

import (
	"slices"
	"sort"
	"strings"
	"unicode"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/padmk/portside/internal/tui/keymap"
	"github.com/padmk/portside/internal/tui/theme"
	"github.com/padmk/portside/internal/tui/ui"
	"github.com/padmk/portside/internal/viewfmt"
)

// PaletteItem is one entry of the command palette: what it says, the key
// that does the same from the current pane (if any) and the command line
// it runs, or for a container, project or port, the action on it (Act.ID
// set) that no command line names. Off holds why it does nothing now;
// Current is set when it acts on the cursor's target.
type PaletteItem struct {
	Label, Key, Command string
	Act                 ui.ActMsg
	Off                 string
	Destructive         bool
	Current             bool
}

// Palette is the command palette (ctrl+p): a query, the entries that
// match it best first and the highlighted one.
type Palette struct {
	query  []rune
	items  []PaletteItem
	shown  []shownItem // best match first
	Cursor int         // index into shown
	offset int
	// rows is the number of entries the last View showed, for clicks.
	rows int
}

// shownItem is an entry that matches the query: its index into items and
// the matched rune indexes of its label, or of its command when only the
// command matches.
type shownItem struct {
	i              int
	label, command []int
}

// NewPalette opens the palette over items, in their order until a query
// ranks them.
func NewPalette(items []PaletteItem) *Palette {
	p := &Palette{items: items}
	p.filter()
	return p
}

// Query is the typed text.
func (p *Palette) Query() string { return string(p.query) }

// Insert types text at the end of the query.
func (p *Palette) Insert(text string) { p.query = append(p.query, []rune(text)...); p.filter() }

// Backspace deletes the last character of the query.
func (p *Palette) Backspace() {
	if len(p.query) > 0 {
		p.query = p.query[:len(p.query)-1]
		p.filter()
	}
}

// DeleteWord deletes the query's last word.
func (p *Palette) DeleteWord() {
	i := len(p.query)
	for i > 0 && unicode.IsSpace(p.query[i-1]) {
		i--
	}
	for i > 0 && !unicode.IsSpace(p.query[i-1]) {
		i--
	}
	p.query = p.query[:i]
	p.filter()
}

// Clear empties the query.
func (p *Palette) Clear() { p.query = nil; p.filter() }

// Down and Up move the highlight, wrapping around.
func (p *Palette) Down() {
	if len(p.shown) > 0 {
		p.Cursor = (p.Cursor + 1) % len(p.shown)
	}
}

func (p *Palette) Up() {
	if len(p.shown) > 0 {
		p.Cursor = (p.Cursor + len(p.shown) - 1) % len(p.shown)
	}
}

// Selected returns the highlighted entry; false when nothing matches.
func (p *Palette) Selected() (PaletteItem, bool) {
	if p.Cursor < 0 || p.Cursor >= len(p.shown) {
		return PaletteItem{}, false
	}
	return p.items[p.shown[p.Cursor].i], true
}

// filter ranks the entries against the query with keymap.Fuzzy on the
// label, falling back to the command as the loosest kind of match: by
// kind of match, then score, then the cursor's target first, entries
// that act before ones that are off, and the given order.
func (p *Palette) filter() {
	q := string(p.query)
	type scored struct {
		shownItem
		m keymap.FuzzyMatch
	}
	var hits []scored
	for i, it := range p.items {
		if strings.TrimSpace(q) == "" {
			hits = append(hits, scored{shownItem: shownItem{i: i}})
			continue
		}
		if m, ok := keymap.Fuzzy(q, it.Label); ok {
			hits = append(hits, scored{shownItem{i: i, label: m.At}, m})
		} else if m, ok := keymap.Fuzzy(q, it.Command); ok {
			m.Rank = keymap.RankLoose
			hits = append(hits, scored{shownItem{i: i, command: m.At}, m})
		}
	}
	sort.SliceStable(hits, func(x, y int) bool {
		a, b := hits[x], hits[y]
		ia, ib := p.items[a.i], p.items[b.i]
		switch {
		case a.m.Rank != b.m.Rank:
			return a.m.Rank > b.m.Rank
		case a.m.Score != b.m.Score:
			return a.m.Score > b.m.Score
		case ia.Current != ib.Current:
			return ia.Current
		case (ia.Off == "") != (ib.Off == ""):
			return ia.Off == ""
		}
		return false
	})
	p.shown = p.shown[:0]
	for _, h := range hits {
		p.shown = append(p.shown, h.shownItem)
	}
	p.Cursor, p.offset = 0, 0
}

// Size is the box's width and height for a w x h screen: at most 72 cells
// wide and 19 rows tall, leaving the top bar and the footer in view. Below
// the list a rule and the selected entry's detail take two rows.
func (p *Palette) Size(w, h int) (bw, bh int) {
	bw = max(min(72, w-2), 20)
	list := max(1, min(len(p.items), 14, h-10))
	return bw, list + 5
}

// Hit returns the index into the shown entries at row y of the box (0 is
// the top border), or -1.
func (p *Palette) Hit(y int) int {
	i := y - 2
	if i < 0 || i >= p.rows {
		return -1
	}
	if i+p.offset >= len(p.shown) {
		return -1
	}
	return i + p.offset
}

// Pick highlights the shown entry i.
func (p *Palette) Pick(i int) {
	if i >= 0 && i < len(p.shown) {
		p.Cursor = i
	}
}

// View renders the palette for a w x h screen: the query with a cursor,
// then the matching entries with their matched characters marked and the
// key or command at the right, destructive ones in the error color and
// ones that are off muted; under a rule, the highlighted entry's detail.
func (p *Palette) View(s theme.Styles, g theme.Glyphs, ascii bool, w, h int) string {
	pn := newPen(s, g)
	bw, bh := p.Size(w, h)
	inner := bw - 4
	var detail []string
	if p.Cursor < len(p.shown) {
		detail = p.detail(pn, p.shown[p.Cursor], inner)
	}
	if len(detail) == 0 {
		detail = []string{""}
	}
	list := bh - 4 - len(detail)
	p.rows = list
	if p.Cursor < p.offset {
		p.offset = p.Cursor
	}
	if p.Cursor >= p.offset+list {
		p.offset = p.Cursor - list + 1
	}
	q := viewfmt.PlainText(string(p.query))
	mark := "› "
	if ascii {
		mark = "> "
	}
	// A long query shows its end, where the caret is.
	if room := inner - ansi.StringWidth(mark) - 1; ansi.StringWidth(q) > room {
		q = ansi.TruncateLeft(q, ansi.StringWidth(q)-room+ansi.StringWidth(g.Ellipsis), g.Ellipsis)
	}
	prompt := pn.s.Accent.Render(mark) + pn.s.Text.Render(q) + pn.s.Text.Reverse(true).Render(" ")
	if q == "" {
		prompt += pn.s.Muted.Render(" restart web, theme nord, shell api")
	}
	rows := []string{prompt}
	for i := p.offset; i < p.offset+list; i++ {
		if i >= len(p.shown) {
			if i == 0 {
				rows = append(rows, pn.s.Muted.Render("no action matches"))
			} else {
				rows = append(rows, "")
			}
			continue
		}
		rows = append(rows, p.row(pn, p.shown[i], inner, i == p.Cursor))
	}
	rows = append(rows, pn.s.Frame.Render(strings.Repeat(g.Border.Top, inner)))
	rows = append(rows, detail...)
	return pn.box("Find an action", rows, inner)
}

// rightColumn is the most of the inner width the key or command at the
// right of a row takes, so the label keeps the rest.
func rightColumn(inner int) int { return max(8, inner/3) }

// labelGap is the least space between a row's label and its key or
// command.
const labelGap = 2

// row is one entry: its label and, at the right, its key, else its
// command cut on the left to rightColumn; the label takes the rest of the
// width.
func (p *Palette) row(pn pen, e shownItem, inner int, selected bool) string {
	it := p.items[e.i]
	right, rightAt := it.Key, []int(nil)
	if right == "" && it.Command != "" {
		right = ":" + it.Command
		for _, i := range e.command {
			rightAt = append(rightAt, i+1)
		}
	}
	rightText := marked{text: []rune(right), at: rightAt}
	if limit := rightColumn(inner); ansi.StringWidth(right) > limit {
		rightText = rightText.tail(limit-ansi.StringWidth(pn.g.Ellipsis), pn.g.Ellipsis)
	}
	room := inner - 2
	if right != "" {
		room -= rightText.width() + labelGap
	}
	labelText := marked{text: []rune(it.Label), at: e.label}
	if labelText.width() > room {
		labelText = labelText.head(room-ansi.StringWidth(pn.g.Ellipsis), pn.g.Ellipsis)
	}
	gap := max(1, inner-2-labelText.width()-rightText.width())
	if selected {
		sel := pn.selected()
		hl := sel.Underline(true)
		line := sel.Render(pn.g.Cursor+" ") + labelText.render(sel, hl) + sel.Render(strings.Repeat(" ", gap)) + rightText.render(sel, hl)
		return theme.Fill(sel, line, inner)
	}
	st := pn.s.Text
	switch {
	case it.Off != "":
		st = pn.s.Muted
	case it.Destructive:
		st = pn.s.Error
	}
	rk := pn.s.Key
	if it.Key == "" {
		rk = pn.s.Muted
	}
	return pn.space(2) + labelText.render(st, pn.s.Match) + pn.space(gap) + rightText.render(rk, pn.s.Match)
}

// detail is the highlighted entry's full label when its row cuts it, the
// command it runs and why it is off, wrapped to at most two lines.
func (p *Palette) detail(pn pen, e shownItem, inner int) []string {
	it := p.items[e.i]
	var parts []string
	right := it.Key
	if right == "" && it.Command != "" {
		right = ":" + it.Command
	}
	room := inner - 2
	if right != "" {
		room -= min(ansi.StringWidth(right), rightColumn(inner)) + labelGap
	}
	if ansi.StringWidth(it.Label) > room {
		parts = append(parts, pn.s.Text.Render(it.Label))
	}
	if it.Command != "" {
		parts = append(parts, pn.s.Muted.Render(":"+it.Command))
	}
	if it.Off != "" {
		parts = append(parts, pn.s.Warn.Render(it.Off))
	}
	if len(parts) == 0 {
		return nil
	}
	lines := strings.Split(ansi.Wrap(strings.Join(parts, pn.s.Muted.Render(pn.g.Separator)), inner, ""), "\n")
	if len(lines) > 2 {
		lines = lines[:2]
		lines[1] = ansi.Truncate(lines[1], inner-ansi.StringWidth(pn.g.Ellipsis), "") + pn.s.Muted.Render(pn.g.Ellipsis)
	}
	return lines
}

// marked is a run of text with the rune indexes the query matched, cut to
// a part of it (from) with an ellipsis before or after.
type marked struct {
	text      []rune
	at        []int
	from      int
	pre, post string
}

func (m marked) width() int {
	return ansi.StringWidth(m.pre) + ansi.StringWidth(string(m.text[m.from:])) + ansi.StringWidth(m.post)
}

// head keeps the first w cells of the text and an ellipsis after them.
func (m marked) head(w int, ellipsis string) marked {
	n, used := 0, 0
	for n < len(m.text) && used+ansi.StringWidth(string(m.text[n])) <= w {
		used += ansi.StringWidth(string(m.text[n]))
		n++
	}
	m.text, m.post = m.text[:n], ellipsis
	return m
}

// tail keeps the last w cells of the text and an ellipsis before them.
func (m marked) tail(w int, ellipsis string) marked {
	from, used := len(m.text), 0
	for from > 0 && used+ansi.StringWidth(string(m.text[from-1])) <= w {
		from--
		used += ansi.StringWidth(string(m.text[from]))
	}
	m.from, m.pre = from, ellipsis
	return m
}

// render draws the text with base and its matched runes with mark.
func (m marked) render(base, mark lipgloss.Style) string {
	var b strings.Builder
	if m.pre != "" {
		b.WriteString(base.Render(m.pre))
	}
	run, on := []rune(nil), false
	flush := func() {
		if len(run) > 0 {
			st := base
			if on {
				st = mark
			}
			b.WriteString(st.Render(string(run)))
			run = run[:0]
		}
	}
	for i := m.from; i < len(m.text); i++ {
		hit := slices.Contains(m.at, i)
		if hit != on {
			flush()
			on = hit
		}
		run = append(run, m.text[i])
	}
	flush()
	if m.post != "" {
		b.WriteString(base.Render(m.post))
	}
	return b.String()
}
