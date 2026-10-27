package server

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/padmk/portside/internal/config"
	"github.com/padmk/portside/internal/supervisor"
)

func query(t *testing.T, rawQuery string) logQuery {
	t.Helper()
	q, err := parseLogQuery(httptest.NewRequest("GET", "/x?"+rawQuery, nil))
	if err != nil {
		t.Fatalf("parseLogQuery(%q): %v", rawQuery, err)
	}
	return q
}

func mkLines(texts ...string) []supervisor.Line {
	out := make([]supervisor.Line, len(texts))
	for i, s := range texts {
		out[i].Text = s
	}
	return out
}

func render(q logQuery, lines []supervisor.Line, notes ...string) (string, *httptest.ResponseRecorder) {
	rec := httptest.NewRecorder()
	writeLines(rec, q, lines, notes...)
	return rec.Body.String(), rec
}

func TestParseLogQueryDefaults(t *testing.T) {
	q := query(t, "")
	if q.lines != 200 || q.match != nil || q.context != 0 || !q.since.IsZero() || q.times {
		t.Errorf("defaults = %+v", q)
	}
}

func TestParseLogQueryLines(t *testing.T) {
	cases := map[string]int{
		"lines=50": 50, "lines=1": 1, "lines=100000": 100000,
		"lines=0": 200, "lines=-3": 200, "lines=abc": 200, "lines=": 200, "lines=1.5": 200,
	}
	for in, want := range cases {
		if got := query(t, in).lines; got != want {
			t.Errorf("%s: lines = %d, want %d", in, got, want)
		}
	}
}

func TestParseLogQueryMatch(t *testing.T) {
	q := query(t, "match=error|warn")
	for _, s := range []string{"ERROR: boom", "a Warning", "xx wArN"} {
		if !q.match.MatchString(s) {
			t.Errorf("match does not find %q", s)
		}
	}
	if q.match.MatchString("fine") {
		t.Error("match found a line without the pattern")
	}
	for _, bad := range []string{"match=(", "match=[a-", "match=*x", "match=a%7B2,1%7D"} {
		if _, err := parseLogQuery(httptest.NewRequest("GET", "/x?"+bad, nil)); err == nil {
			t.Errorf("%s: no error", bad)
		} else if !strings.Contains(err.Error(), "invalid match pattern") {
			t.Errorf("%s: error = %v", bad, err)
		}
	}
}

func TestParseLogQueryContext(t *testing.T) {
	cases := map[string]int{
		"context=3": 3, "context=20": 20, "context=21": 20, "context=9999": 20,
		"context=0": 0, "context=-1": 0, "context=x": 0,
	}
	for in, want := range cases {
		if got := query(t, in).context; got != want {
			t.Errorf("%s: context = %d, want %d", in, got, want)
		}
	}
}

func TestParseLogQuerySinceDuration(t *testing.T) {
	before := time.Now()
	q := query(t, "since=10m")
	after := time.Now()
	if q.since.Before(before.Add(-10*time.Minute)) || q.since.After(after.Add(-10*time.Minute)) {
		t.Errorf("since = %v, want about ten minutes ago", q.since)
	}
	if got := query(t, "since=1h30m").since; time.Since(got) < 89*time.Minute || time.Since(got) > 91*time.Minute {
		t.Errorf("since = %v", got)
	}
}

func TestParseLogQuerySinceTime(t *testing.T) {
	q := query(t, "since=2024-05-06T07:08:09Z")
	if want := time.Date(2024, 5, 6, 7, 8, 9, 0, time.UTC); !q.since.Equal(want) {
		t.Errorf("since = %v, want %v", q.since, want)
	}
	q = query(t, "since=2024-05-06T07:08:09%2B02:00")
	if want := time.Date(2024, 5, 6, 5, 8, 9, 0, time.UTC); !q.since.Equal(want) {
		t.Errorf("since with offset = %v, want %v", q.since, want)
	}
}

func TestParseLogQuerySinceInvalid(t *testing.T) {
	for _, in := range []string{"since=yesterday", "since=10", "since=-5m", "since=0s", "since=2024-05-06", "since=10x"} {
		_, err := parseLogQuery(httptest.NewRequest("GET", "/x?"+in, nil))
		if err == nil {
			t.Errorf("%s: no error", in)
		} else if !strings.Contains(err.Error(), "invalid since") {
			t.Errorf("%s: error = %v", in, err)
		}
	}
}

