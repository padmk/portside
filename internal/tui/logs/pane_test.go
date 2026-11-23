package logs

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/padmk/portside/internal/server"
	"github.com/padmk/portside/internal/supervisor"
	"github.com/padmk/portside/internal/tui/keymap"
	"github.com/padmk/portside/internal/tui/theme"
	"github.com/padmk/portside/internal/tui/ui"
)

var testNow = time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)

func testEnv() *ui.Env {
	return &ui.Env{Styles: theme.New(theme.TerminalFamily.Variant(true)), Glyphs: theme.Unicode,
		Now: func() time.Time { return testNow }}
}

// testPane is a pane showing server x at w x h with the scrollback.
func testPane(t *testing.T, w, h int, backlog string, times []Line) (*Pane, *ui.Env) {
	t.Helper()
	env := testEnv()
	p := New()
	p.server = "x"
	p.loading = true
	p.apply(env, feedMsg{reset: true, backlog: []byte(backlog), times: times})
	p.View(env, w, h, true)
	return p, env
}

func screen(p *Pane, env *ui.Env, w, h int) []string {
	rows := strings.Split(ansi.Strip(p.View(env, w, h, true)), "\n")
	for i, r := range rows {
		rows[i] = strings.TrimRight(r, " ")
	}
	return rows
}

func plains(p *Pane) []string {
	var out []string
	for i := range p.buf.Len() {
		out = append(out, p.buf.At(i).Plain)
	}
	return out
}

func numbered(prefix string, n int) string {
	var b strings.Builder
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, "%s %d\n", prefix, i)
	}
	return b.String()
}

func do(p *Pane, env *ui.Env, ids ...string) {
	for _, id := range ids {
		p.Do(env, keymap.Action{ID: id})
	}
}

func TestTimeColumnDecision(t *testing.T) {
	timed := func(text string) []Line {
		var out []Line
		for i, l := range strings.Split(strings.TrimSuffix(text, "\n"), "\n") {
			out = append(out, Clean(l, testNow.Add(time.Duration(i)*time.Second)))
		}
		return out
	}
	plain := numbered("line", 30)
	own := numbered("12:00:01 INFO line", 30)
	cases := []struct {
		name    string
		backlog string
		times   []Line
		show    bool
	}{
		{"no daemon times", plain, nil, false},
		{"daemon times", plain, timed(plain), true},
		{"own times", own, timed(own), false},
		{"times of other lines", plain, timed(numbered("other", 30)), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p, _ := testPane(t, 60, 10, c.backlog, c.times)
			if p.showStamps() != c.show {
				t.Fatalf("showStamps() = %v, want %v", p.showStamps(), c.show)
			}
		})
	}

	t.Run("stable as lines arrive", func(t *testing.T) {
		p, env := testPane(t, 60, 10, plain, timed(plain))
		p.apply(env, feedMsg{data: []byte(numbered("12:00:01 own", 200))})
		if !p.showStamps() {
			t.Fatal("the column went away as lines with their own times arrived")
		}
		do(p, env, "timestamps")
		if p.showStamps() {
			t.Fatal("t did not hide the column")
		}
	})

	t.Run("hidden until decided", func(t *testing.T) {
		p, env := testPane(t, 60, 10, "", nil)
		p.apply(env, feedMsg{data: []byte("a\nb\n")})
		if p.showStamps() {
			t.Fatal("the column is shown before enough lines decide it")
		}
		if got := screen(p, env, 60, 10)[0]; got != "a" {
			t.Fatalf("first row = %q, want the line without a gutter", got)
		}
	})
}

func TestDateLinesJoinsTheReply(t *testing.T) {
	backlog := "x\n" + numbered("same", 30)
	// The reply was read later: it ends with a line that came after.
	reply := strings.Split(strings.TrimSuffix(backlog+"later\n", "\n"), "\n")
	var times []Line
	for i, l := range reply {
		times = append(times, Clean(l, testNow.Add(time.Duration(i)*time.Second)))
	}
	p, _ := testPane(t, 60, 10, backlog, times)
	for i := range p.buf.Len() {
		if want := testNow.Add(time.Duration(i) * time.Second); !p.buf.At(i).At.Equal(want) {
			t.Fatalf("line %d %q dated %v, want %v", i, p.buf.At(i).Plain, p.buf.At(i).At, want)
		}
	}
}

