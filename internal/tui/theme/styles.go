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
	Selected            lipgloss.Style // background of the selected row; reverse when Mono
	Match               lipgloss.Style // a search match
	Tab, TabActive      lipgloss.Style
	Mode                lipgloss.Style // the footer's mode pill: AccentFg on Accent
	Key, KeyHelp        lipgloss.Style // a key hint and its description
	Overlay             lipgloss.Style // dialogs and menus on Raised, Accent border
	// OverlaySelected is the highlighted row of a list inside an overlay,
	// whose own background is Raised: AccentFg on Accent; reverse and
	// bold when Mono.
	OverlaySelected     lipgloss.Style
	Banner, BannerError lipgloss.Style // the top bar's warning and error banners
	Dim                 lipgloss.Style // what lies under an overlay
}

// State returns the style for a display state.
func (s Styles) State(d string) lipgloss.Style {
	switch d {
	case "running":
		return s.Running
	case "starting", "stopping", "unready":
		return s.Starting
	case "crashed", "blocked":
		return s.Crashed
	case "external":
		return s.External
	}
	return s.Stopped
}
