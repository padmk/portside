package theme

import (
	"image/color"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
)

// New builds the styles for a palette in color.
func New(p Palette) Styles { return build(p, p.Surface, false) }

// NewMono builds the styles for a terminal without colors (Monochrome).
func NewMono(p Palette) Styles { return build(p, p.Surface, true) }

// Monochrome reports whether a terminal's color profile shows no colors,
// so the styles must mark state with attributes (NewMono). Bubble Tea
// sends the profile as tea.ColorProfileMsg after start, NO_COLOR included.
func Monochrome(p colorprofile.Profile) bool { return p <= colorprofile.ASCII }

// OnRaised is s for content drawn on Raised, inside an overlay: every
// style that carried Surface carries Raised instead.
func (s Styles) OnRaised() Styles {
	r := build(s.Palette, s.Palette.Raised, s.Mono)
	r.Screen = s.Screen
	return r
}

// OnSelected is s for the selected row's content: every style that
// carried Surface carries Selected instead, so the row's segments keep
// its background (see SelectedRow).
func (s Styles) OnSelected() Styles {
	r := build(s.Palette, s.Palette.Selected, s.Mono)
	r.Screen = s.Screen
	return r
}

// OnSelection is st, one of s's styles, moved onto the selected row: on
// Selected, with a foreground of the row's own color lifted as OnSelected
// lifts it. Mono rows are reversed by SelectedRow, so st stays.
func (s Styles) OnSelection(st lipgloss.Style) lipgloss.Style {
	if s.Mono {
		return st
	}
	p := s.Palette
	return st.Background(p.Selected).Foreground(visible(p, st.GetForeground(), p.Selected))
}

// visible is c, or Text2 when c is the surface's own color and would
// vanish on it: Terminal's Muted (and Stopped and Border, which are
// Muted) is its dark Raised and Selected.
func visible(p Palette, c, surface color.Color) color.Color {
	if sameColor(c, surface) {
		return p.Text2
	}
	return c
}

func build(p Palette, surface color.Color, mono bool) Styles {
	s := lipgloss.NewStyle().Background(surface)
	// A color equal to the surface would vanish: it is lifted (visible) so
	// labels, stopped states and frames stay visible in overlays and on
	// the selected row.
	lift := func(c color.Color) color.Color { return visible(p, c, surface) }
	text := s.Foreground(p.Text)
	muted := s.Foreground(lift(p.Muted))
	border := lift(p.Border)
	accent := s.Foreground(p.AccentText)
	port := s.Foreground(p.Port)
	pane := s.Border(lipgloss.RoundedBorder()).BorderBackground(surface)
	pill := s.Foreground(p.AccentFg).Background(p.Accent).Bold(true).Padding(0, 1)
	st := Styles{
		Palette: p, Mono: mono,
		Screen: lipgloss.NewStyle().Background(p.Bg), Surface: s,
		Text: text, Text2: s.Foreground(p.Text2), Muted: muted, Bold: text.Bold(true),
		Accent: accent, AccentBold: accent.Bold(true), Port: port, Link: port.Underline(true),
		Warn: s.Foreground(p.Warn), Info: s.Foreground(p.Info), Error: s.Foreground(p.Crashed),
		Running: s.Foreground(p.Running), Starting: s.Foreground(p.Starting), Crashed: s.Foreground(p.Crashed), External: s.Foreground(p.External), Stopped: s.Foreground(lift(p.Stopped)),
		Pane: pane.BorderForeground(border), PaneFocused: pane.BorderForeground(p.Accent),
		Frame: s.Foreground(border), FrameFocused: s.Foreground(p.Accent),
		Title: muted, TitleFocused: accent.Bold(true),
		Selected: s.Background(p.Selected), Match: s.Background(p.Raised).Foreground(p.AccentText).Bold(true),
		Tab: muted.Padding(0, 1), TabActive: pill, Mode: pill, Key: accent.Bold(true), KeyHelp: s.Foreground(p.Text2),
		Overlay:         pane.BorderForeground(p.Accent).BorderBackground(p.Raised).Background(p.Raised).Padding(0, 1),
		OverlaySelected: s.Foreground(p.AccentFg).Background(p.Accent).Bold(true),
		Banner:          s.Foreground(p.AccentFg).Background(p.Warn), BannerError: s.Foreground(p.AccentFg).Background(p.Crashed), Dim: muted.Faint(true),
	}
	if mono {
		st.Selected = s.Reverse(true)
		st.Match = s.Bold(true).Underline(true)
		st.TitleFocused = st.TitleFocused.Underline(true)
		st.TabActive = s.Bold(true).Underline(true).Padding(0, 1)
		st.Mode = s.Reverse(true).Bold(true).Padding(0, 1)
		st.OverlaySelected = s.Reverse(true).Bold(true)
		st.Banner = s.Reverse(true)
		st.BannerError = s.Reverse(true).Bold(true)
	}
	return st
}

// sameColor reports whether a and b are the same color.
func sameColor(a, b color.Color) bool {
	if a == nil || b == nil {
		return false
	}
	ar, ag, ab, aa := a.RGBA()
	br, bg, bb, ba := b.RGBA()
	return ar == br && ag == bg && ab == bb && aa == ba
}
