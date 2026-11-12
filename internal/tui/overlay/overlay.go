// Package overlay draws what the terminal UI shows over its panes: the help
// overlay, the command line, the menus and toasts,
// and places them on the screen (design/tui.md "Interaction").
package overlay

import (
	"slices"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/padmk/portside/internal/tui/theme"
)

// KeyLabel is the display form of a key as the keymap writes it: "ctrl+d"
// is "^d", "space" is "␣", "enter" is "↵"; ascii keeps to ASCII.
func KeyLabel(key string, ascii bool) string { return keyName(key, ascii) }

func keyName(k string, ascii bool) string {
	switch {
	case strings.HasPrefix(k, "ctrl+"):
		return "^" + keyName(strings.TrimPrefix(k, "ctrl+"), ascii)
	case strings.HasPrefix(k, "shift+"):
		if ascii {
			return "S-" + keyName(strings.TrimPrefix(k, "shift+"), ascii)
		}
		return "⇧" + keyName(strings.TrimPrefix(k, "shift+"), ascii)
	case strings.HasPrefix(k, "alt+"):
		if ascii {
			return "M-" + keyName(strings.TrimPrefix(k, "alt+"), ascii)
		}
		return "⌥" + keyName(strings.TrimPrefix(k, "alt+"), ascii)
	}
	switch k {
	case "pgup":
		return "PgUp"
	case "pgdown":
		return "PgDn"
	case "home":
		return "Home"
	case "end":
		return "End"
	}
	if ascii {
		if k == "space" {
			return "spc"
		}
		return k
	}
	switch k {
	case "space":
		return "␣"
	case "enter":
		return "↵"
	case "up":
		return "↑"
	case "down":
		return "↓"
	case "left":
		return "←"
	case "right":
		return "→"
	}
	return k
}

// pen draws inside an overlay: its styles are the theme's on Raised
// (Styles.OnRaised), so styled runs never punch holes into the overlay's
// background, and its borders come from the glyph set.
type pen struct {
	s      theme.Styles
	g      theme.Glyphs
	border lipgloss.Style
}

func newPen(s theme.Styles, g theme.Glyphs) pen {
	r := s.OnRaised()
	return pen{s: r, g: g, border: r.FrameFocused}
}

// selected is the style of the highlighted row in a list inside an
// overlay (theme.Styles.OverlaySelected), since the Raised background of
// a selected pane row is the overlay's own.
func (p pen) selected() lipgloss.Style { return p.s.OverlaySelected }

// Highlight is the highlighted row's style inside an overlay drawn with
// s.
func Highlight(s theme.Styles) lipgloss.Style { return s.OverlaySelected }

func (p pen) space(n int) string {
	if n <= 0 {
		return ""
	}
	return p.s.Surface.Render(strings.Repeat(" ", n))
}

// fit cuts or pads line to exactly w cells on the overlay's background.
func (p pen) fit(line string, w int) string { return p.s.Row(line, w) }

// box frames lines inner cells wide: the glyph set's border with the title
// in the top border and one column of padding, inner+4 cells wide in
// total.
func (p pen) box(title string, lines []string, inner int) string {
	b := p.g.Border
	w := inner + 2
	head := ""
	if title != "" {
		head = p.space(1) + p.s.AccentBold.Render(title) + p.space(1)
		if ansi.StringWidth(head) > w-1 {
			head = ansi.Truncate(head, w-1, "")
		}
	}
	out := make([]string, 0, len(lines)+2)
	out = append(out, p.border.Render(b.TopLeft)+head+p.border.Render(strings.Repeat(b.Top, w-ansi.StringWidth(head))+b.TopRight))
	left, right := p.border.Render(b.Left), p.border.Render(b.Right)
	for _, l := range lines {
		out = append(out, left+p.space(1)+p.fit(l, inner)+p.space(1)+right)
	}
	out = append(out, p.border.Render(b.BottomLeft+strings.Repeat(b.Bottom, w)+b.BottomRight))
	return strings.Join(out, "\n")
}

