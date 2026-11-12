// Package theme holds the terminal UI's colors, glyphs and styles: the
// dashboard's theme families, generated from its CSS, plus Terminal, which
// uses the terminal's own ANSI colors.
package theme

import (
	"image/color"

	"charm.land/lipgloss/v2"
)

// Palette is one theme variant's semantic colors; design/tui.md "Themes"
// says what each is for.
type Palette struct {
	ID   string // the dashboard's theme id, e.g. "rose-pine-dawn"; "terminal-dark" for Terminal
	Dark bool
	// ANSI is set for the Terminal family, whose colors are the user's
	// ANSI palette: their contrast is unknown, so highlights are reverse
	// video and the selected row's text is Text (design/tui.md "Themes").
	ANSI bool

	Bg      color.Color // screen
	Surface color.Color // panes
	Raised  color.Color // overlays and search matches
	// Selected is the selected row's background: Accent tinted into
	// Surface as far as the row's text, labels, ports and states stay
	// readable on it.
	Selected color.Color
	Text     color.Color
	Text2    color.Color // secondary values
	Muted    color.Color // labels, unfocused borders, badges
	Border   color.Color
	Accent   color.Color // focused border, active tab and mode pill backgrounds, cursor
	// AccentText is Accent as text (key hints, the focused title, search
	// matches): Accent mixed toward Text until it reads at 4.5:1 on Bg,
	// Surface and Raised, Accent itself where it already does.
	AccentText color.Color
	AccentFg   color.Color // text on an Accent, Warn or Crashed background (banners)

	Running  color.Color
	Starting color.Color
	Crashed  color.Color
	External color.Color
	Stopped  color.Color
	Warn     color.Color
	Info     color.Color
	Port     color.Color // ports and addresses
}

// Family is a theme in its dark and light variants.
type Family struct {
	ID    string // "terminal", or the dashboard's family id ("rose-pine", "nord", ...)
	Label string
	Dark  Palette
	Light Palette
}

// Variant returns the family's dark or light palette.
func (f Family) Variant(dark bool) Palette {
	if dark {
		return f.Dark
	}
	return f.Light
}

// TerminalFamily uses the terminal's 16 ANSI colors, so the UI matches the
// user's terminal theme; Bg and Surface are the terminal's own background.
// Raised and Selected are bright black (8) in dark terminals, white (7) in
// light ones: black (0) is the background itself in many dark terminals.
// No text is drawn on Accent: the active tab, the mode pill and an
// overlay's highlighted row reverse the terminal's own foreground and
// background (build), the only pair whose contrast the user has chosen.
var TerminalFamily = Family{
	ID:    "terminal",
	Label: "Terminal",
	Dark:  terminal(true),
	Light: terminal(false),
}

func terminal(dark bool) Palette {
	id := "terminal-light"
	text, text2, muted, raised := lipgloss.Color("0"), lipgloss.Color("8"), lipgloss.Color("8"), lipgloss.Color("7")
	if dark {
		id = "terminal-dark"
		text, text2, muted, raised = lipgloss.Color("15"), lipgloss.Color("7"), lipgloss.Color("8"), lipgloss.Color("8")
	}
	return Palette{
		ID: id, Dark: dark, ANSI: true,
		Bg: lipgloss.NoColor{}, Surface: lipgloss.NoColor{}, Raised: raised, Selected: raised,
		Text: text, Text2: text2, Muted: muted, Border: muted,
		Accent: lipgloss.Color("5"), AccentText: lipgloss.Color("5"), AccentFg: lipgloss.Color("15"),
		Running: lipgloss.Color("2"), Starting: lipgloss.Color("3"), Crashed: lipgloss.Color("1"),
		External: lipgloss.Color("5"), Stopped: muted, Warn: lipgloss.Color("3"), Info: lipgloss.Color("6"),
		Port: lipgloss.Color("4"),
	}
}

// Families lists every theme, Terminal first, then the dashboard's
// families in the dashboard's order (generatedFamilies, palettes_gen.go).
func Families() []Family { return append([]Family{TerminalFamily}, generatedFamilies...) }

// Lookup finds a family by id.
func Lookup(id string) (Family, bool) {
	for _, f := range Families() {
		if f.ID == id {
			return f, true
		}
	}
	return Family{}, false
}
