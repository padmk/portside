package logs

import (
	"regexp"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// Rule is a marker line the daemon writes into a server's output when it
// starts, stops or crashes ("── 01:24:42  stopped ──") or after the history
// it restored, shown as a full-width rule.
type Rule struct {
	// Label is the event with its time after it ("stopped 01:24:42").
	Label string
	// Failed marks a crash or a stop the server did not ask for.
	Failed bool
}

var ruleTime = regexp.MustCompile(`^[0-9]{2}:[0-9]{2}:[0-9]{2}$`)

// restored is the marker the daemon writes after the history it restored
// on start.
const restored = "daemon restarted"

// parseRule reads a marker line from its plain text, nil for other lines.
func parseRule(plain string) *Rule {
	if plain == "== "+restored+" ==" {
		return &Rule{Label: restored}
	}
	inner, ok := strings.CutPrefix(plain, "── ")
	if !ok {
		return nil
	}
	if inner, ok = strings.CutSuffix(inner, " ──"); !ok {
		return nil
	}
	inner = strings.TrimSpace(inner)
	if inner == "" {
		return nil
	}
	label := inner
	if at, event, ok := strings.Cut(inner, " "); ok && ruleTime.MatchString(at) {
		inner = strings.TrimSpace(event)
		label = inner + " " + at
	}
	failed := strings.HasPrefix(inner, "crashed") || strings.HasPrefix(inner, "stopped:") ||
		strings.HasPrefix(inner, "exited during") || strings.HasSuffix(inner, ", stopping")
	return &Rule{Label: label, Failed: failed}
}

// ruleRow draws a rule of glyph across width cells with its label in the
// middle.
func ruleRow(label string, width int, glyph, ellipsis string) string {
	if width <= 0 {
		return ""
	}
	if glyph == "" {
		glyph = "-"
	}
	label = ansi.Truncate(" "+label+" ", max(0, width-2), ellipsis)
	lw := ansi.StringWidth(label)
	left := (width - lw) / 2
	return strings.Repeat(glyph, left) + label + strings.Repeat(glyph, width-lw-left)
}

// ownStamp matches a line that begins with its own time: "15:04:05" with
// an optional fraction, an ISO 8601 date-time, or either in brackets.
var ownStamp = regexp.MustCompile(`^\[?(?:[0-9]{4}-[0-9]{2}-[0-9]{2}[T ])?[0-9]{2}:[0-9]{2}:[0-9]{2}(?:[.,][0-9]+)?`)

const (
	// stampSample is how many recent lines decide whether the time
	// column is shown.
	stampSample = 50
	// stampMin is how many lines are needed to decide.
	stampMin = 8
	// stampScan bounds how far back the sample is looked for.
	stampScan = 1000
)

// stampDecision is the automatic choice of the time column, made once per
// server shown so the column does not come and go as lines arrive.
type stampDecision struct {
	decided, show bool
}

// decideStamps makes the automatic choice once there are stampMin
// non-empty lines other than rules among the latest: the column is shown
// when at least half of the sample has a time from the daemon and less
// than 80% begin with a time of their own.
func (p *Pane) decideStamps() {
	if p.stamps.decided {
		return
	}
	seen, timed, own := 0, 0, 0
	n := p.buf.Len()
	for i := n - 1; i >= max(0, n-stampScan) && seen < stampSample; i-- {
		l := p.buf.At(i)
		text := strings.TrimLeft(l.Plain, " ")
		if l.Rule != nil || text == "" {
			continue
		}
		seen++
		if !l.At.IsZero() {
			timed++
		}
		if ownStamp.MatchString(text) {
			own++
		}
	}
	if seen < stampMin {
		return
	}
	p.stamps = stampDecision{decided: true, show: timed*2 >= seen && own*5 < seen*4}
}

// showStamps reports whether the time column is shown: as `t` chose for
// the server, else as decided, hidden until then.
func (p *Pane) showStamps() bool {
	switch p.gutter {
	case gutterShown:
		return true
	case gutterHidden:
		return false
	}
	return p.stamps.decided && p.stamps.show
}
