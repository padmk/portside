// Package edit is the terminal UI's config editing (design/tui.md "Forms
// and dialogs"): `:config` runs the user's editor on the config text and
// saves it through the API with its revision, and the server, new server
// and settings forms are overlays over a dimmed screen. Everything goes
// through the daemon's API, so it works with --on, and the daemon
// validates: its errors are shown under the fields they name.
package edit

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/padmk/portside/internal/client"
	"github.com/padmk/portside/internal/config"
	"github.com/padmk/portside/internal/server"
	"github.com/padmk/portside/internal/tui/form"
	"github.com/padmk/portside/internal/tui/overlay"
	"github.com/padmk/portside/internal/tui/ui"
)

// sheetKind is what a form edits.
type sheetKind int

const (
	editServer sheetKind = iota
	newServer
	settingsSheet
)

// sheet is an open form and what saving it needs: the server's name, the
// spec it was loaded from (keys the form does not show are kept from it)
// and the config revision it was loaded at.
type sheet struct {
	kind     sheetKind
	form     *form.Model
	name     string
	base     config.Spec
	settings settingsView
	revision string
	// picker is the proposals list the form was opened from; esc on an
	// unchanged form goes back to it.
	picker *picker
	// suggested is the free port the daemon proposed for a new server,
	// filled into the empty port field; a task drops it on save.
	suggested string
}

// Editor is the root model's handle on config editing: at most one
// overlay (a loading line, the proposals or a form) and the `:config` run.
type Editor struct {
	// seq numbers what was opened, so a reply for something closed or
	// replaced does nothing.
	seq     int
	loading string
	picker  *picker
	sheet   *sheet
	cfg     *configRun
	// at is the box's top left corner on the last screen, for clicks.
	at [2]int
}

// Open reports whether the editor's overlay is on screen and takes the
// keys.
func (e *Editor) Open() bool { return e.loading != "" || e.picker != nil || e.sheet != nil }

func (e *Editor) close() {
	e.seq++
	e.loading, e.picker, e.sheet = "", nil, nil
}

type (
	specMsg struct {
		seq      int
		name     string
		revision string
		spec     config.Spec
		err      error
	}
	discoverMsg struct {
		seq   int
		reply discoverReply
		err   error
	}
	settingsMsg struct {
		seq  int
		view settingsView
		err  error
	}
	savedMsg struct {
		seq   int
		state *server.State
		err   error
	}
	reloadMsg   struct{ seq int }
	freePortMsg struct {
		seq  int
		port int
	}
)

// EditServer opens the form of a server's config entry.
func (e *Editor) EditServer(env *ui.Env, name string) tea.Cmd {
	e.close()
	e.loading = "Loading " + name + env.Glyphs.Ellipsis
	c, seq := env.Client, e.seq
	revision := ""
	if env.State != nil {
		revision = env.State.Config.Revision
	}
	return func() tea.Msg {
		var sp config.Spec
		err := request(c, http.MethodGet, "/api/servers/"+url.PathEscape(name)+"/spec", nil, &sp)
		return specMsg{seq: seq, name: name, revision: revision, spec: sp, err: err}
	}
}

// NewServer opens the scan proposals of root ("" for the projects
// folder), from which a form opens prefilled or empty.
func (e *Editor) NewServer(env *ui.Env, root string) tea.Cmd {
	e.close()
	e.loading = "Looking for projects" + env.Glyphs.Ellipsis
	c, seq := env.Client, e.seq
	path := "/api/discover"
	if root != "" {
		path += "?" + url.Values{"root": {root}}.Encode()
	}
	return func() tea.Msg {
		var r discoverReply
		err := request(c, http.MethodGet, path, nil, &r)
		return discoverMsg{seq: seq, reply: r, err: err}
	}
}

// Settings opens the form of the top-level settings.
func (e *Editor) Settings(env *ui.Env) tea.Cmd {
	e.close()
	e.loading = "Loading the settings" + env.Glyphs.Ellipsis
	c, seq := env.Client, e.seq
	return func() tea.Msg {
		var v settingsView
		err := request(c, http.MethodGet, "/api/settings", nil, &v)
		return settingsMsg{seq: seq, view: v, err: err}
	}
}