func TestLiveIngest(t *testing.T) {
	cases := []struct {
		name    string
		backlog string
		chunks  []string
		want    []string
	}{
		{"utf-8 split across chunks", "", []string{"caf\xc3", "\xa9 ok\n"}, []string{"café ok"}},
		{"color split across chunks", "", []string{"\x1b[3", "1mred\x1b[0m\n"}, []string{"red"}},
		{"repeated lines are kept", "a\na\n", []string{"a\n", "a\n"}, []string{"a", "a", "a", "a"}},
		{"unfinished line extended", "", []string{"wait", "ing", " done\nnext"}, []string{"waiting done", "next"}},
		{
			"compose redraws its free rows",
			"\n\n",
			[]string{
				"\x1b[2A\x1b[0Gapp | one\n\x1b[0K\n\x1b[0K\n",
				"\x1b[2A\x1b[0Gapp | two\n\x1b[0K\n\x1b[0K\n",
				"\x1b[2A\x1b[0Gapp | three\n\x1b[0K\n\x1b[0K\n",
			},
			[]string{"app | one", "app | two", "app | three", "", ""},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p, env := testPane(t, 60, 10, c.backlog, nil)
			for _, chunk := range c.chunks {
				p.apply(env, feedMsg{data: []byte(chunk)})
			}
			if got := plains(p); strings.Join(got, "|") != strings.Join(c.want, "|") {
				t.Fatalf("lines = %q, want %q", got, c.want)
			}
			if l := p.buf.At(0); c.name == "color split across chunks" && l.Text != "\x1b[31mred\x1b[0m" {
				t.Fatalf("text = %q, want the color kept", l.Text)
			}
		})
	}
}

func TestOverflowShowsGap(t *testing.T) {
	f := &feed{ready: make(chan struct{}, 1)}
	line := strings.Repeat("é", 100) + "\n"
	for f.gap == false {
		f.addData([]byte(line), false)
	}
	if !bytes.HasPrefix(f.data, []byte("é")) || len(f.data) > maxPending {
		t.Fatalf("kept %d bytes starting %q, want at most %d from a line start", len(f.data), f.data[:4], maxPending)
	}
	p, env := testPane(t, 60, 10, "before\nhalf", nil)
	p.apply(env, feedMsg{data: []byte("after\n"), gap: true})
	if got := plains(p); strings.Join(got, "|") != "before|half|"+gapLabel+"|after" {
		t.Fatalf("lines = %q", got)
	}
}

func TestTallWrappedLine(t *testing.T) {
	var words []string
	for i := range 300 {
		words = append(words, fmt.Sprintf("w%03d", i))
	}
	p, env := testPane(t, 40, 6, "head\n"+strings.Join(words, " ")+"\ntail\n", nil)
	do(p, env, "wrap", "top")
	if got := screen(p, env, 40, 6); got[0] != "head" || !strings.HasPrefix(got[1], "w000 w001") {
		t.Fatalf("top shows %q, want head then the first row of the long line", got)
	}
	do(p, env, "bottom", "up", "up")
	got := screen(p, env, 40, 6)
	if last := got[5]; strings.Contains(last, "tail") || !strings.Contains(last, "w29") {
		t.Fatalf("two rows up shows %q at the bottom, want the long line's second last row", got)
	}
	before := screen(p, env, 40, 6)[0]
	do(p, env, "page-up", "down")
	if after := screen(p, env, 40, 6); after[5] != before {
		t.Fatalf("a page up should move by exactly the screen's rows: bottom %q a row later, was top %q", after[5], before)
	}
}

func TestSearchRevealsMatchRow(t *testing.T) {
	var words []string
	for i := range 300 {
		words = append(words, fmt.Sprintf("w%03d", i))
	}
	p, env := testPane(t, 40, 6, "head\n"+strings.Join(words, " ")+"\ntail\n", nil)
	do(p, env, "wrap")
	p.setQuery("w150")
	p.jumpMatch(1)
	if got := strings.Join(screen(p, env, 40, 6), "\n"); !strings.Contains(got, "w150") {
		t.Fatalf("the match is not on screen:\n%s", got)
	}
	if !strings.Contains(p.Status(), "1/1") || !strings.HasPrefix(p.Status(), "PAUSED") {
		t.Fatalf("Status() = %q", p.Status())
	}
}

