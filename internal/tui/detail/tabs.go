package detail

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/padmk/portside/internal/supervisor"
	"github.com/padmk/portside/internal/tui/ui"
	"github.com/padmk/portside/internal/viewfmt"
)

// The pane's tabs, in the order [ and ] go through them.
const (
	tabLogs = iota
	tabDetails
	tabEnv
	tabMetrics
	tabCount
)

// tabNames are the tabs' titles, tabShort their abbreviations for frames
// too narrow for the titles.
var (
	tabNames = []string{"Logs", "Details", "Env", "Metrics"}
	tabShort = []string{"Log", "Det", "Env", "Met"}
)

// hit is a clickable span of the top border, from x to end (exclusive):
// a tab, or with step an arrow to the previous or next tab.
type hit struct {
	x, end int
	tab    int
	step   int
}

// stripLevel is how much of the tab strip fits.
type stripLevel int

const (
	stripFull   stripLevel = iota // every title
	stripTight                    // every title, one space between them
	stripShort                    // every abbreviation
	stripSingle                   // the active title between arrows
)

// minName is how far the name shrinks before the strip does.
const minName = 8

// Strip is a pane's top border with its tabs, as the right pane draws it
// for a server and for a container: the item's state glyph and name, the
// tab strip, and at the right a state word. Names are the tabs' titles,
// Short their abbreviations (the titles when empty), Active the open one.
// Header records where each tab and arrow is, for Click.
type Strip struct {
	Names, Short []string
	Active       int
	// Note follows the name in Muted when it fits beside the whole name
	// (the server pane's "(preview)").
	Note string
	hits []hit
}

// Header lays the top border out for inner cells (the border's width
// without its corners) and returns ui.Box's title and right text. The
// tabs keep their full titles longest: the right texts go first, tried in
// the order given (the longest first), then the name shrinks to minName,
// then the space between titles narrows to one cell, then the titles
// become abbreviations and last the strip shows only the active tab
// between arrows.
func (t *Strip) Header(env *ui.Env, glyph, name string, rights []string, inner int, focused bool) (string, string) {
	type try struct {
		level  stripLevel
		right  string
		shrink bool
	}
	var tries []try
	for _, r := range rights {
		if r != "" {
			tries = append(tries, try{stripFull, r, false})
		}
	}
	tries = append(tries, try{stripFull, "", false}, try{stripFull, "", true}, try{stripTight, "", false}, try{stripTight, "", true},
		try{stripShort, "", false}, try{stripShort, "", true}, try{stripSingle, "", true})
	for i, tr := range tries {
		strip := t.measure(env, tr.level)
		// Box puts a space either side of the title and of the right text;
		// one rule cell stays between them. Without a right text the title
		// may reach the corner.
		fixed := 2 + ansi.StringWidth(strip)
		if tr.right != "" {
			fixed += 1 + ansi.StringWidth(tr.right) + 2
		}
		ident := 0
		if glyph != "" {
			ident += ansi.StringWidth(glyph) + 1
		}
		room := inner - fixed - ident
		nameW := ansi.StringWidth(name)
		if name != "" {
			room-- // the space after the name
		}
		last := i == len(tries)-1
		if nameW > room && (!tr.shrink || room < min(nameW, minName)) && !last {
			continue
		}
		note := t.Note
		if note != "" && nameW+ansi.StringWidth(note)+1 <= room {
			room -= ansi.StringWidth(note) + 1
		} else {
			note = ""
		}
		return t.compose(env, glyph, name, max(0, room), note, tr.level, focused), tr.right
	}
	return "", ""
}

// Click is the tab a click at x of the top border opens; false when x is
// on no tab or arrow.
func (t *Strip) Click(x int) (int, bool) {
	n := len(t.Names)
	for _, h := range t.hits {
		if x >= h.x && x < h.end && n > 0 {
			if h.tab >= 0 {
				return h.tab, true
			}
			return (t.Active + h.step + n) % n, true
		}
	}
	return 0, false
}

