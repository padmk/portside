package servers

import (
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/padmk/portside/internal/server"
	"github.com/padmk/portside/internal/supervisor"
	"github.com/padmk/portside/internal/tui/theme"
	"github.com/padmk/portside/internal/tui/ui"
)

var now = time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)

// testEnv is an env on the Terminal palette with a fixed clock, the
// servers selected as the root model does on its first snapshot.
func testEnv(g theme.Glyphs, groups []string, servers ...supervisor.View) *ui.Env {
	env := &ui.Env{
		Styles:  theme.New(theme.TerminalFamily.Dark),
		Glyphs:  g,
		State:   &server.State{Groups: groups, Servers: servers},
		Pending: map[ui.Selection]string{},
		Now:     func() time.Time { return now },
	}
	if len(servers) > 0 {
		env.Selected = ui.ServerItem(servers[0].Name)
		env.Target = env.Selected
	}
	return env
}

func ptr[T any](v T) *T { return &v }

// awkward are rows that strain the layout: long, wide and combining
// names, five-digit ports, huge memory, many badges.
func awkward() []supervisor.View {
	ago := now.Add(-3 * time.Minute)
	return []supervisor.View{
		{Name: "a-really-long-server-name-that-never-fits", Group: "shop", Port: 54321, Enabled: true, Status: supervisor.Running, Mem: 900 << 40, CPUHistory: []float64{1, 5, 3}},
		{Name: "disabled-worker", Group: "shop", Port: 3000, Status: supervisor.Stopped},
		{Name: "日本語のサーバー名前です", Group: "shop", Port: 8080, Enabled: true, Status: supervisor.Running, Unready: true, NetworkWarning: true, Watch: []string{"*.go"}, Stale: true, PID: 7},
		{Name: "café-api", Group: "shop", Port: 65535, Enabled: true, Status: supervisor.Crashed, ExitCode: ptr(137), ExitedAt: &ago, Temporary: true},
		{Name: "👩‍💻-dev", Port: 5173, Enabled: true, Status: supervisor.Exited, Oneshot: true, Done: true, ExitedAt: &ago},
	}
}

func TestRowsFitTheirWidthAndKeepLampNameAndPort(t *testing.T) {
	for _, g := range theme.GlyphSets() {
		for _, w := range []int{34, 38, 46, 60} {
			env := testEnv(g, []string{"shop"}, awkward()...)
			p := New()
			rows := p.rows(env, false)
			c := layout(env, rows, w)
			for _, r := range rows {
				line := renderRow(env, env.Styles, r, c, w)
				if got := ansi.StringWidth(line); got > w {
					t.Errorf("%s %d: row is %d cells: %q", g.ID, w, got, ansi.Strip(line))
				}
				if r.header {
					continue
				}
				text := ansi.Strip(line)
				v := r.view
				if port := ":" + strconv.Itoa(v.Port); !strings.Contains(text, port) {
					t.Errorf("%s %d: %s lost its port: %q", g.ID, w, v.Name, text)
				}
				if lamp := g.State(v.Display(), 0); !strings.HasPrefix(strings.TrimLeft(text, " "), lamp) {
					t.Errorf("%s %d: %s does not start with its lamp: %q", g.ID, w, v.Name, text)
				}
				if c.name < min(minName, ansi.StringWidth(v.Name)) {
					t.Errorf("%s %d: name column %d is under the minimum", g.ID, w, c.name)
				}
				if !v.Enabled && !strings.Contains(text, g.Disabled) {
					t.Errorf("%s %d: the disabled mark is gone: %q", g.ID, w, text)
				}
				if v.Stale && !strings.Contains(text, g.Stale) {
					t.Errorf("%s %d: the stale mark is gone: %q", g.ID, w, text)
				}
			}
			// The pane draws every line at exactly its width.
			pw := w + 4
			for i, l := range strings.Split(p.View(env, pw, 12, true), "\n") {
				if got := ansi.StringWidth(l); got != pw {
					t.Errorf("%s %d: pane line %d is %d cells: %q", g.ID, w, i, got, ansi.Strip(l))
				}
			}
		}
	}
}

func TestPortsLineUpAcrossGroups(t *testing.T) {
	env := testEnv(theme.Unicode, []string{"shop"},
		supervisor.View{Name: "web", Group: "shop", Port: 3000, Enabled: true, Status: supervisor.Running},
		supervisor.View{Name: "blog", Port: 54321, Enabled: true, Status: supervisor.Stopped},
	)
	p := New()
	rows := p.rows(env, false)
	c := layout(env, rows, 50)
	end := -1
	for _, r := range rows {
		if r.header {
			continue
		}
		text := ansi.Strip(renderRow(env, env.Styles, r, c, 50))
		port := ":" + strconv.Itoa(r.view.Port)
		e := ansi.StringWidth(text[:strings.Index(text, port)+len(port)])
		if end >= 0 && e != end {
			t.Errorf("port of %s ends at %d, want %d", r.view.Name, e, end)
		}
		end = e
	}
}

