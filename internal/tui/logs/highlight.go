package logs

import (
	"regexp"
	"strings"
	"unicode/utf8"

	"charm.land/lipgloss/v2"
	"github.com/padmk/portside/internal/supervisor"
	"github.com/padmk/portside/internal/tui/theme"
)

var levels = regexp.MustCompile(`(?i)\b(ERROR|ERR|FATAL|PANIC|WARN|WARNING|INFO|DEBUG|TRACE)\b`)
var methods = regexp.MustCompile(`\b(GET|POST|PUT|PATCH|DELETE|HEAD|OPTIONS)\b`)
var statuses = regexp.MustCompile(`\b(?:GET|POST|PUT|PATCH|DELETE|HEAD|OPTIONS)\s+\S+\s+([2-5][0-9]{2})\b|\bstatus=([2-5][0-9]{2})\b`)
var durations = regexp.MustCompile(`\b[0-9]+(?:\.[0-9]+)?(?:ms|us|µs|s|m)\b`)
var urls = regexp.MustCompile(`https?://[^\s<>"']+|\b[a-zA-Z0-9.-]+\.localhost:[0-9]+(?:/[^\s<>"']*)?`)
var files = regexp.MustCompile(`\b[a-zA-Z0-9_./-]+\.[a-zA-Z0-9_]+:[0-9]+(?::[0-9]+)?\b`)

// patternMargin is how far before and after the shown part of a line
// the built-in patterns look, so that a word cut by the window is still
// recognized; tokens longer than that are styled only from the margin on.
const patternMargin = 256

// Highlight styles recognized text and search matches.
func Highlight(s theme.Styles, l Line, match *regexp.Regexp) string {
	return highlight(s, l, match, false, 0, len(l.Plain))
}

// highlight renders the part of a line from byte from to byte to of its
// plain text, with recognized text styled, search matches marked and the
// matches of the current search match's line in reverse. The line's own
// colors and links in effect at from come first. The work is linear in
// the line's length and its rendering in the part shown; a style is
// measured only when the part uses it.
func highlight(s theme.Styles, l Line, match *regexp.Regexp, current bool, from, to int) string {
	from, to = max(0, min(from, len(l.Plain))), max(0, min(to, len(l.Plain)))
	if from > to {
		from = to
	}
	matchStyle := s.Match
	if current {
		matchStyle = matchStyle.Reverse(true)
	}
	// owner[i] is the style of plain byte from+i: 0 for none, otherwise an
	// index into styles plus one; a later span wins where spans overlap.
	owner := make([]int32, to-from)
	var styles []styled
	cover := func(start, end int, st styled) {
		start, end = max(start, from), min(end, to)
		if start >= end {
			return
		}
		styles = append(styles, st)
		id := int32(len(styles))
		for i := start; i < end; i++ {
			owner[i-from] = id
		}
	}
	if !l.Color {
		// Built-in patterns look at the shown part and a margin around it.
		wf, wt := runeStart(l.Plain, from-patternMargin), runeStart(l.Plain, to+patternMargin)
		window := l.Plain[wf:wt]
		var kinds [kindCount]*wrapper
		find := func(re *regexp.Regexp, kind int) {
			for _, p := range re.FindAllStringIndex(window, -1) {
				cover(wf+p[0], wf+p[1], styled{kind: kind})
			}
		}
		for _, p := range levels.FindAllStringIndex(window, -1) {
			kind := kindMuted
			switch strings.ToUpper(window[p[0]:p[1]]) {
			case "ERROR", "ERR", "FATAL", "PANIC":
				kind = kindError
			case "WARN", "WARNING":
				kind = kindWarn
			case "INFO":
				kind = kindInfo
			}
			cover(wf+p[0], wf+p[1], styled{kind: kind})
		}
		find(methods, kindInfo)
		find(durations, kindMuted)
		find(files, kindPort)
		for _, p := range statuses.FindAllStringSubmatchIndex(window, -1) {
			start, end := p[2], p[3]
			if start < 0 {
				start, end = p[4], p[5]
			}
			kind := kindRunning
			switch window[start] {
			case '3':
				kind = kindInfo
			case '4':
				kind = kindWarnStatus
			case '5':
				kind = kindErrorStatus
			}
			cover(wf+start, wf+end, styled{kind: kind})
		}
		for _, p := range urls.FindAllStringIndex(window, -1) {
			u := window[p[0]:p[1]]
			target := u
			if !strings.HasPrefix(u, "http") {
				target = "http://" + u
			}
			cover(wf+p[0], wf+p[1], styled{kind: kindLink, link: target})
		}
		for i := range styles {
			k := styles[i].kind
			if kinds[k] == nil {
				w := newWrapper(spanStyle(s, k))
				kinds[k] = &w
			}
			styles[i].wrap = *kinds[k]
		}
	}
	matched := make([]bool, to-from)
	if match != nil {
		for _, p := range match.FindAllStringIndex(l.Plain, -1) {
			for i := max(p[0], from); i < min(p[1], to); i++ {
				matched[i-from] = true
			}
		}
	}
	var mark wrapper
	if match != nil {
		mark = newWrapper(matchStyle)
	}

	// Walk the line's text, which is the plain text with the line's own
	// escape sequences between its bytes; pos is the plain offset.
	text := l.Text
	var out, before strings.Builder
	pos := 0
	activeSGR := ""
	for len(text) > 0 && pos < to {
		loc := escape.FindStringIndex(text)
		if loc != nil && loc[0] == 0 {
			seq := text[:loc[1]]
			if pos < from {
				before.WriteString(seq)
			} else {
				out.WriteString(seq)
			}
			if seq == "\x1b[0m" || seq == "\x1b[m" {
				activeSGR = ""
			} else if sgr.MatchString(seq) {
				activeSGR = supervisor.CompactSequences(activeSGR + seq)
			}
			text = text[loc[1]:]
			continue
		}
		end := len(text)
		if loc != nil {
			end = loc[0]
		}
		chunk := text[:end]
		text = text[end:]
		if skip := min(from-pos, len(chunk)); skip > 0 {
			chunk, pos = chunk[skip:], pos+skip
		}
		if pos == from && before.Len() > 0 {
			out.WriteString(supervisor.CompactSequences(before.String()))
			before.Reset()
		}
		chunk = chunk[:min(len(chunk), to-pos)]
		for len(chunk) > 0 {
			o, m := owner[pos-from], matched[pos-from]
			n := 1
			for n < len(chunk) && owner[pos-from+n] == o && matched[pos-from+n] == m {
				n++
			}
			piece := chunk[:n]
			if o != 0 {
				st := styles[o-1]
				piece = st.wrap.render(piece)
				if st.link != "" {
					piece = "\x1b]8;;" + st.link + "\x1b\\" + piece + "\x1b]8;;\x1b\\"
				}
			}
			if m {
				piece = mark.render(piece)
				if l.Color {
					piece += activeSGR
				}
			}
			out.WriteString(piece)
			chunk, pos = chunk[n:], pos+n
		}
	}
	return out.String()
}

