package overlay

import (
	"fmt"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/padmk/portside/internal/tui/theme"
)

// TestLongMenuScrollsAndHitsTheShownItem checks that a menu taller than
// its bound keeps the cursor on screen, says what is hidden, and that a
// click on a shown row picks that row's item.
func TestLongMenuScrollsAndHitsTheShownItem(t *testing.T) {
	m := Menu{Title: "web", MaxHeight: 10}
	for i := range 30 {
		section := "Server"
		if i >= 20 {
			section = "Daemon"
		}
		m.Items = append(m.Items, MenuItem{ActionID: fmt.Sprint(i), Label: fmt.Sprintf("item %02d", i), Key: "k", Section: section})
	}
	s, g := theme.New(theme.TerminalFamily.Dark), theme.Unicode
	for step := range 30 {
		_, h := m.Size()
		if h > m.MaxHeight {
			t.Fatalf("step %d: the menu is %d rows, more than its bound %d", step, h, m.MaxHeight)
		}
		rows := strings.Split(ansi.Strip(m.View(s, g)), "\n")
		if len(rows) != h {
			t.Fatalf("step %d: the view has %d rows, Size says %d", step, len(rows), h)
		}
		want := fmt.Sprintf("item %02d", m.Cursor)
		found := false
		for y, r := range rows {
			if strings.Contains(r, want) {
				found = true
				if got := m.Hit(y); got != m.Cursor {
					t.Errorf("step %d: a click on %q picks item %d", step, want, got)
				}
			}
		}
		if !found {
			t.Fatalf("step %d: the cursor's %s is off screen:\n%s", step, want, strings.Join(rows, "\n"))
		}
		view := strings.Join(rows, "\n")
		if !strings.Contains(view, "item 00") && !strings.Contains(rows[1], "↑") {
			t.Errorf("step %d: no hint for the items above: %q", step, rows[1])
		}
		if !strings.Contains(view, "item 29") && !strings.Contains(rows[len(rows)-2], "↓") {
			t.Errorf("step %d: no hint for the items below: %q", step, rows[len(rows)-2])
		}
		m.Down()
	}
	// Down from the last item wraps to the top, with its section header.
	if m.Cursor != 0 || !strings.Contains(ansi.Strip(m.View(s, g)), "Server") {
		t.Errorf("after wrapping: cursor %d, view without its header", m.Cursor)
	}
	if m.Hit(1) != -1 {
		t.Error("a click on a section header picks an item")
	}
}

// TestBoxBorderCarriesTheSurroundingBackground checks that an overlay's
// border cells are drawn on the screen's background, not the interior's,
// so no band of the interior's color shows around the lines.
func TestBoxBorderCarriesTheSurroundingBackground(t *testing.T) {
	f, _ := theme.Lookup("nord")
	s := theme.New(f.Dark)
	box := Frame(s, theme.Unicode, "web", []string{"x"}, 10)
	top := strings.Split(box, "\n")[0]
	raised := lipgloss.NewStyle().Background(f.Dark.Raised).Render(" ")
	if bg := raised[:strings.IndexByte(raised, ' ')]; strings.Contains(top, strings.TrimPrefix(bg, "\x1b[")) {
		t.Errorf("the top border carries the interior's background: %q", top)
	}
}