func TestStatusPieces(t *testing.T) {
	p, env := testPane(t, 60, 10, numbered("ERROR line", 20), nil)
	if got := p.Status(); got != "follow" {
		t.Fatalf("Status() = %q", got)
	}
	do(p, env, "up", "wrap")
	p.setQuery("error")
	p.apply(env, feedMsg{data: []byte("ERROR new\nok\n")})
	if got, want := p.Status(), "PAUSED · 2 new · search error 21 matches · wrap"; got != want {
		t.Fatalf("Status() = %q, want %q", got, want)
	}
	do(p, env, "next-match")
	if got := p.Status(); !strings.Contains(got, "PAUSED · 2 new · search error ") || !strings.Contains(got, "/21") {
		t.Fatalf("Status() = %q, want the search beside PAUSED", got)
	}
	do(p, env, "keep-matching", "bottom")
	if got, want := p.Status(), "follow · filter error (21) · wrap"; got != want {
		t.Fatalf("Status() = %q, want %q", got, want)
	}
	env.Glyphs = theme.ASCII
	p.View(env, 60, 10, true)
	if got := p.Status(); got != "follow, filter error (21), wrap" {
		t.Fatalf("ASCII Status() = %q", got)
	}
}

func TestRingEviction(t *testing.T) {
	p, env := testPane(t, 60, 4, numbered("line", 10), nil)
	p.buf = NewBuffer(10)
	p.resetLines()
	p.apply(env, feedMsg{data: []byte(numbered("line", 10))})
	p.setQuery("line 3")
	do(p, env, "up")
	p.startVisual(env)
	p.moveSelection(-1)
	p.apply(env, feedMsg{data: []byte(numbered("more", 5))})
	if p.base != 5 || p.buf.At(0).Plain != "line 6" {
		t.Fatalf("base %d, first %q", p.base, p.buf.At(0).Plain)
	}
	if len(p.search.matches) != 0 {
		t.Fatalf("matches of dropped lines kept: %v", p.search.matches)
	}
	if p.vstart < p.base || p.vcur < p.base || p.anchor < p.base {
		t.Fatalf("selection %d..%d or anchor %d before the first line %d", p.vstart, p.vcur, p.anchor, p.base)
	}
	if got := screen(p, env, 60, 4); len(got) != 4 {
		t.Fatalf("screen %q", got)
	}
	p.setQuery("more")
	if len(p.search.matches) != 5 {
		t.Fatalf("matches = %v", p.search.matches)
	}
}

func TestReadStateKeptPerServer(t *testing.T) {
	p, env := testPane(t, 60, 5, numbered("line", 50), nil)
	do(p, env, "up", "up", "up")
	p.setQuery("line 4")
	want := screen(p, env, 60, 5)
	p.memory.save("x", p.readState())
	p.server = ""
	p.Show(env, "y")
	p.Close()
	st, ok := p.memory.take("x")
	if !ok {
		t.Fatal("no read state kept for x")
	}
	q := New()
	q.server, q.loading, q.restore = "x", true, &st
	q.apply(env, feedMsg{reset: true, backlog: []byte(numbered("line", 50) + "line 51\n")})
	if got := screen(q, env, 60, 5); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("restored screen %q, want %q", got, want)
	}
	if q.search.query != "line 4" || q.follow {
		t.Fatalf("query %q, follow %v", q.search.query, q.follow)
	}
}

func TestScrollSideways(t *testing.T) {
	p, env := testPane(t, 20, 3, "0123456789abcdefghijklmnopqrstuvwxyz\nshort\n", nil)
	do(p, env, "scroll-right")
	got := screen(p, env, 20, 3)
	if got[0] != "…6789abcdefghijklmn…" || got[1] != "…" {
		t.Fatalf("scrolled screen %q", got)
	}
	if !strings.HasSuffix(p.Status(), "col 6") {
		t.Fatalf("Status() = %q", p.Status())
	}
	do(p, env, "scroll-left")
	if got := screen(p, env, 20, 3); got[0] != "0123456789abcdefghi…" {
		t.Fatalf("back at the start %q", got)
	}
}