// Update handles the editor's own messages and pasted text while a form
// field is edited; false for any other message.
func (e *Editor) Update(env *ui.Env, msg tea.Msg) (tea.Cmd, bool) {
	if cmd, ok := e.configUpdate(msg); ok {
		return cmd, true
	}
	switch msg := msg.(type) {
	case specMsg:
		if msg.seq != e.seq {
			return nil, true
		}
		if msg.err != nil {
			e.close()
			return notice(ui.LevelError, "edit "+msg.name+": "+msg.err.Error()), true
		}
		e.loading = ""
		e.sheet = &sheet{kind: editServer, name: msg.name, base: msg.spec, revision: msg.revision,
			form: form.New("Edit "+msg.name, specFields(msg.spec, false))}
		e.sheet.form.Move(1)
	case discoverMsg:
		if msg.seq != e.seq {
			return nil, true
		}
		if msg.err != nil {
			e.close()
			return notice(ui.LevelError, "scan: "+msg.err.Error()), true
		}
		e.loading = ""
		e.picker = &picker{root: msg.reply.Root, items: msg.reply.Suggestions}
	case settingsMsg:
		if msg.seq != e.seq {
			return nil, true
		}
		if msg.err != nil {
			e.close()
			return notice(ui.LevelError, "settings: "+msg.err.Error()), true
		}
		e.loading = ""
		e.sheet = &sheet{kind: settingsSheet, settings: msg.view, revision: msg.view.Revision, form: form.New("Settings", settingsFields(msg.view))}
	case savedMsg:
		if msg.seq != e.seq || e.sheet == nil {
			return nil, true
		}
		return e.saved(env, msg), true
	case reloadMsg:
		if msg.seq != e.seq || e.sheet == nil {
			return nil, true
		}
		switch sh := e.sheet; sh.kind {
		case editServer:
			return e.EditServer(env, sh.name), true
		case settingsSheet:
			return e.Settings(env), true
		}
	case freePortMsg:
		if msg.seq != e.seq || e.sheet == nil || e.sheet.kind != newServer {
			return nil, true
		}
		e.sheet.suggest(msg.port)
	case addedMsg:
		if msg.seq != e.seq || e.picker == nil {
			return nil, true
		}
		return e.added(msg), true
	case form.DiscardMsg:
		if e.sheet != nil && e.sheet.form == msg.Form {
			e.close()
		}
	case tea.PasteMsg:
		if e.sheet == nil || !e.sheet.form.Editing() {
			return nil, false
		}
		e.sheet.form.Paste(msg.Content)
	default:
		return nil, false
	}
	return nil, true
}

// Key handles a key press while the overlay is open.
func (e *Editor) Key(env *ui.Env, msg tea.KeyPressMsg) tea.Cmd {
	switch {
	case e.sheet != nil:
		out, cmd := e.sheet.form.Key(env, msg)
		switch out {
		case form.Save:
			return e.save(env)
		case form.Close:
			back := e.sheet.picker
			e.close()
			if back != nil {
				e.picker = back
			}
		}
		return cmd
	case e.picker != nil:
		return e.pickerKey(env, msg.String())
	}
	if k := msg.String(); k == "esc" || k == "q" {
		e.close()
	}
	return nil
}

// pickerKey handles a key in the proposals list.
func (e *Editor) pickerKey(env *ui.Env, k string) tea.Cmd {
	p := e.picker
	switch k {
	case "j", "down", "tab":
		p.move(1)
	case "k", "up", "shift+tab":
		p.move(-1)
	case "g", "home":
		p.cursor = 0
	case "G", "end":
		p.cursor = len(p.items)
	case "space":
		p.mark()
	case "enter":
		if p.adding {
			return nil
		}
		if len(p.marked) > 0 {
			return e.addMarked(env)
		}
		return e.openProposal(env, p)
	case "m":
		p.cursor = len(p.items)
		return e.openProposal(env, p)
	case "esc", "q":
		e.close()
	}
	return nil
}

// openProposal opens the new server form with the cursor's proposal, or
// empty on the manual row. A form without a port asks the daemon for the
// lowest free one to fill in.
func (e *Editor) openProposal(env *ui.Env, p *picker) tea.Cmd {
	base := config.Spec{}
	title := "New server"
	if !p.manual() {
		base = proposalSpec(p.items[p.cursor])
		title = "New server: " + p.items[p.cursor].Name
	}
	e.picker = nil
	e.sheet = &sheet{kind: newServer, base: base, picker: p, form: form.New(title, specFields(base, true))}
	if base.Name != "" {
		// The proposal's name and command are filled in; the port is next.
		e.sheet.form.Move(3)
	}
	if base.Port != nil {
		return nil
	}
	c, seq := env.Client, e.seq
	return func() tea.Msg {
		var r struct {
			Port int `json:"port"`
		}
		if err := request(c, http.MethodGet, "/api/free-port", nil, &r); err != nil || r.Port <= 0 {
			// The field stays empty: automatic, which the daemon resolves.
			return nil
		}
		return freePortMsg{seq: seq, port: r.Port}
	}
}