// runeStart is byte i of s moved back to the start of its character,
// within s.
func runeStart(s string, i int) int {
	i = max(0, min(i, len(s)))
	for i > 0 && i < len(s) && !utf8.RuneStart(s[i]) {
		i--
	}
	return i
}

// The kinds of recognized text.
const (
	kindError = iota
	kindWarn
	kindErrorStatus
	kindWarnStatus
	kindInfo
	kindMuted
	kindPort
	kindRunning
	kindLink
	kindCount
)

// spanStyle is the style of a kind of recognized text.
func spanStyle(s theme.Styles, kind int) lipgloss.Style {
	switch kind {
	case kindError:
		return s.Error.Bold(true)
	case kindWarn:
		return s.Warn.Bold(true)
	case kindErrorStatus:
		return s.Error
	case kindWarnStatus:
		return s.Warn
	case kindInfo:
		return s.Info
	case kindMuted:
		return s.Muted
	case kindPort:
		return s.Port
	case kindRunning:
		return s.Running
	}
	return s.Link
}

// styled is the style of a span of recognized text.
type styled struct {
	kind int
	link string
	wrap wrapper
}

// wrapper renders text in a style by the escape sequences the style puts
// around a piece of text, or around each character for styles such as
// underline that lipgloss renders one character at a time, measured once;
// a style that renders text some other way is rendered each time.
type wrapper struct {
	style    lipgloss.Style
	pre, suf string
	mode     wrapMode
}

type wrapMode int

const (
	wrapWhole wrapMode = iota
	wrapEachRune
	wrapRendering
)

func newWrapper(st lipgloss.Style) wrapper {
	one := st.Render("a")
	i := strings.IndexByte(one, 'a')
	if i < 0 || strings.Count(one, "a") != 1 {
		return wrapper{style: st, mode: wrapRendering}
	}
	w := wrapper{style: st, pre: one[:i], suf: one[i+1:]}
	switch probe := st.Render("a b"); probe {
	case w.pre + "a b" + w.suf:
		w.mode = wrapWhole
	case w.pre + "a" + w.suf + w.pre + " " + w.suf + w.pre + "b" + w.suf:
		w.mode = wrapEachRune
	default:
		w.mode = wrapRendering
	}
	return w
}

func (w wrapper) render(text string) string {
	switch w.mode {
	case wrapWhole:
		return w.pre + text + w.suf
	case wrapEachRune:
		var b strings.Builder
		b.Grow(len(text) * (1 + len(w.pre) + len(w.suf)))
		for _, r := range text {
			b.WriteString(w.pre)
			b.WriteRune(r)
			b.WriteString(w.suf)
		}
		return b.String()
	}
	return w.style.Render(text)
}
