package form

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/padmk/portside/internal/tui/overlay"
	"github.com/padmk/portside/internal/tui/theme"
	"github.com/padmk/portside/internal/tui/ui"
	"github.com/padmk/portside/internal/viewfmt"
)

const (
	// maxInner is the widest a form's content gets; maxLabel caps the
	// label column.
	maxInner = 76
	maxLabel = 24
	// listRows is how many items of a list show while the cursor is
	// elsewhere.
	listRows = 4
	// errRows and helpRows cap the wrapped error and help under a field.
	errRows  = 3
	helpRows = 2
	// fullRows caps the full value under the highlighted field.
	fullRows = 6
)

// Hints are the key hints for the footer while the form is open; esc is
// always among them.
func (m *Model) Hints(ascii bool) [][2]string {
	if m.editing {
		f := m.Fields[m.cursor]
		if f.Kind == List && f.Lines {
			return [][2]string{{"esc", "keep"}, {overlay.KeyLabel("enter", ascii), "new line"}, {"tab", "next"}, {overlay.KeyLabel("ctrl+s", ascii), "save"}}
		}
		return [][2]string{{"esc", "keep"}, {"tab", "next"}, {overlay.KeyLabel("ctrl+s", ascii), "save"}}
	}
	f := m.Fields[m.cursor]
	act := "edit"
	switch {
	case f.ReadOnly != "":
		act = ""
	case f.Kind == Toggle:
		act = "switch"
	case f.Kind == Choice:
		act = "next choice"
	case f.Kind == fold:
		act = "show"
	}
	out := [][2]string{{"j/k", "move"}}
	if act != "" {
		out = append(out, [2]string{overlay.KeyLabel("enter", ascii), act})
	}
	return append(out, [2]string{overlay.KeyLabel("ctrl+s", ascii), "save"}, [2]string{"esc", "close"})
}

// row is one content row: its text, the field it belongs to and whether
// it holds the input's cursor.
type row struct {
	text  string
	field int
	caret bool
}

// Size is the inner width the form takes on a screen w cells wide.
func Size(w int) int { return max(20, min(maxInner, w-8)) }

