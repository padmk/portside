package detail

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/padmk/portside/internal/client"
	"github.com/padmk/portside/internal/config"
	"github.com/padmk/portside/internal/supervisor"
	"github.com/padmk/portside/internal/tui/keymap"
	"github.com/padmk/portside/internal/tui/logs"
	"github.com/padmk/portside/internal/tui/ui"
	"github.com/padmk/portside/internal/viewfmt"
)

// specMsg carries a server's configured spec for the Env tab, from the
// fetch numbered gen.
type specMsg struct {
	gen    int
	server string
	spec   config.Spec
	err    error
}

// specState is what the Env tab knows of the shown server's spec: the
// last one loaded with its revision and time, kept while a refresh fails,
// and the revision of the latest fetch.
type specState struct {
	server string
	rev    string // asked for
	spec   config.Spec
	loaded bool
	// loadedRev and loadedAt describe spec.
	loadedRev string
	loadedAt  time.Time
	err       string
}

// Pane is the right pane: the selected server's tabs in one frame, with
// its state in the top border and the log mode or the position at the
// bottom right.
type Pane struct {
	logs  *logs.Pane
	tab   int
	shown string
	spec  specState
	// specGen numbers the spec fetches; a reply from an older one is
	// dropped.
	specGen int
	// lists are the Details and Env tabs' fields, cursor and scroll.
	lists [tabCount]fieldList
	// strip is the top border with the tabs.
	strip Strip
}

// New returns the right pane around a log pane.
func New(logPane *logs.Pane) *Pane { return &Pane{logs: logPane} }

// Close ends the log stream.
func (c *Pane) Close() { c.logs.Close() }

// Logs is the Logs tab's pane.
func (c *Pane) Logs() *logs.Pane { return c.logs }

func (c *Pane) Scope() keymap.Scope {
	if c.tab == tabLogs {
		return keymap.ScopeLogs
	}
	return keymap.ScopeDetail
}

// Runs reports whether Do runs an action of another scope than the open
// tab's: the directory copies from any tab, and copy on the Logs tab
// copies the line there, as yank does (the command line reaches them so).
func (c *Pane) Runs(id string) bool {
	return id == "copy-dir" || id == "copy" && c.tab == tabLogs
}

// sync follows the selection, in Update and Do and never while drawing: a
// new server gets its log stream, and the Env tab its spec, fetched again
// when the config's revision changes.
func (c *Pane) sync(env *ui.Env) tea.Cmd {
	name := ""
	if env.Selected.Kind == ui.SelectServer {
		name = env.Selected.Name
	}
	var cmd tea.Cmd
	if name != c.shown {
		c.shown = name
		c.lists = [tabCount]fieldList{}
		cmd = c.logs.Show(env, name)
	}
	return tea.Batch(cmd, c.syncSpec(env))
}

// syncSpec fetches the shown server's spec while the Env tab is open and
// the one it has is for another server or revision.
func (c *Pane) syncSpec(env *ui.Env) tea.Cmd {
	if c.tab != tabEnv || c.shown == "" || env.State == nil {
		return nil
	}
	rev := env.State.Config.Revision
	if c.spec.server == c.shown && c.spec.rev == rev {
		return nil
	}
	if c.spec.server != c.shown {
		c.spec = specState{server: c.shown}
	}
	c.spec.rev = rev
	c.specGen++
	return fetchSpec(env.Client, c.specGen, c.shown)
}

func fetchSpec(client *client.Client, gen int, name string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		var spec config.Spec
		err := client.Do(ctx, http.MethodGet, "/api/servers/"+url.PathEscape(name)+"/spec", nil, &spec)
		return specMsg{gen: gen, server: name, spec: spec, err: err}
	}
}

func (c *Pane) setTab(env *ui.Env, tab int) tea.Cmd {
	c.tab = (tab + tabCount) % tabCount
	return c.syncSpec(env)
}

