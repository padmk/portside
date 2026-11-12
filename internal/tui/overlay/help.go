package overlay

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/padmk/portside/internal/tui/keymap"
	"github.com/padmk/portside/internal/tui/theme"
)

// Help is the help overlay (`?`): the actions of each scope, scrolled by
// Offset.
type Help struct {
	Offset int
	// limit is one more than the largest Offset of the last View, 0
	// before the first.
	limit int
}

// Scroll moves the view by n rows, kept within the rows the last View had.
func (p *Help) Scroll(n int) {
	p.Offset = max(0, p.Offset+n)
	if p.limit > 0 {
		p.Offset = min(p.Offset, p.limit-1)
	}
}

// helpEntry is one row of the help: the keys (or the command) and the
// description.
type helpEntry struct{ keys, help string }

// helpSection is a heading and its rows.
type helpSection struct {
	title   string
	entries []helpEntry
}

// sections lists the actions of the scopes in their order, one section a
// scope (the global scope one per group: panes, main panel, general). The
// first scope is the focused pane's and says so. An action in several
// scopes is listed in each with what it does there; only the navigation
// keys every list shares are listed once.
func sections(r *keymap.Registry, scopes []keymap.Scope, ascii bool) []helpSection {
	var out []helpSection
	index := map[string]int{}
	navSeen := map[string]bool{}
	for si, sc := range scopes {
		for _, a := range r.InScope(sc) {
			title := sc.Title()
			switch {
			case a.Group == "Navigation":
				if navSeen[a.ID] {
					continue
				}
				navSeen[a.ID] = true
				title = "Lists and the main panel"
			case sc == keymap.ScopeGlobal:
				title = a.Group
			case si == 0:
				title += " (this pane)"
			}
			i, ok := index[title]
			if !ok {
				i = len(out)
				index[title] = i
				out = append(out, helpSection{title: title})
			}
			keys := ":" + a.ID
			if len(a.Keys) > 0 {
				labels := make([]string, len(a.Keys))
				for k, key := range a.Keys {
					labels[k] = KeyLabel(key, ascii)
				}
				keys = strings.Join(labels, " ")
			}
			help := a.Help
			if a.Destructive {
				help += " (asks first)"
			}
			out[i].entries = append(out[i].entries, helpEntry{keys, help})
		}
	}
	// The shared navigation keys follow the focused pane's section.
	if i, ok := index["Lists and the main panel"]; ok && i < len(out)-1 {
		nav := out[i]
		out = append(out[:i], out[i+1:]...)
		at := min(len(out), 1)
		out = append(out[:at], append([]helpSection{nav}, out[at:]...)...)
	}
	return out
}

// View renders the overlay for a w x h screen: at most 100 cells wide and
// h-4 rows tall (at least 3), the sections of scopes in order, in two
// columns when the box is 90 cells wide or more; descriptions wrap within
// their column.
func (p *Help) View(s theme.Styles, g theme.Glyphs, r *keymap.Registry, scopes []keymap.Scope, ascii bool, w, h int) string {
	pn := newPen(s, g)
	secs := sections(r, scopes, ascii)
	keyW := 0
	for _, sec := range secs {
		for _, e := range sec.entries {
			keyW = max(keyW, ansi.StringWidth(e.keys))
		}
	}
	keyW = min(keyW, 18)
	bw := max(min(100, w), 12)
	inner := max(bw-4, 1)
	two := bw >= 90
	col := inner
	if two {
		col = (inner - 3) / 2
	}
	// Descriptions wrap under themselves, past the key column.
	textW := max(8, col-keyW-2)
	var lines []string
	for i, sec := range secs {
		if i > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, pn.s.AccentBold.Render(sec.title))
		for _, e := range sec.entries {
			style := pn.s.Key
			if strings.HasPrefix(e.keys, ":") {
				style = pn.s.Muted
			}
			for j, part := range strings.Split(ansi.Wordwrap(e.help, textW, ""), "\n") {
				part = ansi.Truncate(part, textW, g.Ellipsis)
				key := pn.space(keyW)
				if j == 0 {
					key = pn.fit(style.Render(e.keys), keyW)
				}
				lines = append(lines, key+pn.space(2)+pn.s.Text.Render(part))
			}
		}
	}
	if two {
		lines = splitAtBlank(pn, lines, (inner-3)/2)
	}
	rows := max(min(h-4, len(lines)+3)-3, 1)
	p.limit = max(0, len(lines)-rows) + 1
	p.Offset = max(0, min(p.Offset, p.limit-1))
	shown := append([]string{}, lines[p.Offset:min(len(lines), p.Offset+rows)]...)
	more := ""
	if p.limit > 1 {
		more = fmt.Sprintf("  %d/%d", p.Offset+1, p.limit)
	}
	keys := [][2]string{{"j/k", "scroll"}, {KeyLabel("ctrl+p", ascii), "find an action"}, {"esc", "close"}}
	shown = append(shown, HintFit(s, g, inner-ansi.StringWidth(more), keys...)+pn.s.Muted.Render(more))
	return pn.box("Keys", shown, inner)
}

// splitAtBlank lays lines out in two columns, breaking at the blank line
// between groups closest to the middle.
func splitAtBlank(pn pen, lines []string, col int) []string {
	cut := (len(lines) + 1) / 2
	best := -1
	for i, l := range lines {
		if l == "" && (best < 0 || abs(i-cut) < abs(best-cut)) {
			best = i
		}
	}
	left, right := lines[:cut], lines[cut:]
	if best >= 0 {
		left, right = lines[:best], lines[best+1:]
	}
	out := make([]string, max(len(left), len(right)))
	for i := range out {
		l, r := "", ""
		if i < len(left) {
			l = left[i]
		}
		if i < len(right) {
			r = right[i]
		}
		out[i] = pn.fit(l, col) + pn.space(3) + r
	}
	return out
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
