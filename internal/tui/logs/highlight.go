package logs

import (
	"regexp"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/padmk/portside/internal/tui/theme"
)

type span struct {
	start, end int
	style      lipgloss.Style
	link       string
}

var levels = regexp.MustCompile(`(?i)\b(ERROR|ERR|FATAL|PANIC|WARN|WARNING|INFO|DEBUG|TRACE)\b`)
var methods = regexp.MustCompile(`\b(GET|POST|PUT|PATCH|DELETE|HEAD|OPTIONS)\b`)
var statuses = regexp.MustCompile(`\b(?:GET|POST|PUT|PATCH|DELETE|HEAD|OPTIONS)\s+\S+\s+([2-5][0-9]{2})\b|\bstatus=([2-5][0-9]{2})\b`)
var durations = regexp.MustCompile(`\b[0-9]+(?:\.[0-9]+)?(?:ms|us|µs|s|m)\b`)
var urls = regexp.MustCompile(`https?://[^\s<>"']+|\b[a-zA-Z0-9.-]+\.localhost:[0-9]+(?:/[^\s<>"']*)?`)
var files = regexp.MustCompile(`\b[a-zA-Z0-9_./-]+\.[a-zA-Z0-9_]+:[0-9]+(?::[0-9]+)?\b`)

// Highlight styles recognized text and search matches.
func Highlight(s theme.Styles, l Line, match *regexp.Regexp) string {
	return highlight(s, l, match, false)
}

// highlight is Highlight with the matches of the current search match's
// line in reverse, so it stands out from the other matches.
func highlight(s theme.Styles, l Line, match *regexp.Regexp, current bool) string {
	matchStyle := s.Match
	if current {
		matchStyle = matchStyle.Reverse(true)
	}
	var spans []span
	add := func(re *regexp.Regexp, style lipgloss.Style) {
		for _, p := range re.FindAllStringIndex(l.Plain, -1) {
			spans = append(spans, span{start: p[0], end: p[1], style: style})
		}
	}
	if !l.Color {
		for _, p := range levels.FindAllStringIndex(l.Plain, -1) {
			var st lipgloss.Style
			switch strings.ToUpper(l.Plain[p[0]:p[1]]) {
			case "ERROR", "ERR", "FATAL", "PANIC":
				st = s.Error.Bold(true)
			case "WARN", "WARNING":
				st = s.Warn.Bold(true)
			case "INFO":
				st = s.Info
			default:
				st = s.Muted
			}
			spans = append(spans, span{start: p[0], end: p[1], style: st})
		}
		add(methods, s.Info)
		add(durations, s.Muted)
		add(files, s.Port)
		for _, p := range statuses.FindAllStringSubmatchIndex(l.Plain, -1) {
			start, end := p[2], p[3]
			if start < 0 {
				start, end = p[4], p[5]
			}
			st := s.Running
			switch l.Plain[start] {
			case '3':
				st = s.Info
			case '4':
				st = s.Warn
			case '5':
				st = s.Error
			}
			spans = append(spans, span{start: start, end: end, style: st})
		}
		for _, p := range urls.FindAllStringIndex(l.Plain, -1) {
			u := l.Plain[p[0]:p[1]]
			target := u
			if !strings.HasPrefix(u, "http") {
				target = "http://" + u
			}
			spans = append(spans, span{start: p[0], end: p[1], style: s.Link, link: target})
		}
	}
	var matches [][]int
	if match != nil {
		matches = match.FindAllStringIndex(l.Plain, -1)
	}
	// Preserve original escapes while applying styles to plain-text byte ranges.
	text := l.Text
	if !l.Color && len(escape.FindAllStringIndex(text, -1)) == 0 {
		text = l.Plain
	}
	var out strings.Builder
	pos := 0
	activeSGR := ""
	for len(text) > 0 {
		if loc := escape.FindStringIndex(text); loc != nil && loc[0] == 0 {
			seq := text[:loc[1]]
			out.WriteString(seq)
			if sgr.MatchString(seq) {
				if seq == "\x1b[0m" || seq == "\x1b[m" {
					activeSGR = ""
				} else {
					activeSGR += seq
				}
			}
			text = text[loc[1]:]
			continue
		}
		end := len(text)
		if loc := escape.FindStringIndex(text); loc != nil {
			end = loc[0]
		}
		chunk := text[:end]
		for len(chunk) > 0 {
			next := pos + len(chunk)
			var chosen *span
			matched := false
			for i := range spans {
				p := &spans[i]
				if p.start <= pos && pos < p.end {
					chosen = p
					if p.end < next {
						next = p.end
					}
				} else if p.start > pos && p.start < next {
					next = p.start
				}
			}
			for _, p := range matches {
				if p[0] <= pos && pos < p[1] {
					matched = true
					if p[1] < next {
						next = p[1]
					}
				} else if p[0] > pos && p[0] < next {
					next = p[0]
				}
			}
			n := next - pos
			piece := chunk[:n]
			if chosen != nil {
				piece = chosen.style.Render(piece)
				if chosen.link != "" {
					piece = "\x1b]8;;" + chosen.link + "\x1b\\" + piece + "\x1b]8;;\x1b\\"
				}
			}
			if matched {
				piece = matchStyle.Render(piece)
				if l.Color {
					piece += activeSGR
				}
			}
			out.WriteString(piece)
			chunk = chunk[n:]
			pos = next
		}
		text = text[end:]
	}
	return out.String()
}
