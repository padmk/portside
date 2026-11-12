// Package viewfmt holds the text and order the CLI and the terminal UI
// show servers with, so both describe a server the same way.
package viewfmt

import (
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/padmk/portside/internal/paths"
	"github.com/padmk/portside/internal/server"
	"github.com/padmk/portside/internal/supervisor"
)

// Ordered lists the servers as the dashboard does: groups in order of
// appearance, ungrouped servers last.
func Ordered(st *server.State) []supervisor.View {
	ordered := make([]supervisor.View, 0, len(st.Servers))
	for _, g := range st.Groups {
		for _, s := range st.Servers {
			if s.Group == g {
				ordered = append(ordered, s)
			}
		}
	}
	for _, s := range st.Servers {
		if s.Group == "" {
			ordered = append(ordered, s)
		}
	}
	return ordered
}

// Group is one run of servers of the same group.
type Group struct {
	Name    string
	Servers []supervisor.View
}

// Groups splits Ordered into runs of one group each; ungrouped servers come
// last with an empty Name.
func Groups(st *server.State) []Group {
	var groups []Group
	for _, s := range Ordered(st) {
		if len(groups) == 0 || groups[len(groups)-1].Name != s.Group {
			groups = append(groups, Group{Name: s.Group})
		}
		i := len(groups) - 1
		groups[i].Servers = append(groups[i].Servers, s)
	}
	return groups
}

// Note is the text after a server's status in lists: its state note, the
// worktree of a temporary copy and the next scheduled run.
func Note(s supervisor.View, now time.Time) string {
	note := StateNote(s)
	if s.Temporary {
		note = strings.TrimSpace("temporary in " + paths.ShortenHome(s.Worktree) + " " + note)
	}
	if s.NextRun != nil {
		note = strings.TrimSpace("next " + NextRunLabel(*s.NextRun, now) + " " + note)
	}
	return note
}

// StateNote is the part of Note about the server's state: its note, who
// holds its port, where it is forwarded from and a changed config.
func StateNote(s supervisor.View) string {
	note := s.Note
	if s.Done {
		note = "" // the status says it
	}
	if s.External != nil && s.PID == 0 {
		note = fmt.Sprintf("port held by %s (pid %d)", s.External.Command, s.External.PID)
	} else if s.ForwardedFrom != "" {
		note = "port forwarded from " + s.ForwardedFrom
	}
	if s.Stale {
		note = strings.TrimSpace(note + " config changed, restart to apply")
	}
	return note
}

// StatusWord is the status a list prints: the status, "external" when
// another process holds a stopped server's port, or "done".
func StatusWord(s supervisor.View) string {
	w := string(s.Status)
	if s.Status == supervisor.Stopped && s.External != nil {
		w = "external"
	}
	if s.Done {
		w = "done"
	}
	return w
}

// HumanBytes formats a memory size in KB, MB or GB.
func HumanBytes(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%d MB", n>>20)
	case n > 0:
		return fmt.Sprintf("%d KB", n>>10)
	}
	return ""
}

// NextRunLabel says concisely when a scheduled run is due: the time today,
// the weekday within a week, else the date (with the year when it differs).
func NextRunLabel(next, now time.Time) string {
	next, now = next.Local(), now.Local()
	y, m, d := now.Date()
	today := time.Date(y, m, d, 0, 0, 0, 0, time.Local)
	switch {
	case next.Before(today.AddDate(0, 0, 1)):
		return next.Format("15:04")
	case next.Before(today.AddDate(0, 0, 7)):
		return next.Format("Mon 15:04")
	case next.Year() == now.Year():
		return next.Format("2 Jan 15:04")
	}
	return next.Format("2 Jan 2006 15:04")
}

// PlainText makes text from the daemon (a command, a folder, an env
// value) safe to print in a terminal: every control character, newlines
// included, and every format character that can reorder or hide text
// (bidirectional overrides, invisible separators) becomes U+FFFD. The
// zero-width joiner and non-joiner stay, since emoji and scripts need them.
// Invalid UTF-8 becomes U+FFFD too, so no stray byte reads as a C1 control.
func PlainText(s string) string {
	clean := utf8.ValidString(s)
	for _, r := range s {
		if unsafeRune(r) {
			clean = false
			break
		}
	}
	if clean {
		return s
	}
	s = strings.ToValidUTF8(s, "\uFFFD")
	return strings.Map(func(r rune) rune {
		if unsafeRune(r) {
			return '\uFFFD'
		}
		return r
	}, s)
}

func unsafeRune(r rune) bool {
	if r == '\u200c' || r == '\u200d' {
		return false
	}
	return unicode.IsControl(r) || unicode.Is(unicode.Cf, r)
}

// PlainLines is PlainText for text that may span lines: it splits s at
// line breaks (\n, \r\n and \r), turns tabs into spaces and makes each line
// safe; a trailing line break adds no empty line.
func PlainLines(s string) []string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	s = strings.ReplaceAll(s, "\t", "    ")
	s = strings.TrimSuffix(s, "\n")
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = PlainText(l)
	}
	return lines
}

// Sparkline draws vals as braille of exactly width cells, two samples per
// cell, the newest at the right.
func Sparkline(vals []float64, width int) string {
	if width <= 0 {
		return ""
	}
	max := 1e-9
	for _, v := range vals {
		if v > max {
			max = v
		}
	}
	cells := make([]rune, width)
	for i := range cells {
		cells[i] = 0x2800
	}
	left := []rune{0x40, 0x04, 0x02, 0x01}
	right := []rune{0x80, 0x20, 0x10, 0x08}
	start := len(vals) - 2*width
	if start < 0 {
		start = 0
	}
	samples := vals[start:]
	offset := 2*width - len(samples)
	for i, v := range samples {
		rows := int(v / max * 4)
		if rows < 1 {
			rows = 1
		}
		if rows > 4 {
			rows = 4
		}
		pos := offset + i
		bits := left
		if pos%2 == 1 {
			bits = right
		}
		for j := 0; j < rows; j++ {
			cells[pos/2] |= bits[j]
		}
	}
	return string(cells)
}