func TestGroupCountKeepsTasksApart(t *testing.T) {
	env := testEnv(theme.Unicode, nil)
	for _, c := range []struct {
		name    string
		servers []supervisor.View
		want    string
	}{
		{"services", []supervisor.View{{Status: supervisor.Running}, {Status: supervisor.Stopped}}, "1/2"},
		{"tasks", []supervisor.View{{Oneshot: true, Done: true, Status: supervisor.Exited}, {Oneshot: true, Status: supervisor.Stopped}}, "✓ 1/2"},
		{"mixed", []supervisor.View{{Status: supervisor.Running}, {Oneshot: true, Done: true, Status: supervisor.Exited}}, "1/1 · ✓ 1/1"},
		{"crash", []supervisor.View{{Status: supervisor.Crashed}, {Status: supervisor.Running}}, "◆ 1 · 1/2"},
	} {
		var b strings.Builder
		for _, s := range groupCount(env, env.Styles, c.servers) {
			b.WriteString(s.text)
		}
		if got := b.String(); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}

func TestFilterMatchesTheDisplayedState(t *testing.T) {
	unready := supervisor.View{Name: "api", Status: supervisor.Running, Unready: true}
	if !matches(unready, []string{"unready"}) || matches(unready, []string{"running"}) {
		t.Error("the filter does not match the state the lamp shows")
	}
	if !inStates(unready, []supervisor.Display{supervisor.DisplayUnready}) || inStates(unready, []supervisor.Display{supervisor.Display(supervisor.Running)}) {
		t.Error("inStates does not use the display state")
	}
	if !matches(supervisor.View{Name: "web", Port: 3000}, []string{":3000"}) {
		t.Error("the filter does not match the port")
	}
}

func TestSparklineAlphabets(t *testing.T) {
	if got := sparkline(theme.ASCII.Spark, []float64{0, 1, 2, 4}, 4); got != "_.-#" {
		t.Errorf("levels = %q", got)
	}
	if got := sparkline(theme.ASCII.Spark, []float64{2, 4}, 4); got != "  -#" {
		t.Errorf("short levels = %q", got)
	}
	got := sparkline(theme.Unicode.Spark, []float64{1}, 4)
	if strings.ContainsRune(got, '⠀') || ansi.StringWidth(got) != 4 {
		t.Errorf("braille = %q, want spaces before the first sample", got)
	}
}

func TestCursorAndPositionShowTheSelection(t *testing.T) {
	var servers []supervisor.View
	for i := range 22 {
		servers = append(servers, supervisor.View{Name: fmt.Sprintf("srv-%02d", i), Enabled: true, Status: supervisor.Stopped})
	}
	for _, mono := range []bool{false, true} {
		env := testEnv(theme.Unicode, nil, servers...)
		if mono {
			env.Styles = theme.NewMono(theme.TerminalFamily.Dark)
		}
		p := New()
		lines := strings.Split(p.View(env, 30, 10, true), "\n")
		side := env.Glyphs.PaneBorder(true, env.Styles.Mono).Left
		if got := ansi.Strip(lines[1]); !strings.HasPrefix(got, side+theme.Unicode.Cursor+"○ srv-00") {
			t.Errorf("mono %v: selected row %q has no cursor", mono, got)
		}
		if mono && !strings.Contains(lines[1], "\x1b[7m") {
			t.Error("mono: the selected row is not reversed")
		}
		if ansi.Strip(lines[2])[len(side):][0] != ' ' {
			t.Errorf("an unselected row has a cursor: %q", ansi.Strip(lines[2]))
		}
		if bottom := ansi.Strip(lines[len(lines)-1]); !strings.Contains(bottom, " 1/22 ") {
			t.Errorf("mono %v: bottom border %q has no position", mono, bottom)
		}
	}
}

func TestLongFilterKeepsItsCaret(t *testing.T) {
	env := testEnv(theme.Unicode, nil, supervisor.View{Name: "web"})
	p := New()
	p.filtering, p.query = true, strings.Repeat("日本", 20)
	lines := strings.Split(p.View(env, 24, 8, true), "\n")
	prompt := ansi.Strip(lines[len(lines)-2])
	if !strings.Contains(prompt, theme.Unicode.Caret) || ansi.StringWidth(lines[len(lines)-2]) != 24 {
		t.Errorf("filter line %q lost its caret or width", prompt)
	}
}
