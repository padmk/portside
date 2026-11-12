package detail

import (
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/padmk/portside/internal/client"
	"github.com/padmk/portside/internal/config"
	"github.com/padmk/portside/internal/server"
	"github.com/padmk/portside/internal/supervisor"
	"github.com/padmk/portside/internal/tui/keymap"
	"github.com/padmk/portside/internal/tui/logs"
	"github.com/padmk/portside/internal/tui/theme"
	"github.com/padmk/portside/internal/tui/ui"
)

var now = time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)

func testEnv(views ...supervisor.View) *ui.Env {
	started := now.Add(-5 * time.Minute)
	if len(views) == 0 {
		views = []supervisor.View{{Name: "web", Status: supervisor.Running, Enabled: true, StartedAt: &started, Port: 3000, Cmd: "npm run dev", DirDisplay: "~/shop/web"}}
	}
	return &ui.Env{
		Client:   client.New(1),
		Styles:   theme.New(theme.TerminalFamily.Dark),
		Glyphs:   theme.Unicode,
		Keys:     keymap.Default(),
		State:    &server.State{Config: server.ConfigInfo{PathDisplay: "~/portside.toml", Revision: "r1"}, Servers: views},
		Selected: ui.ServerItem(views[0].Name),
		Target:   ui.ServerItem(views[0].Name),
		Now:      func() time.Time { return now },
	}
}

func newTestPane(t *testing.T, env *ui.Env) *Pane {
	t.Helper()
	p := New(logs.New())
	t.Cleanup(p.Close)
	p.Update(env, nil)
	return p
}

// sgrOrLink matches the sequences the renderer itself adds.
var sgrOrLink = regexp.MustCompile(`\x1b\[[0-9;:]*m|\x1b\]8;[^\x1b\a]*(\x1b\\|\a)`)

func plain(s string) string { return ansi.Strip(s) }

func TestDetailsNeutralizeControlsAndWrap(t *testing.T) {
	v := supervisor.View{
		Name: "web", Status: supervisor.Stopped, Enabled: true,
		Cmd:        "sh -c 'echo one\n  echo \x1b[2Jtwo\u202e'",
		DirDisplay: "~/" + strings.Repeat("deep/", 12) + "web",
	}
	env := testEnv(v)
	p := newTestPane(t, env)
	p.tab = tabDetails
	out := p.View(env, 50, 30, true)
	if rest := sgrOrLink.ReplaceAllString(out, ""); strings.ContainsAny(rest, "\x1b\u202e\x07") {
		t.Fatalf("control or format characters reached the terminal: %q", rest)
	}
	rows := strings.Split(plain(out), "\n")
	var cmdRows, dirRows []string
	for i, r := range rows {
		if strings.Contains(r, "Command") {
			cmdRows = rows[i : i+2]
		}
		if strings.Contains(r, "Directory") {
			dirRows = rows[i : i+2]
		}
	}
	if len(cmdRows) == 0 || !strings.Contains(cmdRows[0], "echo one") || !strings.Contains(cmdRows[1], "echo �[2Jtwo�") {
		t.Fatalf("command rows = %q", cmdRows)
	}
	for _, r := range append(cmdRows[1:], dirRows[1:]...) {
		if strings.Contains(r, "Command") || strings.Contains(r, "Directory") {
			t.Errorf("label repeated on a continuation row: %q", r)
		}
	}
	if len(dirRows) < 2 || !strings.Contains(dirRows[1], "deep/") {
		t.Errorf("long directory did not wrap: %q", dirRows)
	}
	// Every rendered row is counted, so scrolling reaches the last one.
	filled, gaps := 0, 0
	for _, r := range rows[1 : len(rows)-1] {
		if strings.Trim(r, "│ ") != "" {
			filled++
		}
	}
	for _, o := range p.lists[tabDetails].owners {
		if o < 0 {
			gaps++
		}
	}
	if got := len(p.lists[tabDetails].owners) - gaps; got != filled {
		t.Errorf("%d rows counted, %d drawn", got, filled)
	}
}

func TestDetailsCursorCopiesValue(t *testing.T) {
	env := testEnv()
	p := newTestPane(t, env)
	p.tab = tabDetails
	p.View(env, 60, 20, true)
	for p.lists[tabDetails].fields[p.lists[tabDetails].cursor].label != "Command" {
		before := p.lists[tabDetails].cursor
		p.Do(env, keymap.Action{ID: "down"})
		if p.lists[tabDetails].cursor == before {
			t.Fatal("no Command field reached")
		}
		p.View(env, 60, 20, true)
	}
	msgs := run(p.Do(env, keymap.Action{ID: "copy"}))
	var copied bool
	for _, m := range msgs {
		if n, ok := m.(ui.NoticeMsg); ok && strings.Contains(n.Text, "npm run dev") {
			copied = true
		}
	}
	if !copied {
		t.Errorf("copy messages = %#v", msgs)
	}
}

// run collects the messages of cmd, batches flattened.
func run(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	if b, ok := msg.(tea.BatchMsg); ok {
		var out []tea.Msg
		for _, c := range b {
			out = append(out, run(c)...)
		}
		return out
	}
	return []tea.Msg{msg}
}