// View renders the form as an overlay box for a screen w x h cells: the
// title in the top border, the fields, and below them the error no field
// names, the busy line and the hints. The fields scroll to keep the
// cursor's field, its help and its error in view.
func (m *Model) View(env *ui.Env, w, h int) string {
	s := overlay.Styles(env.Styles)
	g := env.Glyphs
	inner := Size(w)
	labelW := 0
	for _, f := range m.Fields {
		labelW = max(labelW, ansi.StringWidth(f.Label))
	}
	labelW = min(labelW, maxLabel, inner/3)
	valueW := max(8, inner-2-labelW-2)
	indent := strings.Repeat(" ", 2+labelW+2)

	var rows []row
	shown, at := 0, 0
	for i := range m.Fields {
		if !m.shown(i) {
			continue
		}
		if shown++; i == m.cursor {
			at = shown
		}
		f := &m.Fields[i]
		if f.Section != "" && (i == 0 || m.Fields[i-1].Section != f.Section) {
			if i > 0 {
				rows = append(rows, row{text: "", field: i})
			}
			rows = append(rows, row{text: s.Muted.Bold(true).Render(f.Section), field: i})
		}
		values, caret := m.valueLines(env, s, i, valueW)
		label := ansi.Truncate(f.Label, labelW, g.Ellipsis)
		pad := strings.Repeat(" ", labelW-ansi.StringWidth(label)+2)
		selected := i == m.cursor
		labelStyle := s.Text2
		if f.Err != "" {
			labelStyle = s.Error
		}
		first := ""
		switch {
		case selected && !m.editing:
			first = overlay.Highlight(s, g.Cursor+" "+label+pad+values[0], inner)
		case selected:
			first = s.OverlaySelected.Render(g.Cursor+" "+label) + s.Surface.Render(pad) + values[0]
		default:
			first = s.Surface.Render("  ") + labelStyle.Render(label) + s.Surface.Render(pad) + values[0]
		}
		rows = append(rows, row{text: first, field: i, caret: caret == 0})
		for k, v := range values[1:] {
			rows = append(rows, row{text: s.Surface.Render(indent) + v, field: i, caret: caret == k+1})
		}
		if f.Err != "" {
			for _, l := range wrap(f.Err, valueW, errRows, g.Ellipsis) {
				rows = append(rows, row{text: s.Surface.Render(indent) + s.Error.Render(l), field: i})
			}
		}
		if selected && !m.editing {
			for _, l := range m.fullValue(i, valueW, g.Ellipsis) {
				rows = append(rows, row{text: s.Surface.Render(indent) + s.Text2.Render(l), field: i})
			}
		}
		if selected {
			for _, l := range wrap(f.Help, valueW, helpRows, g.Ellipsis) {
				rows = append(rows, row{text: s.Surface.Render(indent) + s.Muted.Render(l), field: i})
			}
		}
	}

	var foot []string
	if m.Err != "" {
		for _, l := range wrap(m.Err, inner, errRows, g.Ellipsis) {
			foot = append(foot, s.Error.Render(l))
		}
	}
	if m.Busy != "" {
		foot = append(foot, s.Muted.Render(m.Busy))
	}
	foot = append(foot, "")
	pairs := m.Hints(g.ID == theme.ASCII.ID)

	// The box leaves the top bar and the footer in view: its borders, the
	// rows below the fields and the hints take h-2 at most.
	avail := max(1, min(len(rows), h-2-2-len(foot)-1))
	m.scrollTo(rows, avail)
	hint := overlay.HintFit(env.Styles, g, inner, pairs...)
	if len(rows) > avail {
		pos := s.Muted.Render(fmt.Sprintf("%d/%d", at, shown))
		hint = overlay.HintFit(env.Styles, g, inner-1-lipgloss.Width(pos), pairs...)
		if gap := inner - lipgloss.Width(hint) - lipgloss.Width(pos); gap > 0 {
			hint += s.Surface.Render(strings.Repeat(" ", gap)) + pos
		}
	}
	foot = append(foot, hint)

	lines := make([]string, 0, avail+len(foot))
	m.rowField = m.rowField[:0]
	for _, r := range rows[m.offset:min(len(rows), m.offset+avail)] {
		lines = append(lines, r.text)
		m.rowField = append(m.rowField, r.field)
	}
	lines = append(lines, foot...)
	return overlay.Frame(env.Styles, g, m.Title, lines, inner)
}

// scrollTo moves the offset so the cursor's field rows show, its first
// row (and the section heading above it) first when they do not all fit;
// in insert mode the row with the input's cursor shows in any case.
func (m *Model) scrollTo(rows []row, avail int) {
	first, last, caret := -1, -1, -1
	for i, r := range rows {
		if r.field == m.cursor {
			if first < 0 {
				first = i
			}
			last = i
		}
		if r.caret && m.editing && r.field == m.cursor {
			caret = i
		}
	}
	if first < 0 {
		return
	}
	if last >= m.offset+avail {
		m.offset = last - avail + 1
	}
	if first < m.offset {
		m.offset = first
	}
	if caret >= 0 {
		if caret < m.offset {
			m.offset = caret
		}
		if caret >= m.offset+avail {
			m.offset = caret - avail + 1
		}
	}
	m.offset = max(0, min(m.offset, len(rows)-avail))
}

// valueLines draws field i's value in w cells: the input with its cursor
// in insert mode, the placeholder for an empty value, a list's items (some
// of them while the cursor is elsewhere) with secrets masked until edited,
// a read-only value followed by why; the index of the line with the
// input's cursor, -1 when not editing.
func (m *Model) valueLines(env *ui.Env, s theme.Styles, i, w int) ([]string, int) {
	if m.editing && i == m.cursor {
		cursor := s.Text.Reverse(true)
		if s.Mono {
			cursor = lipgloss.NewStyle().Reverse(true)
		}
		return m.in.lines(s.Text, cursor, w)
	}
	return m.shownValue(env, s, i, w), -1
}

