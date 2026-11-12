package theme

import (
	"image/color"
	"math"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
)

// New builds the styles for a palette in color.
func New(p Palette) Styles { return build(p, p.Surface, false, false) }

// NewMono builds the styles for a terminal without colors (Monochrome).
func NewMono(p Palette) Styles { return build(p, p.Surface, true, false) }

// Monochrome reports whether a terminal's color profile shows no colors,
// so the styles must mark state with attributes (NewMono). Bubble Tea
// sends the profile as tea.ColorProfileMsg after start, NO_COLOR included.
func Monochrome(p colorprofile.Profile) bool { return p <= colorprofile.ASCII }

// OnRaised is s for content drawn on Raised, inside an overlay: every
// style that carried Surface carries Raised instead.
func (s Styles) OnRaised() Styles {
	r := build(s.Palette, s.Palette.Raised, s.Mono, false)
	r.Screen = s.Screen
	return r
}

// OnSelected is s for the selected row's content: every style that
// carried Surface carries Selected instead, so the row's segments keep
// its background (see SelectedRow). In the Terminal family the text
// styles that carry a meaning (ports, states, warnings) are Text there,
// since an ANSI color on ANSI 8 or 7 may not read; State's glyph styles
// keep their color.
func (s Styles) OnSelected() Styles {
	r := build(s.Palette, s.Palette.Selected, s.Mono, true)
	r.Screen = s.Screen
	return r
}

// OnSelection is st, one of s's styles, moved onto the selected row: on
// Selected, with its foreground made readable there as OnSelected makes
// it (a semantic color is Text in the Terminal family). Mono rows are
// reversed by SelectedRow, so st stays.
func (s Styles) OnSelection(st lipgloss.Style) lipgloss.Style {
	if s.Mono {
		return st
	}
	p := s.Palette
	return st.Background(p.Selected).Foreground(selectedText(p, st.GetForeground()))
}

// selectedText is the color of text in color c on the selected row: Text
// for the Terminal family's semantic colors, else c readable on Selected.
// Stopped is Muted's ANSI color there, so a color equal to it is left to
// readableOn: muted labels stay secondary.
func selectedText(p Palette, c color.Color) color.Color {
	if p.ANSI {
		for _, sem := range []color.Color{p.Port, p.Running, p.Starting, p.Crashed, p.External, p.Warn, p.Info} {
			if sameColor(c, sem) {
				return p.Text
			}
		}
	}
	return readableOn(p, c, p.Selected, 3)
}

// readableOn is c as drawn on surface. A color equal to the surface would
// vanish (Terminal's Muted, Stopped and Border on its dark Raised and
// Selected): it is lifted to Text2. On a named theme's Selected, which is
// tinted strongly enough to mark the row, c is mixed toward Text in 1%
// steps until it reads at least at min:1, the way scripts/tuithemes
// derives AccentText; Terminal's ANSI colors are the user's to set.
func readableOn(p Palette, c, surface color.Color, min float64) color.Color {
	if sameColor(c, surface) {
		return p.Text2
	}
	if p.ANSI || c == nil || !sameColor(surface, p.Selected) || contrast(c, surface) >= min {
		return c
	}
	cr, cg, cb, _ := c.RGBA()
	tr, tg, tb, _ := p.Text.RGBA()
	for pct := 99; pct >= 0; pct-- {
		f := float64(pct) / 100
		mix := func(a, b uint32) uint8 { return uint8(math.Round((float64(a>>8)*f + float64(b>>8)*(1-f)))) }
		m := color.RGBA{mix(cr, tr), mix(cg, tg), mix(cb, tb), 0xff}
		if contrast(m, surface) >= min {
			return m
		}
	}
	return p.Text
}

