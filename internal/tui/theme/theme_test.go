package theme

import (
	"image/color"
	"math"
	"reflect"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/padmk/portside/internal/supervisor"
)

func TestFamiliesHaveEveryToken(t *testing.T) {
	seen := map[string]bool{}
	for _, f := range Families() {
		if seen[f.ID] {
			t.Errorf("family %q listed twice", f.ID)
		}
		seen[f.ID] = true
		if got, ok := Lookup(f.ID); !ok || got.ID != f.ID {
			t.Errorf("Lookup(%q) = %q, %v", f.ID, got.ID, ok)
		}
		for _, p := range []Palette{f.Dark, f.Light} {
			if p.ID == "" {
				t.Errorf("%s: a variant has no id", f.ID)
			}
			v := reflect.ValueOf(p)
			for i := range v.NumField() {
				name := v.Type().Field(i).Name
				c, ok := v.Field(i).Interface().(color.Color)
				if !ok && v.Field(i).Type() == reflect.TypeFor[color.Color]() {
					t.Errorf("%s: %s is nil", p.ID, name)
					continue
				}
				if !ok {
					continue
				}
				_, transparent := c.(lipgloss.NoColor)
				if transparent && (f.ID != TerminalFamily.ID || (name != "Bg" && name != "Surface")) {
					t.Errorf("%s: %s is transparent", p.ID, name)
				}
			}
		}
		if !f.Dark.Dark || f.Light.Dark {
			t.Errorf("%s: variants' Dark flags are wrong", f.ID)
		}
	}
	if _, ok := Lookup("no-such-theme"); ok {
		t.Error("Lookup found an unknown id")
	}
}

func TestGlyphSetsAreComplete(t *testing.T) {
	states := []supervisor.Display{
		supervisor.Display(supervisor.Running), supervisor.DisplayUnready, supervisor.Display(supervisor.Crashed),
		supervisor.Display(supervisor.Stopped), supervisor.DisplayDone, supervisor.DisplayExternal, supervisor.Display(supervisor.Starting),
	}
	for _, g := range GlyphSets() {
		shapes := map[string]supervisor.Display{}
		for _, d := range states {
			s := g.State(d, 0)
			if s == "" {
				t.Errorf("%s: no glyph for %s", g.ID, d)
			}
			if o, dup := shapes[s]; dup {
				t.Errorf("%s: %s and %s share %q", g.ID, o, d, s)
			}
			shapes[s] = d
		}
		for name, s := range map[string]string{"Disabled": g.Disabled, "Expanded": g.Expanded, "Collapsed": g.Collapsed, "Logo": g.Logo,
			"Cursor": g.Cursor, "Caret": g.Caret, "Stale": g.Stale, "Rule": g.Rule, "Ellipsis": g.Ellipsis, "Separator": g.Separator} {
			if s == "" {
				t.Errorf("%s: %s is empty", g.ID, name)
			}
		}
		for _, w := range []string{g.Cursor, g.Caret, g.Rule, g.Ellipsis, g.Disabled, g.Stale} {
			if ansi.StringWidth(w) != 1 {
				t.Errorf("%s: %q is not one cell wide", g.ID, w)
			}
		}
		for _, b := range []lipgloss.Border{g.Border, g.Heavy} {
			for _, part := range []string{b.Top, b.Bottom, b.Left, b.Right, b.TopLeft, b.TopRight, b.BottomLeft, b.BottomRight} {
				if ansi.StringWidth(part) != 1 {
					t.Errorf("%s: border part %q is not one cell wide", g.ID, part)
				}
			}
		}
		if g.Heavy == g.Border {
			t.Errorf("%s: Heavy is the same border", g.ID)
		}
		for _, p := range []Plot{g.Spark, g.Chart} {
			switch p := p.(type) {
			case Braille:
			case Levels:
				if len(p.Steps) == 0 || p.Blank == 0 {
					t.Errorf("%s: empty levels", g.ID)
				}
			default:
				t.Errorf("%s: no plot alphabet", g.ID)
			}
		}
	}
	// The ASCII set draws nothing outside ASCII.
	a := ASCII
	all := []string{a.Running, a.Unready, a.Crashed, a.Stopped, a.Done, a.External, a.Disabled, a.Expanded, a.Collapsed, a.Logo,
		a.Cursor, a.Caret, a.Stale, a.Rule, a.Ellipsis, a.Separator, string(a.Spark.(Levels).Steps), string(a.Chart.(Levels).Steps)}
	all = append(all, a.Spinner...)
	for _, b := range []lipgloss.Border{a.Border, a.Heavy} {
		all = append(all, b.Top, b.Bottom, b.Left, b.Right, b.TopLeft, b.TopRight, b.BottomLeft, b.BottomRight)
	}
	for _, s := range all {
		for _, r := range s {
			if r > 0x7e {
				t.Errorf("ascii: %q is not ASCII", s)
			}
		}
	}
}