func (c *Pane) Update(env *ui.Env, msg tea.Msg) tea.Cmd {
	cmds := []tea.Cmd{c.sync(env)}
	switch msg := msg.(type) {
	case specMsg:
		if msg.gen != c.specGen || msg.server != c.spec.server {
			return tea.Batch(cmds...)
		}
		if msg.err != nil {
			c.spec.err = msg.err.Error()
		} else {
			c.spec.spec, c.spec.loaded, c.spec.err = msg.spec, true, ""
			c.spec.loadedRev, c.spec.loadedAt = c.spec.rev, env.Now()
		}
		return tea.Batch(cmds...)
	case tea.MouseClickMsg:
		m := msg.Mouse()
		if m.Y == 0 {
			if tab, ok := c.strip.Click(m.X); ok && m.Button == tea.MouseLeft {
				cmds = append(cmds, c.setTab(env, tab))
			}
			return tea.Batch(cmds...)
		}
		if c.tab == tabDetails || c.tab == tabEnv {
			c.lists[c.tab].click(m.Y - 1)
			return tea.Batch(cmds...)
		}
		if c.tab != tabLogs {
			return tea.Batch(cmds...)
		}
		m.X, m.Y = m.X-2, m.Y-1
		msg = tea.MouseClickMsg(m)
	case tea.MouseWheelMsg:
		if c.tab != tabLogs {
			delta := wheelRows
			if msg.Mouse().Button == tea.MouseWheelUp {
				delta = -delta
			}
			c.lists[c.tab].scrollBy(delta)
			return tea.Batch(cmds...)
		}
		m := msg.Mouse()
		m.X, m.Y = m.X-2, m.Y-1
		msg = tea.MouseWheelMsg(m)
	case tea.MouseMsg:
		if c.tab != tabLogs {
			return tea.Batch(cmds...)
		}
	}
	cmds = append(cmds, c.logs.Update(env, msg))
	return tea.Batch(cmds...)
}

const wheelRows = 3

// Do runs the tab keys, the main panel's scrolling, which also comes while
// the servers are focused, and the focused tab's actions; esc clears the
// log search first, then goes back to the servers.
func (c *Pane) Do(env *ui.Env, a keymap.Action) tea.Cmd {
	switch a.ID {
	case "tab-next":
		return c.setTab(env, c.tab+1)
	case "tab-prev":
		return c.setTab(env, c.tab-1)
	case "back":
		if c.tab == tabLogs && c.logs.Back() {
			return nil
		}
		return func() tea.Msg { return ui.FocusListMsg{} }
	case "copy-url", "copy-dir":
		return c.copyFact(env, a.ID)
	}
	if c.tab == tabLogs {
		if a.ID == "copy" {
			a.ID = "yank"
		}
		return c.logs.Do(env, a)
	}
	if a.ID == "copy" {
		return c.copyField(env)
	}
	if c.tab == tabDetails || c.tab == tabEnv {
		c.lists[c.tab].do(a.ID)
	}
	return nil
}

func notice(level ui.Level, text string) tea.Cmd {
	return func() tea.Msg { return ui.NoticeMsg{Level: level, Text: text} }
}

// copyField copies the value under the cursor of the Details or Env tab
// (OSC 52).
func (c *Pane) copyField(env *ui.Env) tea.Cmd {
	f, ok := c.lists[c.tab].current()
	if c.tab != tabDetails && c.tab != tabEnv || !ok || f.copy == "" {
		return notice(ui.LevelInfo, "nothing to copy here")
	}
	what := viewfmt.PlainText(f.label)
	if !f.secret {
		what += " " + clip(viewfmt.PlainText(strings.ReplaceAll(f.copy, "\n", " ")), env.Glyphs.Ellipsis, 40)
	}
	return tea.Batch(tea.SetClipboard(f.copy), notice(ui.LevelSuccess, "copied "+what))
}

