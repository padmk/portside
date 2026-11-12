package logs

import (
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/charmbracelet/x/ansi"

	"github.com/padmk/portside/internal/supervisor"
)

// Line is one log line.
type Line struct {
	At    time.Time
	Text  string
	Plain string
	Color bool
	// Rule is set for the daemon's start, stop and crash markers.
	Rule *Rule
}

var escape = supervisor.ANSIPattern
var sgr = regexp.MustCompile(`^\x1b\[[0-9:;]*m$`)

// tabWidth is the distance between tab stops.
const tabWidth = 8

// Clean removes terminal controls except colors and hyperlinks: the
// line's cursor redraws are replayed by supervisor.RedrawLine, tabs become
// spaces up to the next tab stop and every other control character is
// dropped, so the text holds no control character outside the SGR and
// hyperlink sequences it keeps.
func Clean(raw string, at time.Time) Line {
	raw = supervisor.RedrawLine(raw, keepSequence)
	var out strings.Builder
	color := false
	for len(raw) > 0 {
		loc := escape.FindStringIndex(raw)
		part := raw
		if loc != nil {
			part = raw[:loc[0]]
		}
		for _, r := range part {
			if r == '\t' {
				col := ansi.StringWidth(out.String())
				out.WriteString(strings.Repeat(" ", tabWidth-col%tabWidth))
			} else if !unicode.IsControl(r) {
				out.WriteRune(r)
			}
		}
		if loc == nil {
			break
		}
		seq := raw[loc[0]:loc[1]]
		if keepSequence(seq) {
			out.WriteString(seq)
			color = color || sgr.MatchString(seq)
		}
		raw = raw[loc[1]:]
	}
	text := out.String()
	plain := ansi.Strip(text)
	return Line{At: at, Text: text, Plain: plain, Color: color, Rule: parseRule(plain)}
}

// keepSequence reports whether Clean keeps an escape sequence: colors and
// safe hyperlinks.
func keepSequence(seq string) bool {
	return sgr.MatchString(seq) || strings.HasPrefix(seq, "\x1b]8;") && safeLink(seq)
}

// safeLink reports whether a hyperlink sequence holds no control
// character between its introducer and its terminator.
func safeLink(seq string) bool {
	body := strings.TrimPrefix(seq, "\x1b]")
	body = strings.TrimSuffix(strings.TrimSuffix(body, "\x1b\\"), "\x07")
	return strings.IndexFunc(body, unicode.IsControl) < 0
}

// Buffer keeps at most Max lines, dropping the oldest. It is a ring, so
// appending to a full buffer costs the same as to an empty one.
type Buffer struct {
	Max   int
	lines []Line
	start int // index of the oldest line in lines
	n     int // lines kept
}

func NewBuffer(max int) *Buffer { return &Buffer{Max: max} }

// Append adds lines at the end; past Max the oldest go.
func (b *Buffer) Append(lines ...Line) {
	for _, l := range lines {
		if b.Max <= 0 {
			continue
		}
		if b.n < len(b.lines) {
			b.lines[b.index(b.n)] = l
			b.n++
			continue
		}
		if len(b.lines) < b.Max {
			b.lines = append(b.lines, l)
			b.n++
			continue
		}
		b.lines[b.start] = l
		b.start = (b.start + 1) % len(b.lines)
	}
}

// Extend replaces the last line.
func (b *Buffer) Extend(l Line) {
	if b.n == 0 {
		b.Append(l)
	} else {
		b.lines[b.index(b.n-1)] = l
	}
}

// Set replaces the line at index i.
func (b *Buffer) Set(i int, l Line) { b.lines[b.index(i)] = l }

// Pop removes the last line.
func (b *Buffer) Pop() {
	if b.n > 0 {
		b.n--
		b.lines[b.index(b.n)] = Line{}
	}
}

func (b *Buffer) Len() int      { return b.n }
func (b *Buffer) At(i int) Line { return b.lines[b.index(i)] }
func (b *Buffer) Reset()        { b.lines, b.start, b.n = nil, 0, 0 }

func (b *Buffer) index(i int) int { return (b.start + i) % len(b.lines) }

// Rows renders a line within width cells, including its gutter: wrapped,
// or cut from cell offset left with ellipsis marking text cut off on
// either side.
func Rows(text, gutter string, width int, wrap bool, left int, ellipsis string) []string {
	if width <= 0 {
		return []string{""}
	}
	gutter = ansi.Truncate(gutter, width, "")
	gw := ansi.StringWidth(gutter)
	available := width - gw
	if available == 0 {
		return []string{gutter + strings.Repeat(" ", width-gw)}
	}
	var parts []string
	if wrap {
		parts = strings.Split(ansi.Wrap(text, available, ""), "\n")
	} else {
		parts = []string{cutRow(text, available, left, ellipsis)}
	}
	rows := make([]string, 0, len(parts))
	for i, part := range parts {
		prefix := gutter
		if i > 0 {
			prefix = strings.Repeat(" ", gw)
		}
		rows = append(rows, prefix+part+strings.Repeat(" ", max(0, available-ansi.StringWidth(part))))
	}
	return rows
}

// cutRow is the part of text from cell left that fits in width cells.
func cutRow(text string, width, left int, ellipsis string) string {
	if left <= 0 {
		return ansi.Truncate(text, width, ellipsis)
	}
	total := ansi.StringWidth(text)
	if left >= total {
		return ansi.Truncate(ellipsis, width, "")
	}
	ew := ansi.StringWidth(ellipsis)
	if width <= ew {
		return ansi.Truncate(ellipsis, width, "")
	}
	rest := ansi.Cut(text, left+ew, total)
	return ellipsis + ansi.Truncate(rest, width-ew, ellipsis)
}