func TestLevelsStep(t *testing.T) {
	l := Levels{Blank: ' ', Steps: []rune("_.-=#")}
	for v, want := range map[float64]rune{math.NaN(): '_', -1: '_', 0: '_', 0.5: '-', 1: '#', 2: '#'} {
		if got := l.Step(v); got != want {
			t.Errorf("Step(%v) = %q, want %q", v, got, want)
		}
	}
}

func TestFillKeepsBackgroundAcrossResets(t *testing.T) {
	s := New(mustFamily(t, "nord").Light)
	line := s.Accent.Render("a") + " " + s.Muted.Render("b")
	got := s.Row(line, 6)
	if w := ansi.StringWidth(got); w != 6 {
		t.Fatalf("width %d, want 6", w)
	}
	// Every reset but the last is followed by the background again.
	on := prefix(s.Surface)
	parts := strings.Split(got, "\x1b[m")
	for i, p := range parts[1 : len(parts)-1] {
		if !strings.HasPrefix(p, on) {
			t.Errorf("segment %d %q does not restore the background", i+1, p)
		}
	}
	if !strings.HasPrefix(got, on) {
		t.Error("row does not start on the background")
	}
	// The Terminal family stays transparent: no sequence is added.
	ts := New(TerminalFamily.Dark)
	if got := ts.Row("ab", 4); got != "ab  " {
		t.Errorf("terminal row = %q", got)
	}
	if got := ts.Row("abcdef", 3); got != "abc" {
		t.Errorf("cut row = %q", got)
	}
}

func TestMonoMarksStateWithoutColor(t *testing.T) {
	s := NewMono(TerminalFamily.Dark)
	if !s.Selected.GetReverse() || !s.Mode.GetReverse() || !s.TabActive.GetUnderline() || !s.TitleFocused.GetBold() {
		t.Error("mono styles do not mark selection, mode, the active tab and focus with attributes")
	}
	// Inside an overlay the selected row's Raised background is the
	// overlay's own, so its own style must still show without color.
	if r := s.OnRaised(); !r.OverlaySelected.GetReverse() {
		t.Error("the selected row of an overlay is not reversed in mono")
	}
	if got := s.SelectedRow("ab", 3); !strings.HasPrefix(got, "\x1b[7m") {
		t.Errorf("mono selected row = %q, want reverse", got)
	}
	if b := ASCII.PaneBorder(true, false); b != ASCII.Heavy {
		t.Error("the focused ASCII pane is not heavy")
	}
	if b := Unicode.PaneBorder(true, false); b != Unicode.Border {
		t.Error("the focused pane in color is heavy")
	}
	if b := Unicode.PaneBorder(true, true); b != Unicode.Heavy {
		t.Error("the focused pane in mono is not heavy")
	}
}

func mustFamily(t *testing.T, id string) Family {
	t.Helper()
	f, ok := Lookup(id)
	if !ok {
		t.Fatalf("no family %q", id)
	}
	return f
}

// TestNamedPalettesAreLegible mirrors the dashboard's contrast check for
// the pairs the terminal UI draws: text on the background and on Raised
// (overlays) at 4.5:1, muted labels at 3:1, the mode pill's AccentFg on
// Accent at 3:1, since it is bold, and AccentText (key hints, the focused
// title, matches) at 4.5:1 wherever it is drawn. Raised must differ from
// Bg; Selected must stand out from Surface, since it marks the selected
// row, and every style drawn on it (OnSelected) must read there: text,
// Text2 and the cursor at 4.5:1, labels, ports and states at 3:1.
func TestNamedPalettesAreLegible(t *testing.T) {
	for _, f := range Families()[1:] {
		for _, p := range []Palette{f.Dark, f.Light} {
			if contrast(p.Raised, p.Bg) < 1.04 {
				t.Errorf("%s: Raised does not stand out from Bg", p.ID)
			}
			if r := contrast(p.Selected, p.Surface); r < 1.25 {
				t.Errorf("%s: Selected is %.2f:1 on Surface, so the selected row is faint", p.ID, r)
			}
			for _, c := range []struct {
				name   string
				fg, bg color.Color
				min    float64
			}{
				{"Text on Bg", p.Text, p.Bg, 4.5},
				{"Text on Raised", p.Text, p.Raised, 4.5},
				{"Muted on Bg", p.Muted, p.Bg, 3},
				{"Muted on Raised", p.Muted, p.Raised, 3},
				{"AccentFg on Accent", p.AccentFg, p.Accent, 3},
				{"AccentText on Bg", p.AccentText, p.Bg, 4.5},
				{"AccentText on Surface", p.AccentText, p.Surface, 4.5},
				{"AccentText on Raised", p.AccentText, p.Raised, 4.5},
			} {
				if r := contrast(c.fg, c.bg); r < c.min {
					t.Errorf("%s: %s is %.2f:1, want %.1f:1", p.ID, c.name, r, c.min)
				}
			}
			sel := New(p).OnSelected()
			for _, c := range []struct {
				name string
				st   lipgloss.Style
				min  float64
			}{
				{"Text", sel.Text, 4.5}, {"Text2", sel.Text2, 4.5}, {"Accent", sel.Accent, 4.5},
				{"Muted", sel.Muted, 3}, {"Port", sel.Port, 3}, {"Running", sel.Running, 3}, {"Starting", sel.Starting, 3},
				{"Crashed", sel.Crashed, 3}, {"External", sel.External, 3}, {"Stopped", sel.Stopped, 3}, {"Warn", sel.Warn, 3},
				{"Info", sel.Info, 3}, {"a field's link", New(p).OnSelection(New(p).Link), 3},
			} {
				if r := contrast(c.st.GetForeground(), p.Selected); r < c.min {
					t.Errorf("%s: %s on Selected is %.2f:1, want %.1f:1", p.ID, c.name, r, c.min)
				}
			}
		}
	}
}

