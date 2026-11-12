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

	// State colors a glyph or state word.
	Running, Starting, Crashed, External, Stopped lipgloss.Style

	Pane, PaneFocused   lipgloss.Style // rounded borders, Border or Accent colored
	Frame, FrameFocused lipgloss.Style // the border characters a pane draws itself, on Surface
	Title, TitleFocused lipgloss.Style // pane titles in the top border
	Selected            lipgloss.Style // the selected row's background, Selected; reverse when Mono
	Match               lipgloss.Style // a search match
	Tab, TabActive      lipgloss.Style
	Mode                lipgloss.Style // the footer's mode pill: AccentFg on Accent
	Key, KeyHelp        lipgloss.Style // a key hint and its description (Text2)
	Overlay             lipgloss.Style // dialogs and menus on Raised, Accent border
	// OverlaySelected is the highlighted row of a list inside an overlay,
	// whose own background is Raised: AccentFg on Accent; reverse and
	// bold when Mono.
	OverlaySelected     lipgloss.Style
	Banner, BannerError lipgloss.Style // the top bar's warning and error banners
	Dim                 lipgloss.Style // what lies under an overlay
}

// State returns the style for a display state's glyph.
func (s Styles) State(d string) lipgloss.Style {
	st, _ := s.state(d)
	return st
}

// StateWord returns the style for a display state's word: State's color
// for a state that is active or failing, Text2 for an inactive one
// (stopped, exited, done), whose word must read while its glyph stays
// quiet.
func (s Styles) StateWord(d string) lipgloss.Style {
	if st, active := s.state(d); active {
		return st
	}
	return s.Text2
}

func (s Styles) state(d string) (lipgloss.Style, bool) {
	switch d {
	case "running":
		return s.Running, true
	case "starting", "stopping", "unready":
		return s.Starting, true
	case "crashed", "blocked":
		return s.Crashed, true
	case "external":
		return s.External, true
	}
	return s.Stopped, false
}
