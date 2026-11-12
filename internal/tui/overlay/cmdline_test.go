package overlay

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/padmk/portside/internal/tui/keymap"
	"github.com/padmk/portside/internal/tui/theme"
)

func TestCmdLineKeepsTheCursorInViewWithWideText(t *testing.T) {
	c := NewCmdLine(nil, nil)
	c.Insert("restart " + strings.Repeat("漢", 20) + "x")
	line := c.View(theme.New(theme.TerminalFamily.Dark), 20)
	if w := ansi.StringWidth(line); w != 20 {
		t.Errorf("line is %d cells wide, want 20", w)
	}
	// The last typed character and the cursor after it are on screen.
	if !strings.Contains(ansi.Strip(line), "x") {
		t.Errorf("the end of the text scrolled out: %q", ansi.Strip(line))
	}
}

func TestCmdLineCompletesTheWordUnderTheCursor(t *testing.T) {
	r := keymap.Default()
	n := keymap.Names{Servers: []string{"web", "worker"}}
	c := NewCmdLine(nil, func(line string, cursor int) keymap.Completion { return r.Complete(line, cursor, n) })
	c.Insert("restart w --x")
	for range len(" --x") {
		c.Left()
	}
	c.Complete(false)
	if got := c.Value(); got != "restart web --x" {
		t.Errorf("first completion %q", got)
	}
	c.Complete(false)
	if got := c.Value(); got != "restart worker --x" {
		t.Errorf("second completion %q", got)
	}
}
