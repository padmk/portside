package server

import (
	"fmt"
	"net/http"
	"regexp"
	"strconv"
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
type logQuery struct {
	lines   int
	match   *regexp.Regexp
	context int
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
	return q, nil
}

// writeLines answers with the lines q selects as plain text. Long lines are
// cut, and the oldest lines are dropped to fit the reply size; notes go
// first, in brackets, so a reader knows what it is not seeing.
func writeLines(w http.ResponseWriter, q logQuery, lines []string, notes ...string) {
	out := q.pick(lines)
	for i, l := range out {
		if len(l) > maxLineBytes {
			out[i] = l[:maxLineBytes] + fmt.Sprintf(" [line cut, %d more bytes]", len(l)-maxLineBytes)
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

// pick returns the trailing lines, or the trailing matches with their
// context, separating gaps with "--" as grep does.
func (q logQuery) pick(lines []string) []string {
	if q.match == nil {
		if len(lines) > q.lines {
			lines = lines[len(lines)-q.lines:]
		}
		return append([]string(nil), lines...)
	}
	var hits []int
	for i, l := range lines {
		if q.match.MatchString(l) {
			hits = append(hits, i)
		}
	}
	if len(hits) > q.lines {
		hits = hits[len(hits)-q.lines:]
	}
	var out []string
	next := 0 // first line not yet written
	for _, h := range hits {
		from, to := max(h-q.context, next), min(h+q.context, len(lines)-1)
		if to < from {
			continue
		}
		if from > next && len(out) > 0 {
			out = append(out, "--")
		}
		out = append(out, lines[from:to+1]...)
		next = to + 1
	}
	return out
}
