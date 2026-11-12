package logs

import (
	"errors"
	"regexp"
	"regexp/syntax"
	"slices"
	"strconv"
	"strings"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/padmk/portside/internal/tui/ui"
)

// search is the pane's search term and the lines that match it.
type search struct {
	query string
	re    *regexp.Regexp
	// err is why a regular expression does not compile, "" when it does.
	err string
	// only shows just the matching lines (`F`).
	only bool
	// matches are the absolute indexes of the matching lines, ascending.
	matches []int
	// current is the matching line the search went to, -1 before that.
	current int
}

// compileQuery turns a query into its pattern: a case-insensitive literal,
// or with a "re:" prefix the rest as a Go regular expression; errText says
// why a regular expression does not compile.
func compileQuery(q string) (re *regexp.Regexp, errText string) {
	if rest, ok := strings.CutPrefix(q, "re:"); ok {
		if rest == "" {
			return nil, ""
		}
		re, err := regexp.Compile(rest)
		if err != nil {
			text := err.Error()
			var e *syntax.Error
			if errors.As(err, &e) {
				text = string(e.Code)
			}
			return nil, text
		}
		return re, ""
	}
	if q == "" {
		return nil, ""
	}
	return regexp.MustCompile("(?i)" + regexp.QuoteMeta(q)), ""
}

// setQuery replaces the search term and finds its matches.
func (p *Pane) setQuery(q string) {
	s := &p.search
	s.query = q
	s.re, s.err = compileQuery(q)
	s.matches = s.matches[:0]
	s.current = -1
	if s.re == nil {
		return
	}
	for i := range p.buf.Len() {
		if s.re.MatchString(p.buf.At(i).Plain) {
			s.matches = append(s.matches, p.base+i)
		}
	}
}

// clearSearch drops the term and the filter.
func (p *Pane) clearSearch() {
	p.setQuery("")
	p.search.only = false
}

// filtering reports whether only matching lines are shown.
func (p *Pane) filtering() bool { return p.search.only && p.search.re != nil }

// pruneMatches forgets matches of lines the buffer dropped.
func (p *Pane) pruneMatches() {
	s := &p.search
	i, _ := slices.BinarySearch(s.matches, p.base)
	if i > 0 {
		s.matches = append(s.matches[:0], s.matches[i:]...)
	}
	if s.current < p.base {
		s.current = -1
	}
}

// startSearch opens the search prompt with an empty term.
func (p *Pane) startSearch(env *ui.Env) {
	p.editing = true
	p.setQuery("")
	env.EnterMode(ui.ModeSearch)
}

// searchKey edits the search prompt: enter keeps the term and goes to its
// first match, esc clears it.
func (p *Pane) searchKey(env *ui.Env, k tea.KeyPressMsg) tea.Cmd {
	switch k.String() {
	case "enter":
		if p.search.err != "" {
			return notice("invalid regular expression: "+p.search.err, ui.LevelError)
		}
		p.editing = false
		env.EnterMode(ui.ModeNormal)
		if p.search.re == nil {
			p.clearSearch()
			return nil
		}
		return p.jumpMatch(1)
	case "esc":
		p.editing = false
		p.clearSearch()
		env.EnterMode(ui.ModeNormal)
	case "backspace", "ctrl+h":
		if q := []rune(p.search.query); len(q) > 0 {
			p.setQuery(string(q[:len(q)-1]))
		}
	case "ctrl+u":
		p.setQuery("")
	case "ctrl+w":
		q := strings.TrimRight(p.search.query, " ")
		if i := strings.LastIndexByte(q, ' '); i >= 0 {
			q = q[:i+1]
		} else {
			q = ""
		}
		p.setQuery(q)
	default:
		if t := k.Key().Text; t != "" {
			p.setQuery(p.search.query + strings.Map(printable, t))
		}
	}
	return nil
}

func printable(r rune) rune {
	if unicode.IsControl(r) {
		return -1
	}
	return r
}

// jumpMatch goes to the next (dir 1) or previous (dir -1) matching line,
// wrapping around; the first jump goes to the newest match on screen or
// above it. The match is shown in the middle of the screen, its row of a
// wrapped line, and with lines cut, its column.
func (p *Pane) jumpMatch(dir int) tea.Cmd {
	s := &p.search
	if s.re == nil {
		return notice("no search; press / to search", ui.LevelInfo)
	}
	if len(s.matches) == 0 {
		return notice("no lines match "+s.query, ui.LevelWarn)
	}
	n := len(s.matches)
	var i int
	if pos, ok := slices.BinarySearch(s.matches, s.current); s.current >= 0 && ok {
		i = ((pos+dir)%n + n) % n
	} else {
		i, _ = slices.BinarySearch(s.matches, p.anchorAbs()+1)
		i = max(i-1, 0) // the newest match at or above the anchor, else the oldest
	}
	s.current = s.matches[i]
	p.reveal(s.current)
	return nil
}

// toggleFilter shows only the matching lines, or all of them again.
func (p *Pane) toggleFilter() tea.Cmd {
	if p.search.re == nil && !p.search.only {
		return notice("no search; press / to search first", ui.LevelInfo)
	}
	q := p.anchorAbs()
	p.search.only = !p.search.only
	if !p.follow {
		p.anchor, p.rowOff = q, 0
	}
	p.clampAnchor()
	return nil
}

// searchStatus is the search piece of Status: the term with the current
// match and how many lines match ("search error 3/17"), or the filter
// with its count.
func (p *Pane) searchStatus() string {
	s := p.search
	if s.re == nil {
		return ""
	}
	q := ansi.Truncate(s.query, 24, p.glyphs.Ellipsis)
	count := strconv.Itoa(len(s.matches))
	if p.filtering() {
		return "filter " + q + " (" + count + ")"
	}
	if pos, ok := slices.BinarySearch(s.matches, s.current); s.current >= 0 && ok {
		return "search " + q + " " + strconv.Itoa(pos+1) + "/" + count
	}
	if len(s.matches) == 0 {
		return "search " + q + " no match"
	}
	return "search " + q + " " + count + " matches"
}

// prompt is the search prompt row: "/" and the term with a block cursor,
// then the match count, or in Error why a regular expression does not
// compile. A term too long for the row shows its end, after the
// ellipsis, so the cursor stays visible.
func (p *Pane) prompt(env *ui.Env, w int) string {
	st := env.Styles.Text
	hint := ""
	switch s := p.search; {
	case s.err != "":
		st = env.Styles.Error
		hint = "  " + env.Styles.Error.Render(s.err)
	case s.re != nil:
		hint = "  " + env.Styles.Muted.Render(strconv.Itoa(len(s.matches))+" lines")
	}
	text := "/" + p.search.query
	room := w - 1 - min(ansi.StringWidth(hint), w/2)
	if room < ansi.StringWidth(text) {
		hint = ""
		room = w - 1
	}
	if over := ansi.StringWidth(text) - room; over > 0 && w > 2 {
		text = ansi.TruncateLeft(text, over+ansi.StringWidth(env.Glyphs.Ellipsis), env.Glyphs.Ellipsis)
	}
	cursor := env.Styles.Accent.Reverse(true).Render(" ")
	return ui.Fit(st.Render(text)+cursor+hint, w)
}
