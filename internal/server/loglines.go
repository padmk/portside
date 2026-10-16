package server

import (
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/padmk/portside/internal/supervisor"
)

// Limits that keep a plain logs reply readable for an agent.
const (
	maxLineBytes  = 4 << 10
	maxReplyBytes = 256 << 10
	maxContext    = 20
)

// logQuery is what a plain logs request asks for: the last ?lines= lines
// (200 by default), or with ?match= the last that many lines matching a
// case-insensitive regular expression, each with ?context= lines around it.
// ?since= (a duration such as 10m, or an RFC 3339 time) keeps later lines;
// ?timestamps=1 prefixes each line with its local time.
type logQuery struct {
	lines   int
	match   *regexp.Regexp
	context int
	since   time.Time
	times   bool
}

func parseLogQuery(r *http.Request) (logQuery, error) {
	v := r.URL.Query()
	q := logQuery{lines: 200}
	if n, _ := strconv.Atoi(v.Get("lines")); n > 0 {
		q.lines = n
	}
	if m := v.Get("match"); m != "" {
		if _, err := regexp.Compile(m); err != nil {
			return q, fmt.Errorf("invalid match pattern: %w", err)
		}
		q.match = regexp.MustCompile("(?i)" + m)
	}
	if n, _ := strconv.Atoi(v.Get("context")); n > 0 {
		q.context = min(n, maxContext)
	}
	if since := v.Get("since"); since != "" {
		if d, err := time.ParseDuration(since); err == nil && d > 0 {
			q.since = time.Now().Add(-d)
		} else if t, err := time.Parse(time.RFC3339, since); err == nil {
			q.since = t
		} else {
			return q, fmt.Errorf("invalid since %q: use a duration such as 10m or an RFC 3339 time", since)
		}
	}
	q.times = v.Get("timestamps") == "1"
	return q, nil
}

// writeLines answers with the lines q selects as plain text. Long lines are
// cut, and the oldest lines are dropped to fit the reply size; notes go
// first, in brackets, so a reader knows what it is not seeing.
func writeLines(w http.ResponseWriter, q logQuery, all []supervisor.Line, notes ...string) {
	now := time.Now()
	if !q.since.IsZero() {
		from := len(all)
		for from > 0 && !all[from-1].At.IsZero() && !all[from-1].At.Before(q.since) {
			from--
		}
		all = all[from:]
	}
	if q.times {
		notes = append([]string{"times are local; now " + now.Format("2006-01-02 15:04:05")}, notes...)
	}
	var out []string
	for _, i := range q.pick(all) {
		switch {
		case i < 0:
			out = append(out, "--")
		case q.times:
			out = append(out, stamp(all[i].At, now)+" "+all[i].Text)
		default:
			out = append(out, all[i].Text)
		}
	}
	for i, l := range out {
		if len(l) > maxLineBytes {
			k := maxLineBytes
			for k > 0 && !utf8.RuneStart(l[k]) {
				k--
			}
			out[i] = l[:k] + fmt.Sprintf(" [line cut, %d more bytes]", len(l)-k)
		}
	}
	size, from := 0, len(out)
	for from > 0 && size+len(out[from-1])+1 <= maxReplyBytes {
		from--
		size += len(out[from]) + 1
	}
	if from > 0 {
		notes = append(notes, fmt.Sprintf("%d earlier lines left out to keep the reply under %d KiB", from, maxReplyBytes>>10))
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	for _, n := range notes {
		fmt.Fprintf(w, "[%s]\n", n)
	}
	for _, l := range out[from:] {
		fmt.Fprintln(w, l)
	}
}

// pick returns the indexes of the trailing lines, or of the trailing
// matches with their context, with -1 where a gap is, as grep shows "--".
func (q logQuery) pick(lines []supervisor.Line) []int {
	var out []int
	if q.match == nil {
		for i := max(len(lines)-q.lines, 0); i < len(lines); i++ {
			out = append(out, i)
		}
		return out
	}
	var hits []int
	for i, l := range lines {
		if q.match.MatchString(l.Text) {
			hits = append(hits, i)
		}
	}
	if len(hits) > q.lines {
		hits = hits[len(hits)-q.lines:]
	}
	next := 0 // first line not yet included
	for _, h := range hits {
		from, to := max(h-q.context, next), min(h+q.context, len(lines)-1)
		if to < from {
			continue
		}
		if from > next && len(out) > 0 {
			out = append(out, -1)
		}
		for i := from; i <= to; i++ {
			out = append(out, i)
		}
		next = to + 1
	}
	return out
}

// stamp formats a line's time compactly: the clock for today, with the date
// before that, and blanks when unknown.
func stamp(at, now time.Time) string {
	switch {
	case at.IsZero():
		return "        "
	case at.Year() == now.Year() && at.YearDay() == now.YearDay():
		return at.Format("15:04:05")
	}
	return at.Format("01-02 15:04:05")
}
