package overlay

import (
	"sort"
	"strings"
	"unicode"

	"github.com/charmbracelet/x/ansi"

	"github.com/padmk/portside/internal/tui/keymap"
	"github.com/padmk/portside/internal/tui/theme"
	"github.com/padmk/portside/internal/tui/ui"
	"github.com/padmk/portside/internal/viewfmt"
)

// PaletteItem is one entry of the command palette: what it says, the key
// that does the same from the current pane (if any) and the command line
// it runs, or for a container, project or port, the action on it (Act.ID
// set) that no command line names. Off holds why it does nothing now.
type PaletteItem struct {
	Label, Key, Command string
	Act                 ui.ActMsg
	Off                 string
	Destructive         bool
}

// Palette is the command palette (ctrl+p): a query, the entries that
// match it best first and the highlighted one.
type Palette struct {
	query  []rune
	items  []PaletteItem
	shown  []int // indexes into items, best match first
	Cursor int   // index into shown
	offset int
	// rows is the number of entries the last View showed, for clicks.
	rows int
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
	return p.items[p.shown[p.Cursor]], true
}

// filter ranks the entries against the query (keymap.Fuzzy on the label
// and the command), keeping the given order between equal scores.
func (p *Palette) filter() {
	q := string(p.query)
	type scored struct{ i, score int }
	var hits []scored
	for i, it := range p.items {
		if strings.TrimSpace(q) == "" {
			hits = append(hits, scored{i, 0})
			continue
		}
		a, okA := keymap.Fuzzy(q, it.Label)
		b, okB := keymap.Fuzzy(q, it.Command)
		if okA || okB {
			hits = append(hits, scored{i, max(a, b)})
		}
	}
	sort.SliceStable(hits, func(x, y int) bool { return hits[x].score > hits[y].score })
	p.shown = p.shown[:0]
	for _, h := range hits {
		p.shown = append(p.shown, h.i)
	}
	p.Cursor, p.offset = 0, 0
}

// Size is the box's width and height for a w x h screen: at most 72 cells
// wide and 17 rows tall, leaving the top bar and the footer in view.
func (p *Palette) Size(w, h int) (bw, bh int) {
	bw = max(min(72, w-2), 20)
	list := max(1, min(len(p.items), 14, h-8))
	return bw, list + 3
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
// then the matching entries with the key or command at the right,
// destructive ones in the error color and ones that are off muted with
// the reason; the footer has the keys.
func (p *Palette) View(s theme.Styles, g theme.Glyphs, ascii bool, w, h int) string {
	pn := newPen(s, g)
	bw, bh := p.Size(w, h)
	inner := bw - 4
	list := bh - 3
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
		rows = append(rows, p.row(pn, p.items[p.shown[i]], inner, i == p.Cursor))
	}
	return pn.box("Find an action", rows, inner)
}

// row is one entry: its label (with the reason it is off) and, at the
// right, its key, else its command.
func (p *Palette) row(pn pen, it PaletteItem, inner int, selected bool) string {
	right := it.Key
	if right == "" && it.Command != "" {
		right = ":" + it.Command
	}
	label := it.Label
	if it.Off != "" {
		label += " (" + it.Off + ")"
	}
	room := max(1, inner-2-ansi.StringWidth(right)-2)
	if ansi.StringWidth(label) > room {
		label = ansi.Truncate(label, room-1, "") + pn.g.Ellipsis
	}
	gap := max(1, inner-2-ansi.StringWidth(label)-ansi.StringWidth(right))
	if selected {
		sel := pn.selected()
		return theme.Fill(sel, sel.Render(pn.g.Cursor+" "+label+strings.Repeat(" ", gap)+right), inner)
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
	return pn.space(2) + st.Render(label) + pn.space(gap) + rk.Render(right)
}
