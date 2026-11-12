package theme

import (
	"regexp"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// resets matches the SGR sequences that end a styled segment.
var resets = regexp.MustCompile(`\x1b\[0?m`)

// Row fits line to width cells on Surface: text and padding alike, and the
// spaces between styled segments, which a reset would otherwise leave on
// the terminal's background.
func (s Styles) Row(line string, width int) string { return Fill(s.Surface, line, width) }

// SelectedRow is Row for the selected row: on Raised, or reversed when Mono.
// Draw the row's segments with OnRaised styles, whose Muted stays visible
// on Raised.
func (s Styles) SelectedRow(line string, width int) string { return Fill(s.Selected, line, width) }

// Fill fits line to width cells (cut, or padded with spaces) with base's
// background and attributes under all of it: they are set at the start and
// again after every reset inside line. Foreground colors inside line win.
func Fill(base lipgloss.Style, line string, width int) string {
	if width <= 0 {
		return ""
	}
	if w := ansi.StringWidth(line); w > width {
		line = ansi.Truncate(line, width, "")
	} else {
		line += strings.Repeat(" ", width-w)
	}
	on := prefix(base)
	if on == "" {
		return line
	}
	return on + resets.ReplaceAllLiteralString(line, "\x1b[m"+on) + "\x1b[m"
}

// prefix is the SGR sequence that turns on base's background and
// attributes, or "" when it has none.
func prefix(base lipgloss.Style) string {
	b := lipgloss.NewStyle().Background(base.GetBackground()).Reverse(base.GetReverse()).
		Bold(base.GetBold()).Underline(base.GetUnderline())
	out := b.Render(" ")
	at := strings.IndexByte(out, ' ')
	if at <= 0 {
		return ""
	}
	return out[:at]
}