// Frame draws lines inner cells wide in an overlay box with the title in
// its top border, as the help and the menus are drawn; lines are drawn
// with styles from s.OnRaised().
func Frame(s theme.Styles, g theme.Glyphs, title string, lines []string, inner int) string {
	return newPen(s, g).box(title, lines, inner)
}

// Hint is the closing line of the help and messages overlays: key and
// word pairs separated by the glyph set's separator.
func Hint(s theme.Styles, g theme.Glyphs, pairs ...[2]string) string {
	r := s.OnRaised()
	parts := make([]string, len(pairs))
	for i, p := range pairs {
		parts[i] = r.Key.Render(p[0]) + r.Surface.Render(" ") + r.Muted.Render(p[1])
	}
	return strings.Join(parts, r.Muted.Render(g.Separator))
}

// HintFit is Hint in at most w cells: the first pair (moving, which
// every list does alike) goes first, then the pairs before the last, so
// the way out stays.
func HintFit(s theme.Styles, g theme.Glyphs, w int, pairs ...[2]string) string {
	pairs = slices.Clone(pairs)
	first := true
	for len(pairs) > 1 && ansi.StringWidth(Hint(s, g, pairs...)) > w {
		if first || len(pairs) == 2 {
			pairs, first = pairs[1:], false
			continue
		}
		pairs = slices.Delete(pairs, len(pairs)-2, len(pairs)-1)
	}
	return Hint(s, g, pairs...)
}

// Place draws box over base with its top left corner at column x and row
// y, moved so it stays inside base. Each base row keeps its width and base
// keeps its row count; both may hold ANSI styles.
func Place(base, box string, x, y int) string {
	rows := strings.Split(base, "\n")
	boxRows := strings.Split(box, "\n")
	baseW := 0
	for _, r := range rows {
		baseW = max(baseW, ansi.StringWidth(r))
	}
	bw := 0
	for _, r := range boxRows {
		bw = max(bw, ansi.StringWidth(r))
	}
	bw = min(bw, baseW)
	if len(boxRows) > len(rows) {
		boxRows = boxRows[:len(rows)]
	}
	x = max(0, min(x, baseW-bw))
	y = max(0, min(y, len(rows)-len(boxRows)))
	for i, b := range boxRows {
		row := rows[y+i]
		rw := ansi.StringWidth(row)
		if rw < x+bw {
			row += strings.Repeat(" ", x+bw-rw)
		}
		left := ansi.Truncate(row, x, "")
		left += strings.Repeat(" ", x-ansi.StringWidth(left))
		if ansi.StringWidth(b) > bw {
			b = ansi.Truncate(b, bw, "")
		}
		b += strings.Repeat(" ", bw-ansi.StringWidth(b))
		out := left + "\x1b[m" + b + "\x1b[m"
		if rw > x+bw {
			right := ansi.TruncateLeft(row, x+bw, "")
			out += strings.Repeat(" ", rw-x-bw-ansi.StringWidth(right)) + right
		}
		rows[y+i] = out
	}
	return strings.Join(rows, "\n")
}

// Dimmed is screen as it lies under a modal overlay: every row stripped
// of its own colors and drawn in Dim (muted, faint) on the screen's
// background, w cells wide, so the overlay stands out and nothing behind
// it competes for attention.
func Dimmed(s theme.Styles, screen string, w int) string {
	rows := strings.Split(screen, "\n")
	for i, r := range rows {
		rows[i] = theme.Fill(s.Screen, s.Dim.Render(ansi.Strip(r)), w)
	}
	return strings.Join(rows, "\n")
}

// Center draws box in the middle of base.
func Center(base, box string) string {
	baseW, boxW := 0, 0
	rows := strings.Split(base, "\n")
	for _, r := range rows {
		baseW = max(baseW, ansi.StringWidth(r))
	}
	boxRows := strings.Split(box, "\n")
	for _, r := range boxRows {
		boxW = max(boxW, ansi.StringWidth(r))
	}
	return Place(base, box, (baseW-boxW)/2, (len(rows)-len(boxRows))/2)
}
