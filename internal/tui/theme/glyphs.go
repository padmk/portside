package theme

import (
	"charm.land/lipgloss/v2"

	"github.com/padmk/portside/internal/supervisor"
)

// Glyphs is one glyph set (design/tui.md "Glyphs"). Every state has its
// own shape, so state never depends on color alone.
type Glyphs struct {
	ID string // "unicode", "nerd" or "ascii"

	Running, Unready, Crashed, Stopped, Done, External, Disabled string
	// Stale marks a live server whose config changed since it started (a
	// restart applies it), next to its lamp like Disabled.
	Stale   string
	Spinner []string // frames for starting, stopping and pending actions

	Expanded, Collapsed string // group arrows
	Logo                string // the top bar's mark

	// Kind is the icon before a server's name by discover.CommandKind
	// (supervisor.View.Kind); empty in sets without tool icons.
	Kind map[string]string
	// Badge icons; empty strings fall back to the badge's word.
	Task, Schedule, Watch, Worktree, Network string

	// Chrome: everything drawn around and between the content.
	Cursor    string          // marks the selected row, so selection never depends on color
	Caret     string          // the text cursor at the end of an input line
	Border    lipgloss.Border // panes, overlays
	Heavy     lipgloss.Border // the focused pane when color cannot mark it (PaneBorder)
	Rule      string          // one cell of a horizontal rule (log markers, chart baselines)
	Ellipsis  string          // the tail of cut text
	Separator string          // between inline values, e.g. "exit 1 · 2m ago"
	Spark     Plot            // the servers list's CPU sparkline
	Chart     Plot            // the metrics charts
}

// PaneBorder is the border of a pane: Heavy for the focused pane when
// colors are off (mono) or in the ASCII set, whose terminals are often
// basic, else Border.
func (g Glyphs) PaneBorder(focused, mono bool) lipgloss.Border {
	if focused && (mono || g.ID == ASCII.ID) {
		return g.Heavy
	}
	return g.Border
}

// Plot is the alphabet a sparkline or chart draws with: Braille or Levels.
type Plot interface{ plot() }

// Braille packs 2x4 dots into a cell: two samples per cell, four levels
// per row of cells. Blank is U+2800.
type Braille struct{}

// Levels draws one sample per cell: Steps lists the glyphs from the lowest
// level to the highest, Blank is a cell with no value. In a chart of
// several rows the cells under a column's top are Steps' last glyph.
type Levels struct {
	Blank rune
	Steps []rune
}

// CPUFloor is the lowest top of a CPU plot, in percent of a core: the
// servers list's sparklines and the Metrics chart scale to at least it,
// so a process idling at 1 percent draws a low line, not a full one.
const CPUFloor = 5.0

func (Braille) plot() {}
func (Levels) plot()  {}

// Step returns the glyph for value v in [0, 1].
func (l Levels) Step(v float64) rune {
	if !(v > 0) { // also NaN
		return l.Steps[0]
	}
	i := int(v * float64(len(l.Steps)))
	return l.Steps[min(i, len(l.Steps)-1)]
}

// asciiBorder and asciiHeavy draw panes with ASCII only.
var (
	asciiBorder = lipgloss.ASCIIBorder()
	asciiHeavy  = lipgloss.Border{Top: "=", Bottom: "=", Left: "|", Right: "|", TopLeft: "+", TopRight: "+", BottomLeft: "+", BottomRight: "+", MiddleLeft: "+", MiddleRight: "+", Middle: "+", MiddleTop: "+", MiddleBottom: "+"}
)

// State returns the glyph for a display state; frame picks the spinner frame
// for states in progress.
func (g Glyphs) State(d supervisor.Display, frame int) string {
	switch d {
	case supervisor.Display(supervisor.Running):
		return g.Running
	case supervisor.DisplayUnready:
		return g.Unready
	case supervisor.Display(supervisor.Crashed), supervisor.Display(supervisor.Blocked):
		return g.Crashed
	case supervisor.DisplayDone:
		return g.Done
	case supervisor.DisplayExternal:
		return g.External
	case supervisor.Display(supervisor.Starting), supervisor.Display(supervisor.Stopping):
		return g.Spinner[frame%len(g.Spinner)]
	}
	return g.Stopped
}

// GlyphSets lists the sets in the order `--icons` documents them.
func GlyphSets() []Glyphs { return []Glyphs{Unicode, Nerd, ASCII} }

var Unicode = Glyphs{
	ID:      "unicode",
	Running: "●", Unready: "◍", Crashed: "◆", Stopped: "○", Done: "✓", External: "◎", Disabled: "⊘", Stale: "↻",
	Spinner:  []string{"◐", "◓", "◑", "◒"},
	Expanded: "▾", Collapsed: "▸", Logo: "⚓",
	Cursor: "▎", Caret: "▏", Border: lipgloss.RoundedBorder(), Heavy: lipgloss.ThickBorder(),
	Rule: "─", Ellipsis: "…", Separator: " · ", Spark: Braille{}, Chart: Braille{},
}

var ASCII = Glyphs{
	ID:      "ascii",
	Running: "*", Unready: "?", Crashed: "!", Stopped: "-", Done: "+", External: "@", Disabled: "x", Stale: "^",
	Spinner:  []string{"|", "/", "-", "\\"},
	Expanded: "v", Collapsed: ">", Logo: "#",
	Cursor: ">", Caret: "_", Border: asciiBorder, Heavy: asciiHeavy,
	Rule: "-", Ellipsis: "~", Separator: ", ",
	Spark: Levels{Blank: ' ', Steps: []rune("_.-=#")}, Chart: Levels{Blank: ' ', Steps: []rune("_.-=#")},
}

// Nerd needs a Nerd Font (v3 code points).
var Nerd = Glyphs{
	ID:      "nerd",
	Running: "\uf111", Unready: "\U000f0026", Crashed: "\uf071", Stopped: "\uf10c", Done: "\uf058", External: "\U000f059f", Disabled: "\U000f073a", Stale: "\uf021",
	Spinner:  []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"},
	Expanded: "\uf078", Collapsed: "\uf054", Logo: "\U000f0dd4",
	Kind: nerdKinds,
	Task: "\uf0ae", Schedule: "\uf017", Watch: "\uf06e", Worktree: "\ue725", Network: "\U000f0317",
	Cursor: "▎", Caret: "▏", Border: lipgloss.RoundedBorder(), Heavy: lipgloss.ThickBorder(),
	Rule: "─", Ellipsis: "…", Separator: " · ", Spark: Braille{}, Chart: Braille{},
}