// compose renders the title and records where its tabs are.
func (t *Strip) compose(env *ui.Env, glyph, name string, nameW int, note string, level stripLevel, focused bool) string {
	s := env.Styles
	nameStyle := s.Text
	if focused {
		nameStyle = s.TitleFocused
	}
	var b strings.Builder
	x := 2 // after the corner and Box's space
	if glyph != "" {
		b.WriteString(glyph + s.Surface.Render(" "))
		x += ansi.StringWidth(glyph) + 1
	}
	if name != "" && nameW > 0 {
		n := cut(name, nameW, env.Glyphs.Ellipsis)
		b.WriteString(nameStyle.Render(n) + s.Surface.Render(" "))
		x += ansi.StringWidth(n) + 1
	}
	if note != "" {
		b.WriteString(s.Muted.Render(note) + s.Surface.Render(" "))
		x += ansi.StringWidth(note) + 1
	}
	t.hits = t.hits[:0]
	if level == stripSingle {
		prev, next := arrows(env)
		parts := []struct {
			text string
			h    hit
		}{
			{s.Muted.Render(prev), hit{tab: -1, step: -1}},
			{s.TabActive.Render(t.Names[t.Active]), hit{tab: t.Active}},
			{s.Muted.Render(next), hit{tab: -1, step: 1}},
		}
		for _, p := range parts {
			w := lipgloss.Width(p.text)
			p.h.x, p.h.end = x, x+w
			t.hits = append(t.hits, p.h)
			b.WriteString(p.text)
			x += w
		}
		return b.String()
	}
	for _, p := range t.tabs(env, level) {
		w := lipgloss.Width(p.text)
		if p.tab >= 0 {
			t.hits = append(t.hits, hit{x: x, end: x + w, tab: p.tab})
		}
		b.WriteString(p.text)
		x += w
	}
	return b.String()
}

func arrows(env *ui.Env) (string, string) {
	if env.Glyphs.ID == "ascii" {
		return "<", ">"
	}
	return "‹", "›"
}

// measure is the tab strip alone, for measuring.
func (t *Strip) measure(env *ui.Env, level stripLevel) string {
	if level == stripSingle {
		prev, next := arrows(env)
		return prev + env.Styles.TabActive.Render(t.Names[t.Active]) + next
	}
	var b strings.Builder
	for _, p := range t.tabs(env, level) {
		b.WriteString(p.text)
	}
	return b.String()
}

// tabPart is a rendered tab, or with tab -1 the space between two.
type tabPart struct {
	text string
	tab  int
}

// tabs renders the strip's titles at level: padded a cell either side,
// or in the tight level only the active pill is, and one space separates
// two titles without a pill between them.
func (t *Strip) tabs(env *ui.Env, level stripLevel) []tabPart {
	s := env.Styles
	var out []tabPart
	for i := range t.Names {
		name := t.Names[i]
		if level == stripShort && i < len(t.Short) && t.Short[i] != "" {
			name = t.Short[i]
		}
		switch {
		case i == t.Active:
			out = append(out, tabPart{s.TabActive.Render(name), i})
		case level == stripTight:
			if i > 0 && i-1 != t.Active {
				out = append(out, tabPart{s.Surface.Render(" "), -1})
			}
			out = append(out, tabPart{s.Tab.UnsetPadding().Render(name), i})
		default:
			out = append(out, tabPart{s.Tab.Render(name), i})
		}
	}
	return out
}

// header is the server's top border: its state glyph and name, the tab
// strip, and at the right the state word with the uptime, which goes
// first, then the state word alone (the glyph still shows the state).
func (c *Pane) header(env *ui.Env, v supervisor.View, ok bool, inner int, focused bool) (string, string) {
	s := env.Styles
	name := viewfmt.PlainText(c.shown)
	var glyph, word, uptime string
	if ok {
		d := v.Display()
		glyph = s.State(string(d)).Render(env.Glyphs.State(d, env.Frame))
		word = s.StateWord(string(d)).Render(string(d))
		if v.Status == supervisor.Running && v.StartedAt != nil {
			uptime = word + s.Muted.Render(env.Glyphs.Separator+durationLabel(env.Now().Sub(*v.StartedAt)))
		}
	}
	c.strip.Names, c.strip.Short, c.strip.Active = tabNames, tabShort, c.tab
	c.strip.Note = ""
	if previewing(env) {
		c.strip.Note = previewLabel
	}
	return c.strip.Header(env, glyph, name, []string{uptime, word}, inner, focused)
}

// previewLabel follows the server's name while the active list's cursor
// is on something else (a group header, a port, no row): the pane still
// shows the last server, but actions take the cursor's target.
const previewLabel = "(preview)"

// previewing reports whether the shown server is a preview: the target
// is not the shown server.
func previewing(env *ui.Env) bool {
	return env.Selected.Kind == ui.SelectServer && env.Target != env.Selected
}

// bottom is the frame's bottom border with status at its right.
func bottom(env *ui.Env, w int, focused bool, status string) string {
	return ui.BottomBorder(env, w, focused, status)
}