// suggest fills the free port the daemon proposed into the port field,
// unless the user typed one meanwhile; clearing it makes the port
// automatic again.
func (sh *sheet) suggest(port int) {
	f := sh.form.Field("port")
	if f == nil || strings.TrimSpace(f.Value) != "" || (sh.form.Editing() && sh.form.Fields[sh.form.Cursor()].Key == "port") {
		return
	}
	sh.suggested = strconv.Itoa(port)
	sh.form.Suggest("port", sh.suggested)
}

// save sends the form's values to the daemon.
func (e *Editor) save(env *ui.Env) tea.Cmd {
	sh := e.sheet
	f := sh.form
	f.ClearErrors()
	var fe fieldError
	c, seq := env.Client, e.seq
	var method, path string
	var body any
	switch sh.kind {
	case settingsSheet:
		s, err := settingsFrom(f, sh.settings)
		if errors.As(err, &fe) {
			f.Field(fe.key).Err = fe.msg
			return nil
		}
		method, path = http.MethodPut, "/api/settings"
		body = struct {
			config.Settings
			Revision string `json:"revision"`
		}{s, sh.revision}
	default:
		sp, err := specFrom(f, sh.base)
		if errors.As(err, &fe) {
			f.Field(fe.key).Err = fe.msg
			return nil
		}
		// A task has no port: the suggestion goes when the form became
		// one; a port the user typed stays for the daemon to refuse.
		if sp.Oneshot && sh.suggested != "" && sp.Port != nil && strconv.Itoa(*sp.Port) == sh.suggested {
			sp.Port = nil
		}
		method, path, body = http.MethodPost, "/api/servers", sp
		if sh.kind == editServer {
			method, path = http.MethodPut, "/api/servers/"+url.PathEscape(sh.name)
			body = struct {
				config.Spec
				Revision string `json:"revision"`
			}{sp, sh.revision}
		}
	}
	f.Busy = "Saving" + env.Glyphs.Ellipsis
	return func() tea.Msg {
		var st server.State
		if err := request(c, method, path, body, &st); err != nil {
			return savedMsg{seq: seq, err: err}
		}
		return savedMsg{seq: seq, state: &st}
	}
}

// saved closes the form after a save, with the new state, a notice and,
// for an edited server that runs with its old settings, an offer to
// restart it; a refusal stays in the form, under the field it names, and a
// config that changed since the form opened can be reloaded.
func (e *Editor) saved(env *ui.Env, msg savedMsg) tea.Cmd {
	sh := e.sheet
	sh.form.Busy = ""
	var apiErr *client.APIError
	if errors.As(msg.err, &apiErr) && apiErr.Status == http.StatusConflict && sh.kind != newServer {
		sh.form.Err = "The config changed after this form opened."
		seq := e.seq
		return confirm(ui.ConfirmMsg{
			Title:   "The config changed elsewhere",
			Body:    "Reload shows the file's values and drops your changes here; Cancel keeps the form.",
			Confirm: "Reload",
			Yes:     func() tea.Msg { return reloadMsg{seq: seq} },
		})
	}
	if msg.err != nil {
		text := msg.err.Error()
		if apiErr != nil {
			text = apiErr.Msg
		}
		sh.form.SetError(text)
		return nil
	}
	e.close()
	st := msg.state
	switch sh.kind {
	case newServer:
		name := text(sh.form, "name")
		return tea.Sequence(stateCmd(st), func() tea.Msg { return ui.SelectMsg{Item: ui.ServerItem(name)} },
			savedNotice("added "+name+"; space starts it", st, nil))
	case editServer:
		cmds := []tea.Cmd{stateCmd(st), savedNotice("saved "+sh.name, st, []string{sh.name})}
		if slices.Contains(staleNames(st), sh.name) {
			cmds = append(cmds, restartOffer([]string{sh.name}))
		}
		return tea.Sequence(cmds...)
	}
	return tea.Batch(stateCmd(st), savedNotice("settings saved", st, nil), restartOffer(staleNames(st)))
}

// restartOffer asks once to restart the running servers that keep their
// old settings until they restart, naming them; nil for none.
func restartOffer(names []string) tea.Cmd {
	if len(names) == 0 {
		return nil
	}
	list := joinNames(names)
	body := list + " keeps running with its old settings until it restarts."
	if len(names) > 1 {
		body = list + " keep running with their old settings until they restart."
	}
	yes := make([]tea.Cmd, len(names))
	for i, n := range names {
		item := ui.ServerItem(n)
		yes[i] = func() tea.Msg { return ui.RunMsg{Action: "restart", Item: item} }
	}
	return confirm(ui.ConfirmMsg{
		Title:   "Restart " + list + "?",
		Body:    body,
		Confirm: "Restart",
		Yes:     tea.Batch(yes...),
	})
}