// TestTerminalSelectionIsVisible: the Terminal family's selected row is not
// ANSI black, which is the background of many dark terminals, and muted
// text drawn on it (OnSelected, OnSelection) is lifted off the row's own
// color.
func TestTerminalSelectionIsVisible(t *testing.T) {
	for _, p := range []Palette{TerminalFamily.Dark, TerminalFamily.Light} {
		if sameColor(p.Selected, lipgloss.Color("0")) && p.Dark {
			t.Errorf("%s: Selected is ANSI black", p.ID)
		}
		s := New(p)
		r := s.OnSelected()
		for name, st := range map[string]lipgloss.Style{"muted": r.Muted, "stopped": r.Stopped, "done": r.State("done"), "lifted muted": s.OnSelection(s.Muted)} {
			if sameColor(st.GetForeground(), p.Selected) {
				t.Errorf("%s: %s text on the selected row has the row's color", p.ID, name)
			}
			if !sameColor(st.GetBackground(), p.Selected) {
				t.Errorf("%s: %s text is not on the selected row's background", p.ID, name)
			}
		}
		if !sameColor(s.Selected.GetBackground(), p.Selected) {
			t.Errorf("%s: the selected row is not on Selected", p.ID)
		}
	}
	// Named themes keep Muted on Selected: it already reads at 3:1 there.
	nord := mustFamily(t, "nord").Light
	if !sameColor(New(nord).OnSelected().Muted.GetForeground(), nord.Muted) {
		t.Error("a named theme's muted text changed on Selected")
	}
}

// TestTerminalHighlightsReverse: in the Terminal family, whose ANSI colors
// have unknown contrast, the selected row's text that carries a meaning is
// Text while its state glyphs keep their color, and the active tab, the
// mode pill and an overlay's highlighted row reverse the terminal's own
// colors instead of drawing on Accent. Named themes keep their fills.
func TestTerminalHighlightsReverse(t *testing.T) {
	for _, p := range []Palette{TerminalFamily.Dark, TerminalFamily.Light} {
		s := New(p)
		sel := s.OnSelected()
		for name, st := range map[string]lipgloss.Style{
			"Port": sel.Port, "Link": sel.Link, "Warn": sel.Warn, "Info": sel.Info, "Error": sel.Error,
			"Running": sel.Running, "Starting": sel.Starting, "Crashed": sel.Crashed, "External": sel.External, "Stopped": sel.Stopped,
			"crashed word": sel.StateWord("crashed"), "stopped word": sel.StateWord("stopped"), "a field's port": s.OnSelection(s.Port), "a field's warning": s.OnSelection(s.Warn),
		} {
			if !sameColor(st.GetForeground(), p.Text) {
				t.Errorf("%s: %s on the selected row is not Text", p.ID, name)
			}
		}
		if !sameColor(sel.State("crashed").GetForeground(), p.Crashed) || !sameColor(sel.State("running").GetForeground(), p.Running) {
			t.Errorf("%s: a state glyph lost its color on the selected row", p.ID)
		}
		for name, st := range map[string]lipgloss.Style{"TabActive": s.TabActive, "Mode": s.Mode, "OverlaySelected": s.OverlaySelected} {
			if !st.GetReverse() || !st.GetBold() {
				t.Errorf("%s: %s is not reverse and bold", p.ID, name)
			}
			if _, none := st.GetBackground().(lipgloss.NoColor); !none {
				t.Errorf("%s: %s has a background of its own", p.ID, name)
			}
		}
	}
	nord := mustFamily(t, "nord").Dark
	if s := New(nord); s.OverlaySelected.GetReverse() || !sameColor(s.OverlaySelected.GetBackground(), nord.Accent) ||
		!sameColor(s.OnSelected().Port.GetForeground(), nord.Port) {
		t.Error("a named theme lost its accent fill or its port color on Selected")
	}
}
