package overlay

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/padmk/portside/internal/tui/keymap"
	"github.com/padmk/portside/internal/tui/theme"
)

// MenuItem is one row of a menu: the action it runs, its label and the
// key that runs it (or its command when it has no key there). Off holds
// why the action does nothing on the menu's target; the row is then muted
// and marked (decorate), the reason shows under the list while the row is
// highlighted, and running it shows the reason.
type MenuItem struct {
	ActionID, Label, Key string
	Off                  string
	Destructive          bool
	// Check marks a toggle: on or off; NoCheck for other actions.
	Check Check
	// Section names the part of the menu the item belongs to ("Server",
	// "Daemon"); a menu with several sections shows each name as a muted
	// header above its items, which come together.
	Section string
}

// Check is a menu item's toggle state.
type Check int

const (
	NoCheck Check = iota
	CheckOff
	CheckOn
)

// Menu is the menu of a pane's actions (x or a right click), titled with
// what its actions act on.
type Menu struct {
	Title  string
	Items  []MenuItem
	Cursor int
	// MaxWidth is the widest the box may be, the screen's width; labels
	// are cut to fit. 0 leaves only the menuMaxLabel cap.
	MaxWidth int
	// MaxHeight is the tallest the box may be, borders included; a longer
	// menu scrolls with the cursor and says how much more there is. 0 is
	// no limit.
	MaxHeight int
	// offset is the first line shown (lines()).
	offset int
}

// menuLine is a line of a menu's body: an item (its index) or, for
// item -1, a section header.
type menuLine struct {
	item    int
	section string
}

// lines are the menu's body lines: its items, each section's header
// before its first item when the menu has more than one section.
func (m Menu) lines() []menuLine {
	sections := 0
	for i, it := range m.Items {
		if i == 0 || it.Section != m.Items[i-1].Section {
			sections++
		}
	}
	out := make([]menuLine, 0, len(m.Items)+sections)
	for i, it := range m.Items {
		if sections > 1 && it.Section != "" && (i == 0 || it.Section != m.Items[i-1].Section) {
			out = append(out, menuLine{item: -1, section: it.Section})
		}
		out = append(out, menuLine{item: i})
	}
	return out
}

// window is the part of lines() the box shows: lines [start, end), with
// a "more" row above (up) or below (down) them when lines are hidden
// there. It starts at the kept offset and moves only as far as the
// cursor's line (with its section header) needs.
func (m Menu) window() (start, end int, up, down bool) {
	lines := m.lines()
	n := len(lines)
	avail := n
	if m.MaxHeight > 0 {
		avail = max(3, m.MaxHeight-2-m.reasonHeight(m.inner()))
	}
	if n <= avail {
		return 0, n, false, false
	}
	cur := 0
	for i, l := range lines {
		if l.item == m.Cursor {
			cur = i
		}
	}
	first := cur
	if cur > 0 && lines[cur-1].item < 0 {
		first = cur - 1
	}
	room := func(off int) (int, bool, bool) {
		up := off > 0
		r := avail - b2i(up)
		down := off+r < n
		return r - b2i(down), up, down
	}
	// The last window fills the box: "more" above and lines to the end.
	off := max(0, min(m.offset, n-(avail-1)))
	for range 4 {
		r, _, _ := room(off)
		switch {
		case cur < off:
			off = first
			continue
		case cur >= off+r:
			off = min(cur-r+1, n-(avail-1))
			continue
		}
		break
	}
	if first < off {
		if r, _, _ := room(first); cur < first+r {
			off = first
		}
	}
	r, up, down := room(off)
	return off, min(n, off+r), up, down
}

// follow keeps the offset where the window put it, after the cursor
// moved.
func (m *Menu) follow() {
	start, _, _, _ := m.window()
	m.offset = start
}

// MenuFor returns the menu of the scope's actions marked Menu, each with
// its first key, or its command when it has none.
func MenuFor(r *keymap.Registry, scope keymap.Scope, ascii bool) Menu {
	var m Menu
	for _, a := range r.InScope(scope) {
		if !a.Menu {
			continue
		}
		m.Items = append(m.Items, ItemFor(a, ascii))
	}
	return m
}

// ItemFor is an action's menu row.
func ItemFor(a keymap.Action, ascii bool) MenuItem {
	key := ":" + a.ID
	if len(a.Keys) > 0 {
		key = KeyLabel(a.Keys[0], ascii)
	}
	return MenuItem{ActionID: a.ID, Label: a.Help, Key: key, Destructive: a.Destructive}
}

// Down moves the cursor down, wrapping around.
func (m *Menu) Down() {
	if len(m.Items) > 0 {
		m.Cursor = (m.Cursor + 1) % len(m.Items)
	}
	m.follow()
}

// Up moves the cursor up, wrapping around.
func (m *Menu) Up() {
	if len(m.Items) > 0 {
		m.Cursor = (m.Cursor + len(m.Items) - 1) % len(m.Items)
	}
	m.follow()
}

// menuMaxLabel caps a row's label, so a long label never makes the menu
// wider than a small terminal; reasonRows caps the lines of the reason
// under the list.
const (
	menuMaxLabel = 44
	reasonRows   = 2
)

