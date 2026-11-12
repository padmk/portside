package overlay

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/padmk/portside/internal/tui/theme"
)

// Dialog is a confirmation: a title, the consequence and two choices,
// Cancel (the default) and the destructive one. Choice is the selected
// one: 0 Cancel, 1 the confirmation.
type Dialog struct {
	Title, Body, Confirm string
	Choice               int
	// buttons holds each choice's columns in the last View's bottom row,
	// relative to the box.
	buttons [2][2]int
	height  int
}

// Toggle selects the other choice.
func (d *Dialog) Toggle() { d.Choice = 1 - d.Choice }

// View renders the dialog at most 56 cells wide for a screen w cells wide:
// the title, the body wrapped, and the two choices, the selected one
// highlighted and both with their keys (n, y).
func (d *Dialog) View(s theme.Styles, g theme.Glyphs, w int) string {
	pn := newPen(s, g)
	inner := max(16, min(52, w-6))
	label := d.Confirm
	if label == "" {
		label = "Confirm"
	}
	var lines []string
	if d.Body != "" {
		body := lipgloss.NewStyle().Width(inner).Render(d.Body)
		for l := range strings.SplitSeq(body, "\n") {
			lines = append(lines, pn.s.Text.Render(strings.TrimRight(l, " ")))
		}
		lines = append(lines, "")
	}
	button := func(i int, key, text string, danger bool) string {
		mark := "  "
		st := pn.s.Text
		if danger {
			st = pn.s.Error
		}
		if d.Choice == i {
			mark = g.Cursor + " "
			st = pn.selected()
		}
		return st.Render(mark+text+" ") + pn.s.Key.Render(key)
	}
	cancel := button(0, "n", "Cancel", false)
	ok := button(1, "y", label, true)
	gap := pn.space(3)
	row := cancel + gap + ok
	// The box's content starts after the border and one column of padding.
	cw := ansi.StringWidth(cancel)
	d.buttons[0] = [2]int{2, 2 + cw}
	d.buttons[1] = [2]int{2 + cw + 3, 2 + cw + 3 + ansi.StringWidth(ok)}
	lines = append(lines, row)
	d.height = len(lines) + 2
	return pn.box(d.Title, lines, inner)
}

// Hit returns the choice at column x of the box's bottom content row, -1
// for anything else; y is the row in the box (0 is the top border).
func (d *Dialog) Hit(x, y int) int {
	if y != d.height-2 {
		return -1
	}
	for i, b := range d.buttons {
		if x >= b[0] && x < b[1] {
			return i
		}
	}
	return -1
}