// contrast is the WCAG contrast ratio of two colors.
func contrast(a, b color.Color) float64 {
	lum := func(c color.Color) float64 {
		r, g, bl, _ := c.RGBA()
		ch := func(v uint32) float64 {
			x := float64(v) / 0xffff
			if x <= 0.03928 {
				return x / 12.92
			}
			return math.Pow((x+0.055)/1.055, 2.4)
		}
		return 0.2126*ch(r) + 0.7152*ch(g) + 0.0722*ch(bl)
	}
	x, y := lum(a), lum(b)
	return (math.Max(x, y) + 0.05) / (math.Min(x, y) + 0.05)
}

// build makes the styles on surface; selected is set for the selected
// row's (OnSelected).
func build(p Palette, surface color.Color, mono, selected bool) Styles {
	s := lipgloss.NewStyle().Background(surface)
	// Labels, states and frames stay readable on the surface (readableOn):
	// text at 4.5:1, the rest at 3:1.
	on := func(c color.Color, min float64) lipgloss.Style { return s.Foreground(readableOn(p, c, surface, min)) }
	text := s.Foreground(p.Text)
	muted := on(p.Muted, 3)
	border := readableOn(p, p.Border, surface, 1)
	accent := on(p.AccentText, 4.5)
	port := on(p.Port, 3)
	pane := s.Border(lipgloss.RoundedBorder()).BorderBackground(surface)
	pill := s.Foreground(p.AccentFg).Background(p.Accent).Bold(true).Padding(0, 1)
	st := Styles{
		Palette: p, Mono: mono,
		Screen: lipgloss.NewStyle().Background(p.Bg), Surface: s,
		Text: text, Text2: on(p.Text2, 4.5), Muted: muted, Bold: text.Bold(true),
		Accent: accent, AccentBold: accent.Bold(true), Port: port, Link: port.Underline(true),
		Warn: on(p.Warn, 3), Info: on(p.Info, 3), Error: on(p.Crashed, 3),
		Running: on(p.Running, 3), Starting: on(p.Starting, 3), Crashed: on(p.Crashed, 3), External: on(p.External, 3), Stopped: on(p.Stopped, 3),
		Pane: pane.BorderForeground(border), PaneFocused: pane.BorderForeground(p.Accent),
		Frame: s.Foreground(border), FrameFocused: s.Foreground(p.Accent),
		Title: muted, TitleFocused: accent.Bold(true),
		Selected: s.Background(p.Selected), Match: s.Background(p.Raised).Foreground(p.AccentText).Bold(true),
		Tab: muted.Padding(0, 1), TabActive: pill, Mode: pill, Key: accent.Bold(true), KeyHelp: s.Foreground(p.Text2),
		Overlay:         pane.BorderForeground(p.Accent).BorderBackground(p.Raised).Background(p.Raised).Padding(0, 1),
		OverlaySelected: s.Foreground(p.AccentFg).Background(p.Accent).Bold(true),
		Banner:          s.Foreground(p.AccentFg).Background(p.Warn), BannerError: s.Foreground(p.AccentFg).Background(p.Crashed), Dim: muted.Faint(true),
	}
	st.glyphs = stateStyles{st.Running, st.Starting, st.Crashed, st.External, st.Stopped}
	st.idleWord = st.Text2
	if selected && !mono && p.ANSI {
		t := func(c color.Color) lipgloss.Style { return s.Foreground(selectedText(p, c)) }
		st.Port, st.Link = t(p.Port), t(p.Port).Underline(true)
		st.Warn, st.Info, st.Error = t(p.Warn), t(p.Info), t(p.Crashed)
		st.Running, st.Starting, st.Crashed, st.External, st.Stopped = t(p.Running), t(p.Starting), t(p.Crashed), t(p.External), st.Text
		st.idleWord = st.Text
	}
	if p.ANSI {
		// The terminal's own foreground and background, reversed: the
		// one pair on any ANSI palette that reads, whatever Accent is.
		st.TabActive = s.Reverse(true).Bold(true).Padding(0, 1)
		st.Mode = st.TabActive
		st.OverlaySelected = s.Reverse(true).Bold(true)
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