// TestFeedsRouteToTheirPane: two panes that opened their first feed each
// (a container's Logs and Job tabs) take only their own feed's messages,
// interleaved, and only the owner schedules the next read.
func TestFeedsRouteToTheirPane(t *testing.T) {
	env := testEnv()
	open := func(server string) *Pane {
		p := New()
		p.server, p.loading = server, true
		p.feed = newFeed(t.Context())
		t.Cleanup(p.Close)
		return p
	}
	logsPane, jobPane := open(containerKey+"db"), open(jobKey+"db\x00job")
	if logsPane.feed.id == jobPane.feed.id {
		t.Fatal("two panes' feeds share an id")
	}
	msgs := []struct {
		from *Pane
		msg  feedMsg
	}{
		{logsPane, feedMsg{reset: true, data: []byte("CONTAINER_ONLY 1\n")}},
		{jobPane, feedMsg{reset: true, data: []byte("$ docker compose up\n")}},
		{jobPane, feedMsg{data: []byte("[+] up 0/1\n")}},
		{logsPane, feedMsg{data: []byte("CONTAINER_ONLY 2\n")}},
	}
	for _, m := range msgs {
		m.msg.id = m.from.feed.id
		for _, p := range []*Pane{logsPane, jobPane} {
			cmd := p.Update(env, m.msg)
			if owner := p == m.from; (cmd != nil) != owner {
				t.Fatalf("pane %q: next read scheduled = %v for a message of %q", p.server, cmd != nil, m.from.server)
			}
		}
	}
	if got := strings.Join(plains(logsPane), "|"); got != "CONTAINER_ONLY 1|CONTAINER_ONLY 2" {
		t.Errorf("Logs lines = %q", got)
	}
	if got := strings.Join(plains(jobPane), "|"); got != "$ docker compose up|[+] up 0/1" {
		t.Errorf("Job lines = %q", got)
	}
}

// TestSettleClosesTheFeedKeepingLines: an ended job's pane closes its
// feed once its scrollback arrived and keeps the output.
func TestSettleClosesTheFeedKeepingLines(t *testing.T) {
	env := testEnv()
	p := New()
	p.server, p.loading = jobKey+"db\x00job", true
	p.feed = newFeed(t.Context())
	if cmd := p.Settle(); cmd != nil {
		t.Fatal("a loading pane waits for its scrollback, not a timer")
	}
	id := p.feed.id
	if cmd := p.Update(env, feedMsg{id: id, reset: true, data: []byte("done\n")}); cmd != nil || p.feed != nil {
		t.Fatalf("feed still open after the scrollback of an ended job (next read %v)", cmd != nil)
	}
	if got := strings.Join(plains(p), "|"); got != "done" {
		t.Errorf("lines = %q", got)
	}
}

// An empty log of a stopped server names a way to start it that works
// from where the keys are: space in the servers list, the command line
// in the focused pane, where space does nothing.
func TestEmptyLogNamesAWorkingStart(t *testing.T) {
	env := testEnv()
	env.State = &server.State{Servers: []supervisor.View{{Name: "my app", Status: supervisor.Stopped}}}
	p := New()
	p.server = "my app"
	for _, c := range []struct {
		focused bool
		want    string
	}{{false, "stopped · space starts it"}, {true, `stopped · :start "my app"`}} {
		if got := ansi.Strip(p.View(env, 60, 5, c.focused)); !strings.Contains(got, c.want) {
			t.Errorf("focused %v: no %q in\n%s", c.focused, c.want, got)
		}
	}
}

// A reconnect or a clear drops the lines under a search prompt or a
// selection; the pane must give the keys back to the root model, or the
// session stays in SEARCH or SELECT with nothing to search or select.
func TestDroppedLinesEndTheInputMode(t *testing.T) {
	enter := map[string]func(*testing.T, *Pane, *ui.Env){
		"search": func(_ *testing.T, p *Pane, env *ui.Env) { p.startSearch(env) },
		"select": func(t *testing.T, p *Pane, env *ui.Env) {
			if !p.startVisual(env) {
				t.Fatal("nothing to select")
			}
		},
	}
	drop := map[string]func(*Pane, *ui.Env){
		"reconnect": func(p *Pane, env *ui.Env) {
			p.apply(env, feedMsg{reset: true, backlog: []byte(numbered("new", 3))})
		},
		"clear": func(p *Pane, env *ui.Env) { p.Update(env, ui.LogsClearedMsg{Server: "x"}) },
	}
	for mode, start := range enter {
		for event, end := range drop {
			t.Run(mode+" then "+event, func(t *testing.T) {
				p, env := testPane(t, 60, 6, numbered("line", 5), nil)
				start(t, p, env)
				if m, ok := env.TakeMode(); !ok || m == ui.ModeNormal {
					t.Fatalf("%s did not enter a mode", mode)
				}
				end(p, env)
				if m, ok := env.TakeMode(); !ok || m != ui.ModeNormal {
					t.Fatalf("after %s the pane asked for mode %v (asked %v), want NORMAL", event, m, ok)
				}
				if p.editing || p.visual {
					t.Fatalf("after %s editing %v, visual %v", event, p.editing, p.visual)
				}
			})
		}
	}
}