func TestTabStripWidths(t *testing.T) {
	for _, tc := range []struct {
		w          int
		name       string
		want, nope []string
	}{
		{100, "storefront", []string{"● storefront", "Logs", "Details", "Env", "Metrics", "running · 5 min"}, nil},
		{60, "storefront", []string{"● storefront", "Logs", "Details", "Env", "Metrics", "running"}, []string{"5 min"}},
		{40, "web", []string{"● web", "Logs", "Details", "Env", "Metrics"}, nil},
		{40, "storefront", []string{"● storefront", "Log", "Det", "Met"}, []string{"Details"}},
		{24, "storefront", []string{"‹", "Details", "›"}, []string{"Metrics"}},
	} {
		started := now.Add(-5 * time.Minute)
		env := testEnv(supervisor.View{Name: tc.name, Status: supervisor.Running, StartedAt: &started})
		p := newTestPane(t, env)
		p.tab = tabDetails
		top := strings.Split(p.View(env, tc.w, 10, false), "\n")[0]
		if got := ansi.StringWidth(top); got != tc.w {
			t.Errorf("%d: top border is %d cells", tc.w, got)
		}
		text := plain(top)
		for _, s := range tc.want {
			if !strings.Contains(text, s) {
				t.Errorf("%d: %q lacks %q", tc.w, text, s)
			}
		}
		for _, s := range tc.nope {
			if strings.Contains(text, s) {
				t.Errorf("%d: %q has %q", tc.w, text, s)
			}
		}
		// Clicking a tab's title opens it.
		for _, h := range p.strip.hits {
			if h.tab == tabMetrics || h.step == 1 {
				p.Update(env, tea.MouseClickMsg(tea.Mouse{X: h.x, Y: 0, Button: tea.MouseLeft}))
				break
			}
		}
		want := tabMetrics
		if tc.w == 24 {
			want = tabEnv // the arrow steps one tab
		}
		if p.tab != want {
			t.Errorf("%d: click opened tab %d, want %d", tc.w, p.tab, want)
		}
	}
}

func TestMetricsCharts(t *testing.T) {
	vals := []float64{0, 50, 100}
	levels := theme.ASCII.Chart.(theme.Levels)
	got := chart(levels, vals, 5, 2, 1)
	want := []string{"    #", "  _##"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("levels chart = %q, want %q", got, want)
	}
	braille := chart(theme.Braille{}, vals, 2, 1, 1)
	if len(braille) != 1 || ansi.StringWidth(braille[0]) != 2 || braille[0] == "\u2800\u2800" {
		t.Errorf("braille chart = %q", braille)
	}

	started := now.Add(-time.Minute)
	v := supervisor.View{Name: "web", Status: supervisor.Running, StartedAt: &started, CPU: 12, Mem: 64 << 20,
		CPUHistory: []float64{5, 30, 12}, MemHistory: []int64{60 << 20, 64 << 20, 64 << 20}}
	env := testEnv(v)
	env.Glyphs = theme.ASCII
	out := plain(strings.Join(Metrics(env, v, 2*time.Second, 40, 20), "\n"))
	for _, s := range []string{"CPU  12%", "peak 30%", "Memory  64 MB", "peak 64 MB", "a sample every 2 sec", strings.Repeat("-", 40)} {
		if !strings.Contains(out, s) {
			t.Errorf("metrics lack %q:\n%s", s, out)
		}
	}
	if strings.ContainsAny(out, "─…\u2800") {
		t.Errorf("ASCII metrics hold Unicode:\n%s", out)
	}

	for _, tc := range []struct {
		status supervisor.Status
		want   string
	}{{supervisor.Running, "first sample comes within 2 sec"}, {supervisor.Stopped, "Not running, so no samples"}} {
		v := supervisor.View{Name: "web", Status: tc.status}
		if got := plain(Metrics(testEnv(v), v, 2*time.Second, 80, 3)[0]); !strings.Contains(got, tc.want) {
			t.Errorf("%s: empty metrics = %q", tc.status, got)
		}
	}
}

func TestEnvSourceAndStale(t *testing.T) {
	env := testEnv()
	p := newTestPane(t, env)
	if cmd := p.Do(env, keymap.Action{ID: "tab-next"}); cmd != nil {
		t.Fatal("Details fetched something")
	}
	if cmd := p.Do(env, keymap.Action{ID: "tab-next"}); cmd == nil {
		t.Fatal("opening Env fetched no spec")
	}
	p.View(env, 80, 20, true)
	if cmd := p.Do(env, keymap.Action{ID: "tab-next"}); cmd != nil {
		t.Fatal("drawing or leaving Env fetched again")
	}
	p.Do(env, keymap.Action{ID: "tab-prev"})
	spec := config.Spec{Name: "web", Env: map[string]string{"API_TOKEN": "hunter2", "MODE": "dev\x1b]0;pwned\a"}}
	p.Update(env, specMsg{gen: p.specGen, server: "web", spec: spec})
	out := plain(p.View(env, 80, 20, true))
	for _, s := range []string{"from ~/portside.toml · revision r1 · loaded", "secrets hidden", "API_TOKEN = ••••••"} {
		if !strings.Contains(out, s) {
			t.Errorf("Env lacks %q:\n%s", s, out)
		}
	}
	if strings.Contains(out, "hunter2") || strings.Contains(out, "\a") {
		t.Errorf("Env shows a secret or a control:\n%q", out)
	}

	// A new revision fetches again; its failure keeps the values, marked
	// stale.
	env.State.Config.Revision = "r2"
	if cmd := p.Update(env, nil); cmd == nil {
		t.Fatal("a new revision fetched nothing")
	}
	p.Update(env, specMsg{gen: p.specGen, server: "web", err: errors.New("daemon unreachable")})
	out = plain(p.View(env, 80, 20, true))
	for _, s := range []string{"Refresh failed: daemon unreachable", "revision r1", "stale", "MODE"} {
		if !strings.Contains(out, s) {
			t.Errorf("stale Env lacks %q:\n%s", s, out)
		}
	}
}
