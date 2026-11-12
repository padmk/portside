package detail

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/padmk/portside/internal/tui/ui"
	"github.com/padmk/portside/internal/viewfmt"
)

// field is one labeled value of the Details or Env tab. Values come from
// the daemon and are made terminal-safe when laid out; a value over
// several lines, or wider than the tab, wraps with the label on its first
// row only.
type field struct {
	label string
	value string
	style lipgloss.Style
	// link makes the value an OSC 8 hyperlink to this address.
	link string
	// copy is what the copy action takes from the field, untouched; empty
	// for a field without a value worth copying.
	copy string
	// secret hides copy in the notice that confirms a copy.
	secret bool
	// info marks a note between the values (the Env tab's source line):
	// the cursor skips it.
	info bool
	// gap puts a blank row before the field, between groups of fields.
	gap bool
}

// maxValueRows is the most rows one value wraps over; the last one ends
// with the ellipsis when there is more, which the copy action still takes.
const maxValueRows = 8

// layout is how fields are drawn: the label column's width and whether
// labels align right (Details) or left (Env's names), and what separates
// a label from its value.
type layout struct {
	labelW int
	right  bool
	sep    string
}

// line is one rendered row and the field it belongs to (-1 for a gap).
type line struct {
	field int
	text  string
}

// lines lays fields out in w cells: a cursor column, the label column,
// the separator and the value, wrapped. Selected is the field under the
// cursor, or -1 for none.
func (l layout) lines(env *ui.Env, fields []field, selected, w int) []line {
	s, g := env.Styles, env.Glyphs
	labelW := 0
	for _, f := range fields {
		if !f.info {
			labelW = max(labelW, ansi.StringWidth(viewfmt.PlainText(f.label)))
		}
	}
	labelW = min(labelW, l.labelW, max(0, w/3))
	valueW := max(1, w-1-labelW-ansi.StringWidth(l.sep))
	var out []line
	for i, f := range fields {
		if f.gap && len(out) > 0 {
			out = append(out, line{field: -1, text: s.Row("", w)})
		}
		sel := i == selected
		var values []string
		if f.info {
			// Notes span the whole width, without a label.
			values = wrap(f.value, max(1, w-1), g.Ellipsis)
		} else {
			values = wrap(f.value, valueW, g.Ellipsis)
		}
		for row, value := range values {
			var segs []seg
			cursor := " "
			if sel {
				cursor = g.Cursor
			}
			segs = append(segs, seg{text: cursor, style: s.Accent})
			if !f.info {
				label := ""
				if row == 0 {
					label = ansi.Truncate(viewfmt.PlainText(f.label), labelW, g.Ellipsis)
				}
				pad := strings.Repeat(" ", max(0, labelW-ansi.StringWidth(label)))
				if l.right {
					label = pad + label
				} else {
					label += pad
				}
				sep := l.sep
				if row > 0 {
					sep = strings.Repeat(" ", ansi.StringWidth(sep))
				}
				segs = append(segs, seg{text: label, style: s.Muted}, seg{text: sep, style: s.Muted})
			}
			// A link spans each row's part of the address.
			segs = append(segs, seg{value, f.style, f.link})
			out = append(out, line{field: i, text: paint(env, segs, w, sel)})
		}
	}
	return out
}

// wrap makes text terminal-safe and wraps it to w cells, breaking at
// spaces and slashes where it can, over at most maxValueRows rows.
func wrap(text string, w int, ellipsis string) []string {
	var rows []string
	for _, l := range viewfmt.PlainLines(text) {
		if ansi.StringWidth(l) <= w {
			rows = append(rows, l)
			continue
		}
		rows = append(rows, strings.Split(ansi.Wrap(l, w, "/"), "\n")...)
	}
	if len(rows) > maxValueRows {
		rows = rows[:maxValueRows]
		last := rows[maxValueRows-1]
		rows[maxValueRows-1] = ansi.Truncate(last, max(0, w-ansi.StringWidth(ellipsis)), "") + ellipsis
	}
	return rows
}

// safeLink reports whether a link target can go into an OSC 8 sequence:
// printable ASCII without spaces, so it cannot end the sequence early.
func safeLink(u string) bool {
	for i := 0; i < len(u); i++ {
		if u[i] <= ' ' || u[i] >= 0x7f {
			return false
		}
	}
	return u != ""
}

// seg is a piece of a row, its style and the address it links to.
type seg struct {
	text  string
	style lipgloss.Style
	link  string
}

// paint joins segs into one row of exactly w cells on the pane's
// background, or on the selection's when selected, which every piece then
// carries so it spans the whole row.
func paint(env *ui.Env, segs []seg, w int, selected bool) string {
	s := env.Styles
	var b strings.Builder
	for _, sg := range segs {
		if sg.text == "" {
			continue
		}
		st := sg.style
		if selected {
			st = st.Background(s.Selected.GetBackground())
		}
		if sg.link != "" && safeLink(sg.link) {
			b.WriteString(ansi.SetHyperlink(sg.link) + st.Render(sg.text) + ansi.ResetHyperlink())
			continue
		}
		b.WriteString(st.Render(sg.text))
	}
	if selected {
		return s.SelectedRow(cut(b.String(), w, env.Glyphs.Ellipsis), w)
	}
	return s.Row(cut(b.String(), w, env.Glyphs.Ellipsis), w)
}

// cut shortens styled text to w cells, ending with the ellipsis.
func cut(text string, w int, ellipsis string) string {
	if ansi.StringWidth(text) <= w {
		return text
	}
	return ansi.Truncate(text, w, ellipsis)
}

// note is a one-row message in a tab (empty states, errors).
func note(env *ui.Env, text string, style lipgloss.Style, w int) string {
	return env.Styles.Row(style.Render(cut(viewfmt.PlainText(text), w, env.Glyphs.Ellipsis)), w)
}
