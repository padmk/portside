package logs

import (
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/padmk/portside/internal/supervisor"
	"github.com/padmk/portside/internal/tui/ui"
)

const (
	// maxPartial bounds a line: past it, an unfinished line so far is kept
	// as a line of its own and a longer complete line is split, so the
	// work of cleaning, matching and drawing one line stays bounded.
	maxPartial = 64 << 10
	// maxRedraw is how many recent terminal rows a line moving the cursor
	// up can write over, as supervisor.CursorUp bounds it.
	maxRedraw = 100
	// gapLabel is the rule shown where output was dropped.
	gapLabel = "some output was not shown"
)

// ingestState follows the terminal rows of the output: the raw
// unfinished last line and the line it shows, and the buffer lines of the
// latest rows, so that a line moving the cursor up (docker compose
// redrawing the rows it keeps free) writes over them as
// supervisor.PlainLines does. A row the pane left out is written by
// adding a line at the end, which keeps the order while such rows are the
// latest, as the free rows of a redraw are.
type ingestState struct {
	partial   string
	partialAt time.Time
	// open is the absolute index of the line showing partial, -1 for none.
	open int
	// rows are the absolute indexes of the latest rows, -1 for a row the
	// pane left out (a blank line); row is the index into rows of the row
	// the next line writes, len(rows) at the end.
	rows []int
	row  int
}

func newIngest() ingestState { return ingestState{open: -1} }

// apply takes a stream message: a new connection's scrollback replaces the
// lines, dated by the logs reply where the two agree, then live output is
// split into lines stamped with the time it arrived.
func (p *Pane) apply(env *ui.Env, m feedMsg) {
	now := env.Now()
	if m.reset {
		if p.restore == nil && !p.loading {
			st := p.readState()
			p.restore = &st
		}
		p.leaveModes(env)
		p.resetLines()
		p.loading, p.lost = false, false
		p.ingest(string(m.backlog), time.Time{})
		p.dateLines(m.times)
		if p.restore != nil {
			p.restoreState(*p.restore)
			p.restore = nil
		}
	}
	if m.gap {
		p.addGap(now)
	}
	if len(m.data) > 0 {
		p.ingest(string(m.data), now)
	}
	if m.err != nil {
		p.lost = true
	}
	p.decideStamps()
	p.pruneMatches()
	if p.visual {
		p.vstart, p.vcur = max(p.vstart, p.base), max(p.vcur, p.base)
	}
	p.clampAnchor()
}

// resetLines drops every line and the matches.
func (p *Pane) resetLines() {
	p.buf.Reset()
	p.base = 0
	p.in = newIngest()
	p.search.matches = p.search.matches[:0]
	p.search.current = -1
	p.anchor, p.rowOff, p.unseen = 0, 0, 0
	p.visual = false
}

// ingest splits output into lines. An unfinished last line is shown and
// replaced as output extends it; a line is stamped with the time its
// first part arrived.
func (p *Pane) ingest(data string, at time.Time) {
	in := &p.in
	if in.open >= 0 {
		p.popLast(in.open)
		in.open = -1
	}
	first := in.partialAt
	if in.partial == "" {
		first = at
	}
	parts := strings.Split(in.partial+data, "\n")
	last, complete := parts[len(parts)-1], parts[:len(parts)-1]
	if n := len(complete) - p.buf.Max; n > 0 {
		// Only the newest lines stay.
		complete = complete[n:]
		in.rows, in.row = nil, 0
	}
	for i, raw := range complete {
		t := at
		if i == 0 {
			t = first
		}
		// The carriage return a terminal ends lines with is no part of
		// their text, and must not become a row of its own.
		if len(raw) > maxPartial {
			raw = strings.TrimRight(raw, "\r")
		}
		for len(raw) > maxPartial {
			cut := safeCut(raw, maxPartial)
			p.place(raw[:cut], t)
			raw = raw[cut:]
		}
		p.place(raw, t)
	}
	if len(complete) > 0 {
		first = at
	}
	for len(last) > maxPartial {
		cut := safeCut(last, maxPartial)
		p.place(last[:cut], first)
		last, first = last[cut:], at
	}
	in.partial, in.partialAt = last, first
	if last != "" && in.row >= len(in.rows) && supervisor.CursorUp(last) == 0 {
		in.open = p.appendLine(Clean(last, first))
	}
}

// safeCut is where a long unfinished line can end: at most n bytes in,
// at a character start and outside an escape sequence.
func safeCut(s string, n int) int {
	n = min(n, len(s))
	if i := strings.LastIndexByte(s[:n], '\x1b'); i >= 0 && !complete(s[i:n]) {
		n = i
	}
	for n > 0 && n < len(s) && !utf8.RuneStart(s[n]) {
		n--
	}
	if n == 0 {
		return min(len(s), maxPartial)
	}
	return n
}

// complete reports whether s, which starts with ESC, holds a whole escape
// sequence. The pattern also takes ESC and an introducer of a longer
// sequence (CSI "[", OSC "]", DCS, SOS, PM, APC) as a two-byte escape, so
// that one is whole only when nothing follows the introducer in s.
func complete(s string) bool {
	loc := escape.FindStringIndex(s)
	if loc == nil || loc[0] != 0 {
		return false
	}
	return !(loc[1] == 2 && len(s) > 2 && strings.IndexByte("[]PX^_", s[1]) >= 0)
}