// joinNames is "web", "web and api", "web, api and worker".
func joinNames(names []string) string {
	if len(names) < 2 {
		return strings.Join(names, "")
	}
	return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
}

// staleNames are the servers that run with settings older than the
// config's: a restart applies the change.
func staleNames(st *server.State) []string {
	var out []string
	if st == nil {
		return nil
	}
	for _, v := range st.Servers {
		if v.Stale && v.CanStop() {
			out = append(out, v.Name)
		}
	}
	return out
}

// savedNotice says what was saved, the daemon's warnings and the running
// servers (but skip) that keep their old settings until restarted.
func savedNotice(what string, st *server.State, skip []string) tea.Cmd {
	level, text := ui.LevelSuccess, what
	if st != nil && len(st.Warnings) > 0 {
		level, text = ui.LevelWarn, text+"; "+strings.Join(st.Warnings, "; ")
	}
	var stale []string
	for _, n := range staleNames(st) {
		if !slices.Contains(skip, n) {
			stale = append(stale, n)
		}
	}
	if len(stale) > 0 {
		verb := "runs"
		if len(stale) > 1 {
			verb = "run"
		}
		text += "; " + strings.Join(stale, ", ") + " " + verb + " with the old settings until restarted"
	}
	return notice(level, text)
}

// Hints are the footer's key hints while the overlay is open.
func (e *Editor) Hints(ascii bool) [][2]string {
	switch {
	case e.sheet != nil:
		return e.sheet.form.Hints(ascii)
	case e.picker != nil:
		return e.picker.hints(ascii)
	}
	return [][2]string{{"esc", "cancel"}}
}

// View draws the overlay centered over the screen, which is dimmed under
// it, and keeps the box's place for clicks.
func (e *Editor) View(env *ui.Env, screen string, w, h int) string {
	box := ""
	switch {
	case e.sheet != nil:
		box = e.sheet.form.View(env, w, h)
	case e.picker != nil:
		box = e.picker.view(env, w, h)
	default:
		box = overlay.Frame(env.Styles, env.Glyphs, "", []string{env.Styles.OnRaised().Muted.Render(e.loading)}, max(20, ansi.StringWidth(e.loading)))
	}
	bw, bh := 0, strings.Count(box, "\n")+1
	for _, r := range strings.Split(box, "\n") {
		bw = max(bw, ansi.StringWidth(r))
	}
	e.at = [2]int{max(0, (w-bw)/2), max(0, (h-bh)/2)}
	return overlay.Place(overlay.Dimmed(env.Styles, screen, w), box, e.at[0], e.at[1])
}

// Mouse handles the mouse while the overlay is open: a click on a field
// or a proposal selects it (a second click on a proposal opens it), the
// wheel moves the cursor.
func (e *Editor) Mouse(env *ui.Env, msg tea.MouseMsg) tea.Cmd {
	ms := msg.Mouse()
	switch msg.(type) {
	case tea.MouseWheelMsg:
		n := map[tea.MouseButton]int{tea.MouseWheelDown: 1, tea.MouseWheelUp: -1}[ms.Button]
		switch {
		case e.sheet != nil && !e.sheet.form.Editing():
			e.sheet.form.Move(n)
		case e.picker != nil:
			e.picker.move(n)
		}
	case tea.MouseClickMsg:
		if ms.Button != tea.MouseLeft {
			return nil
		}
		y := ms.Y - e.at[1] - 1
		switch {
		case e.sheet != nil:
			e.sheet.form.Click(y)
		case e.picker != nil:
			before := e.picker.cursor
			if e.picker.click(y) && e.picker.cursor == before && !e.picker.adding {
				return e.openProposal(env, e.picker)
			}
		}
	}
	return nil
}

func request(c *client.Client, method, path string, body, out any) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return c.Do(ctx, method, path, body, out)
}

func notice(level ui.Level, text string) tea.Cmd {
	return func() tea.Msg { return ui.NoticeMsg{Level: level, Text: text} }
}

func confirm(c ui.ConfirmMsg) tea.Cmd { return func() tea.Msg { return c } }

// stateCmd passes the state a save returned to the panes at once, before
// the events connection brings it, so a new server can be selected.
func stateCmd(st *server.State) tea.Cmd {
	if st == nil {
		return nil
	}
	return func() tea.Msg { return ui.StateMsg{State: st} }
}
