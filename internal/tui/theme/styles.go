package theme

import "charm.land/lipgloss/v2"

// Styles are the lipgloss styles every part of the UI draws with, built
// from one palette; nothing outside this package picks a color.
//
// Every style drawn inside a pane carries the Surface background, so a
// named theme paints its own background whatever the terminal's is; in the
// Terminal family that background is lipgloss.NoColor and the terminal's
// own shows through.
type Styles struct {
	Palette Palette
	// Mono is set when the terminal shows no colors (NO_COLOR, or a
	// profile without color; see Monochrome): selection, focus, the active
	// tab and the mode then show as reverse, bold and underline.
	Mono bool

	// Screen is the background outside panes, Surface inside them.
	Screen, Surface lipgloss.Style

	Text, Text2, Muted, Bold lipgloss.Style
	Accent, AccentBold       lipgloss.Style
	Port, Link               lipgloss.Style // Link is underlined Port
	Warn, Info, Error        lipgloss.Style

	// State colors a state word; State and StateWord pick one for a
	// display state. On the Terminal family's selected row (OnSelected)
	// they are Text, the way Port, Warn, Info and Error are there, while
	// the glyph styles (State) keep their color.
	Running, Starting, Crashed, External, Stopped lipgloss.Style
	glyphs                                        stateStyles
	// idleWord is an inactive state's word: Text2, Text on the Terminal
	// family's selected row.
	idleWord lipgloss.Style

	Pane, PaneFocused   lipgloss.Style // rounded borders, Border or Accent colored
	Frame, FrameFocused lipgloss.Style // the border characters a pane draws itself, on Surface
	Title, TitleFocused lipgloss.Style // pane titles in the top border
	Selected            lipgloss.Style // the selected row's background, Selected; reverse when Mono
	Match               lipgloss.Style // a search match
	Tab, TabActive      lipgloss.Style
	// Mode is the footer's mode pill: AccentFg on Accent; reverse and
	// bold in the Terminal family and when Mono, like TabActive.
	Mode         lipgloss.Style
	Key, KeyHelp lipgloss.Style // a key hint and its description (Text2)
	Overlay      lipgloss.Style // dialogs and menus on Raised, Accent border
	// OverlaySelected is the highlighted row of a list inside an overlay,
	// whose own background is Raised: AccentFg on Accent; reverse and
	// bold in the Terminal family and when Mono.
	OverlaySelected     lipgloss.Style
	Banner, BannerError lipgloss.Style // the top bar's warning and error banners
	Dim                 lipgloss.Style // what lies under an overlay
}

// stateStyles are the colors of the state glyphs.
type stateStyles struct{ running, starting, crashed, external, stopped lipgloss.Style }

// State returns the style for a display state's glyph (and a graphic
// such as the CPU sparkline), which keeps its color on every row.
func (s Styles) State(d string) lipgloss.Style {
	g := s.glyphs
	st, _ := pick(d, g.running, g.starting, g.crashed, g.external, g.stopped)
	return st
}

// StateWord returns the style for a display state's word: State's color
// for a state that is active or failing, Text2 for an inactive one
// (stopped, exited, done), whose word must read while its glyph stays
// quiet (Text on the Terminal family's selected row).
func (s Styles) StateWord(d string) lipgloss.Style {
	if st, active := pick(d, s.Running, s.Starting, s.Crashed, s.External, s.Stopped); active {
		return st
	}
	return s.idleWord
}

// pick is the style of display state d among the five, and whether d is
// active or failing.
func pick(d string, running, starting, crashed, external, stopped lipgloss.Style) (lipgloss.Style, bool) {
	switch d {
	case "running":
		return running, true
	case "starting", "stopping", "unready":
		return starting, true
	case "crashed", "blocked":
		return crashed, true
	case "external":
		return external, true
	}
	return stopped, false
}