// place writes one complete raw line on its terminal row: a new row at
// the end, or over a recent row the line moved the cursor up to; an
// overwrite that leaves no text keeps the row's text.
func (p *Pane) place(raw string, at time.Time) {
	in := &p.in
	in.row = max(in.row-supervisor.CursorUp(raw), 0)
	l := Clean(raw, at)
	switch {
	case in.row >= len(in.rows):
		in.rows = append(in.rows, p.appendLine(l))
		in.row = len(in.rows)
	case in.rows[in.row] < p.base:
		// A row the pane left out, or one no longer kept: the line is
		// added at the end, where such rows are.
		in.rows[in.row] = p.appendLine(l)
		in.row++
	case l.Plain != "":
		p.setLine(in.rows[in.row], l)
		in.row++
	default:
		in.row++
	}
	if n := len(in.rows) - (maxRedraw + 1); n > 0 {
		in.rows = append(in.rows[:0], in.rows[n:]...)
		in.row -= n
	}
}

// addGap marks output the stream dropped: the unfinished line ends where
// it is and a rule says that output is missing.
func (p *Pane) addGap(now time.Time) {
	p.in = newIngest()
	p.appendLine(Line{At: now, Text: gapLabel, Plain: gapLabel, Rule: &Rule{Label: gapLabel}})
}

// appendLine adds a line and returns its absolute index, -1 when it is
// left out. Empty lines at the very start or after a rule are left out, a
// rule takes the place of the empty line the daemon writes before it, and
// a rule repeating the one just before it is left out.
func (p *Pane) appendLine(l Line) int {
	n := p.buf.Len()
	if strings.TrimSpace(l.Plain) == "" && (n == 0 || p.buf.At(n-1).Rule != nil) {
		return -1
	}
	if l.Rule != nil && n > 0 {
		prev := p.buf.At(n - 1)
		if prev.Rule != nil && prev.Rule.Label == l.Rule.Label {
			return -1
		}
		if strings.TrimSpace(prev.Plain) == "" {
			p.setLine(p.base+n-1, l)
			return p.base + n - 1
		}
	}
	p.buf.Append(l)
	if p.buf.Len() == n {
		p.base++
	}
	abs := p.base + p.buf.Len() - 1
	p.matchAdded(abs, l)
	if !p.follow {
		p.unseen++
	}
	return abs
}

// setLine replaces the line at absolute index abs.
func (p *Pane) setLine(abs int, l Line) {
	p.buf.Set(abs-p.base, l)
	p.matchSet(abs, l)
}

// popLast removes the last line, at absolute index abs.
func (p *Pane) popLast(abs int) {
	if abs != p.base+p.buf.Len()-1 {
		return
	}
	p.buf.Pop()
	p.matchRemoved(abs)
	if !p.follow && p.unseen > 0 {
		p.unseen--
	}
}

// dateLines gives the scrollback's lines the times of the logs reply.
// The reply was read after the scrollback, so it may end with newer
// lines: its line matching the newest complete line, with at least the
// verify lines before it matching too, is where the two meet, and the
// lines date back from there as long as they agree. Lines that find no
// partner keep no time.
func (p *Pane) dateLines(times []Line) {
	const verify, scan = 20, 1000
	var mine, theirs []int // non-blank lines, newest first
	var mineText, theirText []string
	for i := p.buf.Len() - 1; i >= 0; i-- {
		if p.base+i == p.in.open {
			continue
		}
		if t := normalize(p.buf.At(i).Plain); t != "" {
			mine, mineText = append(mine, i), append(mineText, t)
		}
	}
	for i := len(times) - 1; i >= 0; i-- {
		if t := normalize(times[i].Plain); t != "" {
			theirs, theirText = append(theirs, i), append(theirText, t)
		}
	}
	if len(mine) == 0 {
		return
	}
	for h := 0; h < min(len(theirs), scan); h++ {
		if theirText[h] != mineText[0] {
			continue
		}
		n := 0
		for n < len(mine) && h+n < len(theirs) && mineText[n] == theirText[h+n] {
			n++
		}
		if n < min(verify, len(mine), len(theirs)-h) {
			continue
		}
		for j := range n {
			l := p.buf.At(mine[j])
			l.At = times[theirs[h+j]].At
			p.buf.Set(mine[j], l)
		}
		return
	}
}

// normalize is a line's text for comparing the two views of the output:
// words separated by single spaces.
func normalize(s string) string { return strings.Join(strings.Fields(s), " ") }

// Matches follow the lines: added, replaced and removed.

func (p *Pane) matchAdded(abs int, l Line) {
	if p.search.re != nil && p.search.re.MatchString(l.Plain) {
		p.search.matches = append(p.search.matches, abs)
	}
}

func (p *Pane) matchSet(abs int, l Line) {
	s := &p.search
	if s.re == nil {
		return
	}
	i, has := slices.BinarySearch(s.matches, abs)
	switch hit := s.re.MatchString(l.Plain); {
	case hit && !has:
		s.matches = slices.Insert(s.matches, i, abs)
	case !hit && has:
		s.matches = slices.Delete(s.matches, i, i+1)
	}
}

func (p *Pane) matchRemoved(abs int) {
	s := &p.search
	if i, has := slices.BinarySearch(s.matches, abs); has {
		s.matches = slices.Delete(s.matches, i, i+1)
	}
}
