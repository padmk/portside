package overlay

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/padmk/portside/internal/tui/theme"
)

// TestPaletteSaysWhichMatchesItShows checks that the palette counts the
// matching entries, not the whole catalog, and follows the cursor.
func TestPaletteSaysWhichMatchesItShows(t *testing.T) {
	var items []PaletteItem
	for i := range 30 {
		items = append(items, PaletteItem{Label: fmt.Sprintf("Restart s%02d", i)}, PaletteItem{Label: fmt.Sprintf("Open o%02d", i)})
	}
	p := NewPalette(items)
	p.Insert("restart")
	s := theme.New(theme.TerminalFamily.Dark)
	if view := ansi.Strip(p.View(s, theme.Unicode, false, 120, 40)); !strings.Contains(view, " 1-14 of 30 ") {
		t.Errorf("no window of the 30 matches in\n%s", view)
	}
	p.Up() // wraps to the last match
	if view := ansi.Strip(p.View(s, theme.Unicode, false, 120, 40)); !strings.Contains(view, " 17-30 of 30 ") {
		t.Errorf("the window does not follow the cursor to the end:\n%s", view)
	}
}
