package detail

import (
	"fmt"

	"charm.land/lipgloss/v2"

	"github.com/padmk/portside/internal/tui/ui"
)

// fieldList is a tab of labeled values (Details, Env): the field under
// the cursor, the first row shown and whether the next frame scrolls to
// the cursor (after the cursor moved; after a scroll the cursor follows
// the rows instead); from the last frame, the fields, the field each row
// shows and the height, for keys, clicks and copying.
type fieldList struct {
	scroll, cursor int
	reveal         bool
	rows           int
	fields         []field
	owners         []int
}

// click puts the cursor on the field shown on content row y.
func (l *fieldList) click(y int) {
	i := l.scroll + y
	if y < 0 || i >= len(l.owners) || l.owners[i] < 0 || l.fields[l.owners[i]].info {
		return
	}
	l.cursor = l.owners[i]
}

func (l *fieldList) scrollBy(delta int) {
	l.scroll = max(0, min(l.scroll+delta, len(l.owners)-l.rows))
	l.reveal = false
}

// move moves the cursor delta fields, skipping notes, and scrolls to it
// in the next frame.
func (l *fieldList) move(delta int) {
	i := l.cursor
	step := 1
	if delta < 0 {
		step, delta = -1, -delta
	}
	for ; delta > 0; delta-- {
		next := i + step
		for next >= 0 && next < len(l.fields) && l.fields[next].info {
			next += step
		}
		if next < 0 || next >= len(l.fields) {
			break
		}
		i = next
	}
	l.cursor = i
	l.reveal = true
}

// do runs the cursor and scroll keys; false for other actions. page is a
// full frame's rows, as the log tabs page.
func (l *fieldList) do(id string) bool {
	page := max(1, l.rows)
	switch id {
	case "down":
		l.move(1)
	case "up":
		l.move(-1)
	case "next-page":
		l.move(page)
	case "prev-page":
		l.move(-page)
	case "top":
		l.move(-len(l.fields))
	case "bottom":
		l.move(len(l.fields))
	case "scroll-down":
		l.scrollBy(wheelRows)
	case "scroll-up":
		l.scrollBy(-wheelRows)
	case "half-down":
		l.scrollBy(max(1, page/2))
	case "half-up":
		l.scrollBy(-max(1, page/2))
	case "page-down":
		l.scrollBy(page)
	case "page-up":
		l.scrollBy(-page)
	default:
		return false
	}
	return true
}

// current is the field under the cursor.
func (l *fieldList) current() (field, bool) {
	if l.cursor < 0 || l.cursor >= len(l.fields) || l.fields[l.cursor].info {
		return field{}, false
	}
	return l.fields[l.cursor], true
}

// view lays fields out in cw x ch, keeps the cursor on a field and in
// view, and returns the rows from the scroll position on with the
// position for the bottom border when they overflow. The cursor is drawn
// only while the pane is focused.
func (l *fieldList) view(env *ui.Env, fields []field, lay layout, cw, ch int, focused bool) ([]string, string) {
	l.fields, l.rows = fields, ch
	var selectable []int
	for i, f := range fields {
		if !f.info {
			selectable = append(selectable, i)
		}
	}
	cur := -1
	if len(selectable) > 0 {
		cur = max(selectable[0], min(l.cursor, selectable[len(selectable)-1]))
		for fields[cur].info {
			cur++
		}
	}
	lines := lay.lines(env, fields, -1, cw)
	owners := make([]int, len(lines))
	for i, ln := range lines {
		owners[i] = ln.field
	}
	l.owners = owners
	first, last := -1, -1
	for i, o := range owners {
		if o == cur {
			if first < 0 {
				first = i
			}
			last = i
		}
	}
	scroll := max(0, min(l.scroll, len(lines)-ch))
	if cur >= 0 {
		if l.reveal {
			if first < scroll {
				scroll = first
			} else if last >= scroll+ch {
				scroll = max(0, min(first, last-ch+1))
			}
		} else if last < scroll || first >= scroll+ch {
			// A scroll moved the field out of view: the cursor follows.
			for i := scroll; i < min(len(owners), scroll+ch); i++ {
				if o := owners[i]; o >= 0 && !fields[o].info {
					cur = o
					break
				}
			}
		}
	}
	l.scroll, l.reveal = scroll, false
	if cur >= 0 {
		l.cursor = cur
	}
	sel := -1
	if focused {
		sel = cur
	}
	rendered := lay.lines(env, fields, sel, cw)
	out := make([]string, 0, ch)
	for i := scroll; i < len(rendered) && len(out) < ch; i++ {
		out = append(out, rendered[i].text)
	}
	status := ""
	if len(lines) > ch && cur >= 0 {
		pos := 0
		for i, f := range selectable {
			if f == cur {
				pos = i + 1
			}
		}
		status = fmt.Sprintf("%d/%d", pos, len(selectable))
	}
	return out, status
}

// Fact is one labeled value of a Facts list. Value is made terminal-safe
// when laid out and wraps; Copy is what the copy action takes, empty for
// a value not worth copying; Note makes a line between the values that
// the cursor skips (a loading or error line).
type Fact struct {
	Label, Value, Copy string
	Style              lipgloss.Style
	Note               bool
}

// Facts is a Details tab of labeled values outside this package (a
// container's), laid out, navigated and positioned as a server's Details
// tab is: right-aligned labels, long values wrapped, the position in the
// bottom border when it overflows, the cursor drawn while focused, page
// keys a full frame.
type Facts struct{ list fieldList }

// View renders facts in w x h and returns the rows and the position for
// the bottom border, "" when everything fits.
func (f *Facts) View(env *ui.Env, facts []Fact, w, h int, focused bool) ([]string, string) {
	fields := make([]field, len(facts))
	for i, x := range facts {
		fields[i] = field{label: x.Label, value: x.Value, style: x.Style, copy: x.Copy, info: x.Note}
	}
	return f.list.view(env, fields, detailsLayout, w, h, focused)
}

// Do runs a cursor or scroll action; false for the others.
func (f *Facts) Do(id string) bool { return f.list.do(id) }

// Click puts the cursor on the fact shown on content row y.
func (f *Facts) Click(y int) { f.list.click(y) }

// Wheel scrolls by the wheel's step, up for a negative direction.
func (f *Facts) Wheel(dir int) { f.list.scrollBy(dir * wheelRows) }

// Reset goes back to the first fact, for another item.
func (f *Facts) Reset() { f.list = fieldList{} }

// Current is the fact under the cursor as its label and what copy takes.
func (f *Facts) Current() (label, copy string, ok bool) {
	x, ok := f.list.current()
	return x.label, x.copy, ok
}
