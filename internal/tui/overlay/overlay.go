// Package overlay draws what the terminal UI shows over its panes: the help
// overlay, the command line, the menus and toasts,
// and places them on the screen (design/tui.md "Interaction").
package overlay

import (
	"image/color"
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

// Styles are the styles of what an overlay draws inside its box: the
// theme's own where the panes show the terminal's background (the
// Terminal family, whose overlays are the terminal's background inside one
// accent border), else the theme's on Raised (Styles.OnRaised), so styled
// runs never punch holes into the overlay's background.
func Styles(s theme.Styles) theme.Styles {
	if _, plain := s.Palette.Surface.(lipgloss.NoColor); plain {
		return s
	}
	return s.OnRaised()
}

// pen draws inside an overlay with Styles, and its borders with the glyph
// set's lines in Accent. The border's cells carry the background around
// the box (the screen's, under a modal overlay), so the box is exactly its
// border and its interior, with no band of the interior's color around
// the lines.
type pen struct {
	s      theme.Styles
	g      theme.Glyphs
	border lipgloss.Style
}

func newPen(s theme.Styles, g theme.Glyphs) pen {
	return pen{s: Styles(s), g: g, border: edge(s, s.Screen)}
}

// edge is the border's style over the background of around.
func edge(s theme.Styles, around lipgloss.Style) lipgloss.Style {
	return lipgloss.NewStyle().Foreground(s.Palette.Accent).Background(around.GetBackground())
}

// selected is OverlaySelected, for a highlighted run that is not a whole
// row (a dialog's button); rows use Highlight.
func (p pen) selected() lipgloss.Style { return p.s.OverlaySelected }

// Highlight draws text as the highlighted row of a list inside an overlay,
// w cells wide (cut or padded): the text's own colors give way to
// OverlaySelected's foreground on its background (reverse and bold when
// Mono). Every selected row of an overlay is drawn this way.
func Highlight(s theme.Styles, text string, w int) string {
	sel := s.OverlaySelected
	return theme.Fill(sel, sel.Render(ansi.Strip(text)), w)
}

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
		// The title lies on the border, over the border's background.
		head = p.border.Render(" ") + p.border.Foreground(p.s.Palette.AccentText).Bold(true).Render(title) + p.border.Render(" ")
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
// with Styles(s).
func Frame(s theme.Styles, g theme.Glyphs, title string, lines []string, inner int) string {
	return newPen(s, g).box(title, lines, inner)
}

// Hint is the closing line of the help and messages overlays: key and
// word pairs separated by the glyph set's separator.
func Hint(s theme.Styles, g theme.Glyphs, pairs ...[2]string) string {
	r := Styles(s)
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
// of its own colors and drawn in a dim foreground on the screen's
// background, w cells wide, so the overlay stands out and nothing behind
// it competes for attention. A theme with its own background gets Dim's
// color mixed halfway toward that background, since many terminals draw
// the faint attribute of an exact color at full strength; the Terminal
// family keeps Dim's ANSI color with the faint attribute, and without
// colors only the faint attribute remains, which keeps the text legible.
func Dimmed(s theme.Styles, screen string, w int) string {
	bg := s.Screen.GetBackground()
	fg := s.Dim.GetForeground()
	dim := lipgloss.NewStyle().Background(bg)
	if mixed, ok := mix(fg, bg); ok {
		dim = dim.Foreground(mixed)
	} else {
		dim = dim.Foreground(fg).Faint(true)
	}
	rows := strings.Split(screen, "\n")
	for i, r := range rows {
		r = ansi.Strip(r)
		if rw := ansi.StringWidth(r); rw > w {
			r = ansi.Truncate(r, w, "")
		} else {
			r += strings.Repeat(" ", w-rw)
		}
		rows[i] = dim.Render(r)
	}
	return strings.Join(rows, "\n")
}

// mix is the color halfway between two exact colors; false when either is
// an ANSI color or none, whose value the terminal decides.
func mix(a, b color.Color) (color.Color, bool) {
	exact := func(c color.Color) bool {
		switch c.(type) {
		case nil, lipgloss.NoColor, ansi.BasicColor, ansi.IndexedColor:
			return false
		}
		return true
	}
	if !exact(a) || !exact(b) {
		return nil, false
	}
	ar, ag, ab, _ := a.RGBA()
	br, bg, bb, _ := b.RGBA()
	half := func(x, y uint32) uint8 { return uint8((x + y) / 2 >> 8) }
	return color.RGBA{half(ar, br), half(ag, bg), half(ab, bb), 255}, true
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
