package remote

import (
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/padmk/portside/internal/tui/overlay"
	"github.com/padmk/portside/internal/tui/theme"
	"github.com/padmk/portside/internal/tui/ui"
)

// doubleClick is how close two clicks on one row are to run its enter.
const doubleClick = 400 * time.Millisecond

// span is a clickable part of a content line, from x to end (cells,
// exclusive), that runs key.
type span struct {
	x, end int
	key    string
}

// lineClick is what a click on a content line does: it puts the cursor on
// row (-1 for a line that is no row) and runs the key of the span under
// the pointer.
type lineClick struct {
	row   int
	spans []span
}

// clicks collects an overlay's lineClick per content line as its lines
// are drawn.
type clicks []lineClick

func (c *clicks) line(row int) { *c = append(*c, lineClick{row: row}) }
func (c *clicks) on(x, end int, key string) {
	if n := len(*c); n > 0 && end > x {
		(*c)[n-1].spans = append((*c)[n-1].spans, span{x, end, key})
	}
}

// actionLine draws key and label pairs as the overlay's clickable actions
// (as overlay.Hint draws them) and records each pair's span.
func actionLine(s theme.Styles, g theme.Glyphs, cl *clicks, pairs [][2]string) string {
	r := overlay.Styles(s)
	out, x := "", 0
	cl.line(-1)
	for i, p := range pairs {
		if i > 0 {
			sep := r.Muted.Render(g.Separator)
			out += sep
			x += ansi.StringWidth(sep)
		}
		part := r.Key.Render(overlay.KeyLabel(p[0], g.ID == theme.ASCII.ID)) + r.Surface.Render(" ") + r.Text.Render(p[1])
		w := ansi.StringWidth(part)
		cl.on(x, x+w, p[0])
		out += part
		x += w
	}
	return out
}

// geometry is where the last frame drew the open overlay: its first
// content cell on the screen, and the content lines shown from off on.
type geometry struct {
	x, y, off, n int
}

// keyPress is the key a click runs, as the keyboard sends it.
func keyPress(k string) tea.KeyPressMsg {
	switch k {
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "space":
		return tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}
	}
	r := []rune(k)
	return tea.KeyPressMsg{Code: r[0], Text: k}
}

// Mouse scrolls the open overlay with the wheel; a click on a row puts
// the cursor on it (twice in a row on the Computers overlay runs its
// enter), and a click on an action, the sharing line, a port's
// forwarding or an agent's check box runs it as its key does.
func (h *Host) Mouse(msg tea.MouseMsg) tea.Cmd {
	if w, ok := msg.(tea.MouseWheelMsg); ok {
		d := map[tea.MouseButton]int{tea.MouseWheelDown: 1, tea.MouseWheelUp: -1}[w.Mouse().Button]
		switch h.open {
		case viewComputers:
			h.comp.move(d)
		case viewAgents:
			h.agents.cursor = max(0, min(h.agents.cursor+d, len(h.agents.items)-1))
		default:
			h.scroll = max(0, h.scroll+d)
		}
		return nil
	}
	click, ok := msg.(tea.MouseClickMsg)
	if !ok || click.Mouse().Button != tea.MouseLeft || h.env == nil {
		return nil
	}
	m := click.Mouse()
	x, y := m.X-h.geo.x, m.Y-h.geo.y
	var cl clicks
	switch h.open {
	case viewComputers:
		cl = h.comp.clicks
	case viewAgents:
		cl = h.agents.clicks
	case viewDaemon:
		cl = h.daemonClicks
	}
	i := h.geo.off + y
	if y < 0 || y >= h.geo.n || i >= len(cl) || x < 0 {
		return nil
	}
	lc := cl[i]
	key := ""
	for _, sp := range lc.spans {
		if x >= sp.x && x < sp.end {
			key = sp.key
		}
	}
	if lc.row >= 0 {
		now := h.env.Now()
		switch h.open {
		case viewComputers:
			again := h.comp.cursor == lc.row && h.lastRow == lc.row && now.Sub(h.lastClick) < doubleClick
			h.comp.cursor = lc.row
			if again && key == "" {
				key = "enter"
			}
		case viewAgents:
			h.agents.cursor = lc.row
		}
		h.lastRow, h.lastClick = lc.row, now
		if key == "enter" {
			h.lastRow = -1 // a third click starts again
		}
	}
	if key == "" {
		return nil
	}
	return h.Key(h.env, keyPress(key))
}

// placed records where View drew the overlay: content lines shown from
// off, n of them, in a box inner cells wide centered on a w x hgt screen.
func (h *Host) placed(env *ui.Env, w, hgt, inner, off, n int) {
	h.env = env
	h.geo = geometry{x: (w-(inner+4))/2 + 2, y: (hgt-(n+2))/2 + 1, off: off, n: n}
}