func (m *Model) shownValue(env *ui.Env, s theme.Styles, i, w int) []string {
	f := &m.Fields[i]
	value := s.Text
	if f.ReadOnly != "" {
		value = s.Text2
	}
	switch f.Kind {
	case fold:
		return []string{s.Muted.Render(cut(f.Value, w, env.Glyphs.Ellipsis))}
	case Toggle:
		if f.On {
			return []string{s.Running.Render("on")}
		}
		return []string{s.Muted.Render("off")}
	case List:
		if !f.Lines {
			break
		}
		items := f.Items()
		if len(items) == 0 {
			return []string{s.Muted.Render(placeholder(f))}
		}
		show := items
		if i != m.cursor && len(show) > listRows {
			show = show[:listRows-1]
		}
		out := make([]string, 0, len(show)+2)
		for _, it := range show {
			if f.Secret != nil && !f.revealed && f.Secret(it) {
				k, _, _ := strings.Cut(it, "=")
				out = append(out, value.Render(cut(viewfmt.PlainText(k), w-7, env.Glyphs.Ellipsis))+s.Muted.Render("="+viewfmt.SecretMask))
				continue
			}
			out = append(out, value.Render(cut(viewfmt.PlainText(it), w, env.Glyphs.Ellipsis)))
		}
		if len(show) < len(items) {
			out = append(out, s.Muted.Render(fmt.Sprintf("+%d more", len(items)-len(show))))
		}
		if f.ReadOnly != "" {
			out = append(out, s.Muted.Render(cut(f.ReadOnly, w, env.Glyphs.Ellipsis)))
		}
		return out
	}
	text := viewfmt.PlainText(f.Value)
	if f.Value == "" {
		value, text = s.Muted, placeholder(f)
	}
	if why := "  " + f.ReadOnly; f.ReadOnly != "" && w-ansi.StringWidth(why) >= 8 {
		return []string{value.Render(cut(text, w-ansi.StringWidth(why), env.Glyphs.Ellipsis)) + s.Muted.Render(why)}
	}
	return []string{value.Render(cut(text, w, env.Glyphs.Ellipsis))}
}

// fullValue is field i's whole value wrapped in w cells, for the line
// under the highlighted field, when its one-line value is cut to fit;
// none when it fits or the field shows its value on several lines.
func (m *Model) fullValue(i, w int, tail string) []string {
	f := &m.Fields[i]
	switch {
	case f.Kind == Toggle || f.Kind == fold || (f.Kind == List && f.Lines) || f.Value == "":
		return nil
	}
	room := w
	if why := "  " + f.ReadOnly; f.ReadOnly != "" && w-ansi.StringWidth(why) >= 8 {
		room -= ansi.StringWidth(why)
	}
	if ansi.StringWidth(viewfmt.PlainText(f.Value)) <= room {
		return nil
	}
	return wrap(f.Value, w, fullRows, tail)
}

func placeholder(f *Field) string {
	if f.Placeholder != "" {
		return f.Placeholder
	}
	return "none"
}

// Items are a list's items, trimmed, without blank ones.
func (f Field) Items() []string {
	sep := ","
	if f.Lines {
		sep = "\n"
	}
	var out []string
	for l := range strings.SplitSeq(f.Value, sep) {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}

// JoinItems is the Value of a list holding items.
func JoinItems(items []string, lines bool) string {
	if lines {
		return strings.Join(items, "\n")
	}
	return strings.Join(items, ", ")
}

func cut(s string, w int, tail string) string {
	if w <= 0 {
		return ""
	}
	return ansi.Truncate(s, w, tail)
}

// wrap breaks text into at most n lines of w cells, the last one cut.
func wrap(text string, w, n int, tail string) []string {
	if text == "" {
		return nil
	}
	text = viewfmt.PlainText(text)
	lines := strings.Split(lipgloss.NewStyle().Width(w).Render(text), "\n")
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], " ")
	}
	if len(lines) > n {
		lines = lines[:n]
		lines[n-1] = ansi.Truncate(lines[n-1], w-ansi.StringWidth(tail), "") + tail
	}
	return lines
}