// copyFact copies the shown server's URL, as it opens from here, or its
// folder, whatever tab is open.
func (c *Pane) copyFact(env *ui.Env, id string) tea.Cmd {
	v, ok := c.view(env)
	if !ok {
		return notice(ui.LevelInfo, "no server selected")
	}
	value, what := v.Dir, "folder"
	if id == "copy-url" {
		value, what = env.Session.URL(v.Name, v.URL), "URL"
	}
	if value == "" {
		return notice(ui.LevelError, v.Name+" has no "+what+" to copy")
	}
	return tea.Batch(tea.SetClipboard(value), notice(ui.LevelSuccess, "copied "+clip(viewfmt.PlainText(value), env.Glyphs.Ellipsis, 60)))
}

func clip(s, ellipsis string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + ellipsis
}

// view finds the shown server in the latest state.
func (c *Pane) view(env *ui.Env) (supervisor.View, bool) {
	if env.State == nil || c.shown == "" {
		return supervisor.View{}, false
	}
	for _, v := range env.State.Servers {
		if v.Name == c.shown {
			return v, true
		}
	}
	return supervisor.View{}, false
}

func (c *Pane) View(env *ui.Env, w, h int, focused bool) string {
	v, ok := c.view(env)
	// Addresses show as they open from here.
	v.URL, v.Alias = env.Session.URL(v.Name, v.URL), env.Session.URL(v.Name, v.Alias)
	title, right := c.header(env, v, ok, max(0, w-2), focused)
	cw, ch := max(0, w-4), max(0, h-2)
	lines, status := c.content(env, v, ok, cw, ch, focused)
	box := ui.Box(env, w, h, focused, title, right, lines)
	if w < 4 || h < 2 || status == "" {
		return box
	}
	rows := strings.Split(box, "\n")
	rows[len(rows)-1] = bottom(env, w, focused, status)
	return strings.Join(rows, "\n")
}

// content renders the active tab's rows in cw x ch and the bottom
// border's status: the log mode, or the position in a tab that overflows.
func (c *Pane) content(env *ui.Env, v supervisor.View, ok bool, cw, ch int, focused bool) ([]string, string) {
	s := env.Styles
	if c.tab == tabLogs {
		return strings.Split(c.logs.View(env, cw, ch, focused), "\n"), c.logs.Status()
	}
	if !ok {
		return []string{note(env, "Select a server to see its details", s.Muted, cw)}, ""
	}
	switch c.tab {
	case tabMetrics:
		return Metrics(env, v, supervisor.SampleInterval, cw, ch), ""
	case tabDetails:
		return c.fieldRows(env, detailFields(env, v), detailsLayout, cw, ch, focused)
	}
	st := c.spec
	if st.server != c.shown {
		st = specState{}
	}
	switch {
	case st.loaded:
		var fields []field
		if st.err != "" {
			fields = append(fields, field{value: "Refresh failed: " + st.err, style: s.Error, info: true})
		}
		fields = append(fields, envFields(env, st.spec, false, c.source(env, st))...)
		return c.fieldRows(env, fields, envLayout(st.spec), cw, ch, focused)
	case st.err != "":
		return []string{note(env, st.err, s.Error, cw)}, ""
	}
	return []string{note(env, "Loading"+env.Glyphs.Ellipsis, s.Muted, cw)}, ""
}

// source says where the Env tab's values come from and how current they
// are: stale after a failed refresh, updating while a newer revision
// loads.
func (c *Pane) source(env *ui.Env, st specState) string {
	sep := env.Glyphs.Separator
	from := "from the config"
	if env.State != nil && env.State.Config.PathDisplay != "" {
		from = "from " + env.State.Config.PathDisplay
	}
	parts := []string{from}
	if st.loadedRev != "" {
		parts = append(parts, "revision "+st.loadedRev)
	}
	parts = append(parts, "loaded "+st.loadedAt.Local().Format("15:04:05"))
	switch {
	case st.err != "":
		parts = append(parts, "stale")
	case st.rev != st.loadedRev:
		parts = append(parts, "updating")
	}
	return strings.Join(parts, sep)
}

// fieldRows lays out a Details or Env tab (fieldList.view).
func (c *Pane) fieldRows(env *ui.Env, fields []field, l layout, cw, ch int, focused bool) ([]string, string) {
	return c.lists[c.tab].view(env, fields, l, cw, ch, focused)
}
