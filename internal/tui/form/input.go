package form

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/padmk/portside/internal/viewfmt"
)

// input is the text being edited in a field: runes with a cursor; lines
// separated by '\n' when the field takes one item per line.
type input struct {
	text   []rune
	cursor int
}

func newInput(s string) input { r := []rune(s); return input{text: r, cursor: len(r)} }

func (in *input) value() string { return string(in.text) }

func (in *input) insert(s string) {
	r := []rune(s)
	out := make([]rune, 0, len(in.text)+len(r))
	out = append(out, in.text[:in.cursor]...)
	out = append(out, r...)
	out = append(out, in.text[in.cursor:]...)
	in.text = out
	in.cursor += len(r)
}

func (in *input) backspace() {
	if in.cursor == 0 {
		return
	}
	in.text = append(in.text[:in.cursor-1], in.text[in.cursor:]...)
	in.cursor--
}

func (in *input) delete() {
	if in.cursor < len(in.text) {
		in.text = append(in.text[:in.cursor], in.text[in.cursor+1:]...)
	}
}

func (in *input) left()  { in.cursor = max(0, in.cursor-1) }
func (in *input) right() { in.cursor = min(len(in.text), in.cursor+1) }

// lineStart and lineEnd are the bounds of the cursor's line.
func (in *input) lineStart() int {
	i := in.cursor
	for i > 0 && in.text[i-1] != '\n' {
		i--
	}
	return i
}

func (in *input) lineEnd() int {
	i := in.cursor
	for i < len(in.text) && in.text[i] != '\n' {
		i++
	}
	return i
}

func (in *input) home() { in.cursor = in.lineStart() }
func (in *input) end()  { in.cursor = in.lineEnd() }

// deleteWord removes the word before the cursor, as ctrl+w does in a shell.
func (in *input) deleteWord() {
	i := in.cursor
	for i > 0 && in.text[i-1] == ' ' {
		i--
	}
	for i > 0 && in.text[i-1] != ' ' && in.text[i-1] != '\n' {
		i--
	}
	in.text = append(in.text[:i], in.text[in.cursor:]...)
	in.cursor = i
}

// deleteLine removes the line up to the cursor, as ctrl+u does.
func (in *input) deleteLine() {
	i := in.lineStart()
	in.text = append(in.text[:i], in.text[in.cursor:]...)
	in.cursor = i
}

// vertical moves the cursor to the line above (-1) or below (+1), keeping
// its column where the line is long enough.
func (in *input) vertical(d int) {
	col := in.cursor - in.lineStart()
	switch {
	case d < 0:
		start := in.lineStart()
		if start == 0 {
			return
		}
		in.cursor = start - 1
		in.cursor = min(in.lineStart()+col, in.lineEnd())
	case d > 0:
		end := in.lineEnd()
		if end == len(in.text) {
			return
		}
		in.cursor = end + 1
		in.cursor = min(in.lineStart()+col, in.lineEnd())
	}
}

// lines renders the text as lines with the cursor drawn in cursor style,
// each cut to w cells around the cursor, and the index of the line that
// holds the cursor. The runes are drawn as safe text (viewfmt.PlainText)
// and measured in terminal cells; the text itself stays as typed.
func (in *input) lines(text, cursor lipgloss.Style, w int) ([]string, int) {
	var out []string
	caret, start := 0, 0
	for i := 0; i <= len(in.text); i++ {
		if i < len(in.text) && in.text[i] != '\n' {
			continue
		}
		line := in.text[start:i]
		at := -1
		if in.cursor >= start && in.cursor <= i {
			at, caret = in.cursor-start, len(out)
		}
		out = append(out, renderLine(line, at, text, cursor, w))
		start = i + 1
	}
	return out, caret
}

// renderLine draws one line of runes with the cursor at index at (-1 for
// none), scrolled so the cursor's cell stays inside w cells: as much of
// the line before it shows as fits.
func renderLine(line []rune, at int, text, cursor lipgloss.Style, w int) string {
	cells := make([]string, len(line))
	widths := make([]int, len(line))
	for i, r := range line {
		cells[i] = viewfmt.PlainText(string(r))
		widths[i] = ansi.StringWidth(cells[i])
	}
	from := 0
	if at >= 0 {
		used := 1
		if at < len(line) {
			used = max(1, widths[at])
		}
		from = at
		for from > 0 && used+widths[from-1] <= w {
			used += widths[from-1]
			from--
		}
	}
	var b strings.Builder
	used := 0
	flush := func(seg *strings.Builder) {
		if seg.Len() > 0 {
			b.WriteString(text.Render(seg.String()))
			seg.Reset()
		}
	}
	var seg strings.Builder
	for i := from; i < len(line); i++ {
		cw := widths[i]
		if i == at {
			cw = max(1, cw)
		}
		if used+cw > w {
			break
		}
		used += cw
		if i == at {
			flush(&seg)
			b.WriteString(cursor.Render(cells[i]))
			continue
		}
		seg.WriteString(cells[i])
	}
	flush(&seg)
	if at == len(line) && used < w {
		b.WriteString(cursor.Render(" "))
	}
	return b.String()
}