func TestParseLogQueryTimestamps(t *testing.T) {
	if !query(t, "timestamps=1").times {
		t.Error("timestamps=1 not set")
	}
	for _, in := range []string{"timestamps=0", "timestamps=true", "timestamps="} {
		if query(t, in).times {
			t.Errorf("%s turned timestamps on", in)
		}
	}
}

func TestPickTail(t *testing.T) {
	lines := mkLines("0", "1", "2", "3", "4")
	cases := []struct {
		n    int
		want []int
	}{
		{2, []int{3, 4}},
		{5, []int{0, 1, 2, 3, 4}},
		{50, []int{0, 1, 2, 3, 4}},
		{1, []int{4}},
	}
	for _, c := range cases {
		if got := (logQuery{lines: c.n}).pick(lines); !slices.Equal(got, c.want) {
			t.Errorf("tail %d = %v, want %v", c.n, got, c.want)
		}
	}
	if got := (logQuery{lines: 3}).pick(nil); len(got) != 0 {
		t.Errorf("tail of nothing = %v", got)
	}
}

func TestPickMatch(t *testing.T) {
	lines := mkLines("a", "ERR", "b", "c", "d", "e", "ERR", "f")
	re := regexp.MustCompile("(?i)err")
	cases := []struct {
		name string
		q    logQuery
		want []int
	}{
		{"no context", logQuery{lines: 10, match: re}, []int{1, -1, 6}},
		{"context with gap", logQuery{lines: 10, match: re, context: 1}, []int{0, 1, 2, -1, 5, 6, 7}},
		{"contexts touch", logQuery{lines: 10, match: re, context: 2}, []int{0, 1, 2, 3, 4, 5, 6, 7}},
		{"context past ends", logQuery{lines: 10, match: re, context: 20}, []int{0, 1, 2, 3, 4, 5, 6, 7}},
		{"last hit only", logQuery{lines: 1, match: re, context: 1}, []int{5, 6, 7}},
		{"no hits", logQuery{lines: 10, match: regexp.MustCompile("zzz"), context: 2}, nil},
	}
	for _, c := range cases {
		if got := c.q.pick(lines); !slices.Equal(got, c.want) {
			t.Errorf("%s: pick = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestPickMatchOverlappingContexts(t *testing.T) {
	lines := mkLines("x0", "hit", "x2", "hit", "x4", "x5", "x6", "x7", "x8", "hit")
	re := regexp.MustCompile("hit")
	// The hits at 1 and 3 share lines 1 to 3 and are merged without a gap.
	got := (logQuery{lines: 10, match: re, context: 2}).pick(lines)
	want := []int{0, 1, 2, 3, 4, 5, -1, 7, 8, 9}
	if !slices.Equal(got, want) {
		t.Errorf("pick = %v, want %v", got, want)
	}
	// A hit that lies inside the previous hit's context adds nothing.
	lines = mkLines("0", "hit", "hit", "3")
	got = (logQuery{lines: 10, match: re, context: 3}).pick(lines)
	if !slices.Equal(got, []int{0, 1, 2, 3}) {
		t.Errorf("nested hits: pick = %v", got)
	}
	// No index is ever repeated.
	seen := map[int]bool{}
	for _, i := range (logQuery{lines: 10, match: re, context: 5}).pick(lines) {
		if seen[i] {
			t.Errorf("index %d picked twice", i)
		}
		seen[i] = true
	}
}

func TestPickMatchUsesLastHits(t *testing.T) {
	var texts []string
	for i := range 10 {
		texts = append(texts, fmt.Sprintf("hit %d", i), "filler")
	}
	got := (logQuery{lines: 2, match: regexp.MustCompile("hit")}).pick(mkLines(texts...))
	if !slices.Equal(got, []int{16, -1, 18}) {
		t.Errorf("pick = %v", got)
	}
}

func TestWriteLinesTail(t *testing.T) {
	body, rec := render(logQuery{lines: 2}, mkLines("a", "b", "c", "d"))
	if body != "c\nd\n" {
		t.Errorf("body = %q", body)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/plain; charset=utf-8" {
		t.Errorf("Content-Type = %q", ct)
	}
	if rec.Code != 200 {
		t.Errorf("status = %d", rec.Code)
	}
}

func TestWriteLinesEmpty(t *testing.T) {
	body, _ := render(logQuery{lines: 5}, nil)
	if body != "" {
		t.Errorf("body = %q", body)
	}
	body, _ = render(logQuery{lines: 5}, nil, "no output yet")
	if body != "[no output yet]\n" {
		t.Errorf("body with note = %q", body)
	}
}

func TestWriteLinesNotes(t *testing.T) {
	body, _ := render(logQuery{lines: 5}, mkLines("a"), "first", "second")
	if body != "[first]\n[second]\na\n" {
		t.Errorf("body = %q", body)
	}
}

func TestWriteLinesMatchWithContext(t *testing.T) {
	lines := mkLines("boot", "Error: one", "after", "quiet", "quiet", "quiet", "before", "ERROR two", "end")
	q := query(t, "match=error&context=1&lines=10")
	body, _ := render(q, lines)
	want := "boot\nError: one\nafter\n--\nbefore\nERROR two\nend\n"
	if body != want {
		t.Errorf("body = %q, want %q", body, want)
	}
}

func TestWriteLinesSince(t *testing.T) {
	now := time.Now()
	lines := []supervisor.Line{
		{At: now.Add(-3 * time.Hour), Text: "old"},
		{At: now.Add(-90 * time.Minute), Text: "older than since"},
		{At: now.Add(-30 * time.Minute), Text: "recent"},
		{At: now.Add(-1 * time.Minute), Text: "newest"},
	}
	body, _ := render(logQuery{lines: 10, since: now.Add(-time.Hour)}, lines)
	if body != "recent\nnewest\n" {
		t.Errorf("body = %q", body)
	}
	body, _ = render(logQuery{lines: 10, since: now.Add(time.Minute)}, lines)
	if body != "" {
		t.Errorf("body with a future since = %q", body)
	}
	// A line exactly at since is kept.
	at := now.Add(-time.Hour)
	body, _ = render(logQuery{lines: 10, since: at}, []supervisor.Line{{At: at.Add(-time.Second), Text: "x"}, {At: at, Text: "edge"}})
	if body != "edge\n" {
		t.Errorf("body at the edge = %q", body)
	}
}

func TestWriteLinesSinceDropsLinesWithoutTime(t *testing.T) {
	now := time.Now()
	lines := []supervisor.Line{{Text: "undated"}, {At: now, Text: "dated"}}
	body, _ := render(logQuery{lines: 10, since: now.Add(-time.Hour)}, lines)
	if body != "dated\n" {
		t.Errorf("body = %q", body)
	}
}

func TestWriteLinesSinceThenTail(t *testing.T) {
	now := time.Now()
	var lines []supervisor.Line
	for i := range 10 {
		lines = append(lines, supervisor.Line{At: now.Add(time.Duration(i-10) * time.Minute), Text: fmt.Sprint("l", i)})
	}
	body, _ := render(logQuery{lines: 2, since: now.Add(-5*time.Minute - time.Second)}, lines)
	if body != "l8\nl9\n" {
		t.Errorf("body = %q", body)
	}
}

func TestWriteLinesTimestamps(t *testing.T) {
	now := time.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 12, 34, 56, 0, time.Local)
	old := time.Date(2020, 3, 4, 5, 6, 7, 0, time.Local)
	lines := []supervisor.Line{
		{At: old, Text: "ancient"},
		{Text: "unknown"},
		{At: today, Text: "today"},
	}
	body, _ := render(logQuery{lines: 10, times: true}, lines, "extra")
	got := strings.Split(strings.TrimSuffix(body, "\n"), "\n")
	if len(got) != 5 {
		t.Fatalf("body = %q", body)
	}
	if !regexp.MustCompile(`^\[times are local; now \d{4}-\d\d-\d\d \d\d:\d\d:\d\d\]$`).MatchString(got[0]) {
		t.Errorf("first note = %q", got[0])
	}
	want := []string{"[extra]", "03-04 05:06:07 ancient", "         unknown", "12:34:56 today"}
	if !slices.Equal(got[1:], want) {
		t.Errorf("lines = %q, want %q", got[1:], want)
	}
}

func TestWriteLinesTimestampsLeaveGapMarkerBare(t *testing.T) {
	now := time.Now()
	lines := []supervisor.Line{{At: now, Text: "hit"}, {At: now, Text: "x"}, {At: now, Text: "x"}, {At: now, Text: "hit"}}
	q := logQuery{lines: 10, times: true, match: regexp.MustCompile("hit")}
	body, _ := render(q, lines)
	got := strings.Split(strings.TrimSuffix(body, "\n"), "\n")
	if len(got) != 4 || got[2] != "--" || !strings.HasSuffix(got[1], " hit") || !strings.HasSuffix(got[3], " hit") {
		t.Errorf("body = %q", body)
	}
}

func TestWriteLinesNoTimestampNoteByDefault(t *testing.T) {
	body, _ := render(logQuery{lines: 5}, []supervisor.Line{{At: time.Now(), Text: "x"}})
	if body != "x\n" {
		t.Errorf("body = %q", body)
	}
}

func TestWriteLinesCutsLongLines(t *testing.T) {
	cases := []struct {
		name string
		line string
		want string
	}{
		{"ascii", strings.Repeat("a", maxLineBytes+904), strings.Repeat("a", maxLineBytes) + " [line cut, 904 more bytes]"},
		{"one byte over", strings.Repeat("a", maxLineBytes+1), strings.Repeat("a", maxLineBytes) + " [line cut, 1 more bytes]"},
		{"2-byte rune split", "x" + strings.Repeat("é", 3000), "x" + strings.Repeat("é", 2047) + " [line cut, 1906 more bytes]"},
		{"3-byte rune split", strings.Repeat("世", 2000), strings.Repeat("世", 1365) + " [line cut, 1905 more bytes]"},
		{"4-byte rune on the edge", strings.Repeat("😀", 2000), strings.Repeat("😀", 1024) + " [line cut, 3904 more bytes]"},
		{"exact limit", strings.Repeat("a", maxLineBytes), strings.Repeat("a", maxLineBytes)},
		{"short", "short", "short"},
	}
	for _, c := range cases {
		body, _ := render(logQuery{lines: 10}, mkLines("before", c.line))
		if want := "before\n" + c.want + "\n"; body != want {
			t.Errorf("%s: got %d bytes ending %q, want %d bytes ending %q", c.name, len(body), tail(body, 40), len(want), tail(want, 40))
		}
		if !utf8.ValidString(body) {
			t.Errorf("%s: body is not valid UTF-8", c.name)
		}
	}
}

func TestWriteLinesCutLineWithTimestamp(t *testing.T) {
	line := supervisor.Line{Text: strings.Repeat("a", maxLineBytes)}
	body, _ := render(logQuery{lines: 1, times: true}, []supervisor.Line{line})
	got := strings.Split(strings.TrimSuffix(body, "\n"), "\n")
	last := got[len(got)-1]
	// The stamp counts toward the limit.
	if len(last) > maxLineBytes+len(" [line cut, 99999 more bytes]") || !strings.Contains(last, "[line cut, 9 more bytes]") {
		t.Errorf("line = %d bytes ending %q", len(last), tail(last, 40))
	}
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}

func TestWriteLinesCapsReply(t *testing.T) {
	var lines []supervisor.Line
	for i := range 1000 {
		lines = append(lines, supervisor.Line{Text: fmt.Sprintf("%04d", i) + strings.Repeat("x", 996)})
	}
	body, _ := render(logQuery{lines: 1000}, lines, "note")
	// Each kept line costs 1001 bytes: 261 of them fit in 256 KiB.
	if !strings.HasPrefix(body, "[note]\n[739 earlier lines left out to keep the reply under 256 KiB]\n") {
		t.Errorf("body starts %q", body[:120])
	}
	got := strings.Split(strings.TrimSuffix(body, "\n"), "\n")
	kept := got[2:]
	if len(kept) != 261 || !strings.HasPrefix(kept[0], "0739") || !strings.HasPrefix(kept[260], "0999") {
		t.Errorf("kept %d lines from %q to %q", len(kept), kept[0][:4], kept[len(kept)-1][:4])
	}
	if size := len(body) - len(got[0]) - len(got[1]) - 2; size > maxReplyBytes {
		t.Errorf("lines take %d bytes, limit %d", size, maxReplyBytes)
	}
}

func TestWriteLinesUnderCapHasNoNote(t *testing.T) {
	var lines []supervisor.Line
	for range 200 {
		lines = append(lines, supervisor.Line{Text: strings.Repeat("y", 1000)})
	}
	body, _ := render(logQuery{lines: 200}, lines)
	if strings.Contains(body, "left out") || strings.Count(body, "\n") != 200 {
		t.Errorf("unexpected note or line count (%d newlines)", strings.Count(body, "\n"))
	}
}

func TestWriteLinesCapKeepsNewestEvenWhenLarge(t *testing.T) {
	// Every line is a maximal 4 KiB line plus its cut note.
	var lines []supervisor.Line
	for i := range 100 {
		lines = append(lines, supervisor.Line{Text: fmt.Sprintf("%03d", i) + strings.Repeat("z", maxLineBytes)})
	}
	body, _ := render(logQuery{lines: 100}, lines)
	if !strings.Contains(body, "earlier lines left out") || !strings.Contains(body, "099z") {
		t.Errorf("body lacks the note or the newest line")
	}
	if strings.Contains(body, "000z") {
		t.Error("oldest line survived the cap")
	}
}

func TestStampedLines(t *testing.T) {
	data := "2024-05-06T07:08:09.123456789Z first line\n" +
		"2024-05-06T07:08:10Z second  with  spaces\n" +
		"2024-05-06T09:08:11.5+02:00 offset stamp\n" +
		"no stamp here\n" +
		"2024-05-06T07:08:12Z \n" +
		"2024-05-06T07:08:13Z \x1b[31mcolored\x1b[0m\r\n" +
		"2024-05-06T07:08:14Z progress 1\rprogress 2\n" +
		"yesterday is not a stamp\n" +
		"2024-05-06T07:08:15Z last\n" +
		"2024-05-06T07:08:16Z \n" +
		"\n"
	got := stampedLines([]byte(data))
	type want struct {
		at   string
		text string
	}
	wants := []want{
		{"2024-05-06T07:08:09.123456789Z", "first line"},
		{"2024-05-06T07:08:10Z", "second  with  spaces"},
		{"2024-05-06T07:08:11.5Z", "offset stamp"},
		{"", "no stamp here"},
		{"2024-05-06T07:08:12Z", ""},
		{"2024-05-06T07:08:13Z", "colored"},
		{"2024-05-06T07:08:14Z", "progress 2"},
		{"", "yesterday is not a stamp"},
		{"2024-05-06T07:08:15Z", "last"},
	}
	if len(got) != len(wants) {
		t.Fatalf("got %d lines, want %d: %+v", len(got), len(wants), got)
	}
	for i, w := range wants {
		if got[i].Text != w.text {
			t.Errorf("line %d text = %q, want %q", i, got[i].Text, w.text)
		}
		if w.at == "" {
			if !got[i].At.IsZero() {
				t.Errorf("line %d has time %v, want none", i, got[i].At)
			}
			continue
		}
		at, err := time.Parse(time.RFC3339Nano, w.at)
		if err != nil {
			t.Fatal(err)
		}
		if !got[i].At.Equal(at) {
			t.Errorf("line %d at %v, want %v", i, got[i].At, at)
		}
		if got[i].At.Location() != time.Local {
			t.Errorf("line %d time is in %v, want local", i, got[i].At.Location())
		}
	}
}

func TestStampedLinesEmpty(t *testing.T) {
	for _, in := range []string{"", "\n", "2024-05-06T07:08:09Z \n"} {
		if got := stampedLines([]byte(in)); len(got) != 0 {
			t.Errorf("stampedLines(%q) = %+v", in, got)
		}
	}
}

func TestStampedLinesFeedWriteLines(t *testing.T) {
	data := "2020-03-04T05:06:07Z a\n2020-03-04T05:06:08Z b\n"
	body, _ := render(logQuery{lines: 10, since: time.Date(2020, 3, 4, 5, 6, 8, 0, time.UTC)}, stampedLines([]byte(data)))
	if body != "b\n" {
		t.Errorf("body = %q", body)
	}
}

func TestClearLogsUnknownServer(t *testing.T) {
	cfg := &config.Config{Servers: []config.Server{{Name: "web"}}}
	sup := supervisor.New(cfg, t.TempDir())
	defer sup.Shutdown()
	srv := &Server{sup: sup}
	for _, name := range []string{"missing", "web"} {
		req := httptest.NewRequest("DELETE", "/api/servers/"+name+"/logs", nil)
		req.SetPathValue("name", name)
		rec := httptest.NewRecorder()
		srv.clearLogs(rec, req)
		want := http.StatusOK
		if name == "missing" {
			want = http.StatusNotFound
		}
		if rec.Code != want {
			t.Fatalf("%s: status %d, want %d", name, rec.Code, want)
		}
	}
}