// label is an item's text: a toggle's mark (or room for it) and its
// label, cut to max cells with tail.
func (it MenuItem) label(max int, tail, mark string) string {
	l := it.Label
	switch it.Check {
	case CheckOn:
		l = mark + " " + l
	case CheckOff:
		l = strings.Repeat(" ", ansi.StringWidth(mark)+1) + l
	}
	if ansi.StringWidth(l) > max {
		l = ansi.Truncate(l, max-1, "") + tail
	}
	return l
}

// labelMax is the widest a label may be: menuMaxLabel, less where
// MaxWidth leaves no room for it beside the longest key.
func (m Menu) labelMax() int {
	key := 0
	for _, it := range m.Items {
		key = max(key, ansi.StringWidth(it.Key))
	}
	if m.MaxWidth <= 0 {
		return menuMaxLabel
	}
	return max(4, min(menuMaxLabel, m.MaxWidth-4-2-3-key))
}

// inner is the width inside the padding: the cursor marker's two cells,
// the longest label, three spaces and the longest key, at least the
// title's width (cut to MaxWidth).
func (m Menu) inner() int {
	label, key := 0, 0
	for _, it := range m.Items {
		room := markRoom(it.Off != "", it.Destructive)
		label = max(label, ansi.StringWidth(it.label(m.labelMax()-room, "~", "x"))+room)
		key = max(key, ansi.StringWidth(it.Key))
	}
	w := max(2+label+3+key, ansi.StringWidth(m.Title)+1)
	if m.MaxWidth > 0 {
		w = min(w, m.MaxWidth-4)
	}
	return w
}

// Size is the box's width and height, border included.
func (m Menu) Size() (w, h int) {
	start, end, up, down := m.window()
	inner := m.inner()
	return inner + 4, end - start + b2i(up) + b2i(down) + m.reasonHeight(inner) + 2
}

// reason is why item i is off, as a line under the list says it.
func (m Menu) reason(i int) string { return m.Items[i].Label + ": " + m.Items[i].Off }

// reasonLines wraps item i's reason into at most reasonRows lines of w
// cells, the last one cut with tail.
func (m Menu) reasonLines(i, w int, tail string) []string {
	lines := strings.Split(ansi.Wrap(m.reason(i), max(1, w), ""), "\n")
	if len(lines) > reasonRows {
		lines = lines[:reasonRows]
		lines[reasonRows-1] = ansi.Truncate(lines[reasonRows-1], w-ansi.StringWidth(tail), "") + tail
	}
	return lines
}

// reasonHeight is the rows under the list for the highlighted item's
// reason: a blank row and the reason's lines while that item is off, else
// none.
func (m Menu) reasonHeight(inner int) int {
	c := m.Cursor
	if c < 0 || c >= len(m.Items) || m.Items[c].Off == "" {
		return 0
	}
	return len(m.reasonLines(c, inner, "~")) + 1
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// Hit returns the item at row y of the box (0 is the top border), or -1
// for a border, a section header or a "more" row.
func (m Menu) Hit(y int) int {
	start, end, up, _ := m.window()
	i := y - 1 - b2i(up)
	if y < 1 || i < 0 || start+i >= end {
		return -1
	}
	return m.lines()[start+i].item
}

// View renders the menu with the cursor row marked and highlighted;
// destructive items are in the error color, items that are off muted with
// the highlighted one's reason under the list, both with decorate's
// marker where color does not tell them apart, section headers muted, and
// a scrolled menu says how many items are above and below what it shows.
func (m Menu) View(s theme.Styles, g theme.Glyphs) string {
	pn := newPen(s, g)
	inner := m.inner()
	lines := m.lines()
	start, end, up, down := m.window()
	arrowUp, arrowDown, check := "↑", "↓", "✓"
	if g.ID == theme.ASCII.ID {
		arrowUp, arrowDown, check = "^", "v", "x"
	}
	more := func(arrow string, from, to int) string {
		n := 0
		for _, l := range lines[from:to] {
			if l.item >= 0 {
				n++
			}
		}
		return pn.space(2) + pn.s.Muted.Render(fmt.Sprintf("%s %d more", arrow, n))
	}
	var rows []string
	if up {
		rows = append(rows, more(arrowUp, 0, start))
	}
	for _, l := range lines[start:end] {
		if l.item < 0 {
			rows = append(rows, pn.s.Muted.Render(ansi.Truncate(l.section, inner, g.Ellipsis)))
			continue
		}
		i, it := l.item, m.Items[l.item]
		label, mark := decorate(pn.s, it.Off != "", it.Destructive, i == m.Cursor)
		text := it.label(m.labelMax()-markRoom(it.Off != "", it.Destructive), g.Ellipsis, check)
		gap := max(0, inner-2-ansi.StringWidth(text)-ansi.StringWidth(mark)-ansi.StringWidth(it.Key))
		if i == m.Cursor {
			rows = append(rows, Highlight(pn.s, g.Cursor+" "+text+mark+strings.Repeat(" ", gap)+it.Key, inner))
			continue
		}
		rows = append(rows, pn.space(2)+label.Render(text)+pn.s.Muted.Render(mark)+pn.space(gap)+pn.s.Muted.Render(it.Key))
	}
	if down {
		rows = append(rows, more(arrowDown, end, len(lines)))
	}
	if m.reasonHeight(inner) > 0 {
		rows = append(rows, "")
		for _, l := range m.reasonLines(m.Cursor, inner, g.Ellipsis) {
			rows = append(rows, pn.s.Text2.Render(l))
		}
	}
	return pn.box(m.Title, rows, inner)
}
