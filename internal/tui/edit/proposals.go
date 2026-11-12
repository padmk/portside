package edit

import (
	"errors"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/charmbracelet/x/ansi"

	"github.com/padmk/portside/internal/client"
	"github.com/padmk/portside/internal/config"
	"github.com/padmk/portside/internal/server"
	"github.com/padmk/portside/internal/tui/form"
	"github.com/padmk/portside/internal/tui/overlay"
	"github.com/padmk/portside/internal/tui/theme"
	"github.com/padmk/portside/internal/tui/ui"
	"github.com/padmk/portside/internal/viewfmt"
)

// discoverReply is GET /api/discover: the folder scanned and the projects
// found there that the config does not run yet.
type discoverReply struct {
	Root        string                  `json:"root"`
	Suggestions []server.DiscoverResult `json:"suggestions"`
}

// proposalSpec is the server a scan proposal adds, as the dashboard's
// DiscoverPanel maps it: the directory as the config writes it, the
// suggested command and port (0 for a project without one, automatic when
// none was found), and the suggested stop timeout.
func proposalSpec(p server.DiscoverResult) config.Spec {
	sp := config.Spec{Name: p.Name, Dir: p.DirConfig, Cmd: p.Cmd, Stop: p.StopAfter}
	switch {
	case p.NoPort:
		zero := 0
		sp.Port = &zero
	case p.Port != 0:
		port := p.Port
		sp.Port = &port
	}
	return sp
}

// picker is the list of scan proposals the new server form starts from;
// its last row opens an empty form. Marked proposals are added at once,
// as they are, and the ones the daemon refuses stay with its error.
type picker struct {
	root   string
	items  []server.DiscoverResult
	cursor int
	offset int
	// marked holds the proposals space marked, by name; errs the daemon's
	// refusal of each that failed to be added.
	marked map[string]bool
	errs   map[string]string
	adding bool
	// rows maps the content rows of the last view to items; len(items)
	// is the manual row, -1 others.
	rows []int
}

func (p *picker) move(n int) { p.cursor = max(0, min(len(p.items), p.cursor+n)) }

// manual reports whether the cursor is on the row for an empty form.
func (p *picker) manual() bool { return p.cursor == len(p.items) }

// mark marks or unmarks the cursor's proposal and moves on, as space does
// in a file manager's list.
func (p *picker) mark() {
	if p.manual() || p.adding {
		return
	}
	if p.marked == nil {
		p.marked = map[string]bool{}
	}
	name := p.items[p.cursor].Name
	if p.marked[name] {
		delete(p.marked, name)
	} else {
		p.marked[name] = true
	}
	p.move(1)
}

// hints are the list's keys; m (an empty form) is the last row's, which
// says so.
func (p *picker) hints(ascii bool) [][2]string {
	enter := "open the form"
	if n := len(p.marked); n > 0 {
		enter = fmt.Sprintf("add %d marked", n)
	}
	out := [][2]string{{"j/k", "move"}}
	if len(p.items) > 0 {
		out = append(out, [2]string{overlay.KeyLabel("space", ascii), "mark"})
	}
	return append(out, [2]string{overlay.KeyLabel("enter", ascii), enter}, [2]string{"esc", "close"})
}

// addResult is one marked proposal's POST /api/servers.
type addResult struct {
	name string
	err  error
}

// addedMsg ends adding the marked proposals: each one's result and the
// state after the last that was added.
type addedMsg struct {
	seq     int
	results []addResult
	state   *server.State
}

// addMarked adds the marked proposals one after another, as they are
// (proposalSpec), as the dashboard's "Add selected" does.
func (e *Editor) addMarked(env *ui.Env) tea.Cmd {
	p := e.picker
	var specs []config.Spec
	for _, it := range p.items {
		if p.marked[it.Name] {
			specs = append(specs, proposalSpec(it))
		}
	}
	p.adding, p.errs = true, nil
	c, seq := env.Client, e.seq
	return func() tea.Msg {
		out := addedMsg{seq: seq}
		for _, sp := range specs {
			var st server.State
			err := request(c, http.MethodPost, "/api/servers", sp, &st)
			if err == nil {
				out.state = &st
			}
			out.results = append(out.results, addResult{sp.Name, err})
		}
		return out
	}
}

// added drops the proposals that were added from the list and keeps the
// others marked with their errors; when all went in, the list closes and
// the first is selected.
func (e *Editor) added(msg addedMsg) tea.Cmd {
	p := e.picker
	p.adding = false
	var ok, failed []string
	p.errs = map[string]string{}
	for _, r := range msg.results {
		if r.err == nil {
			ok = append(ok, r.name)
			delete(p.marked, r.name)
			continue
		}
		text := r.err.Error()
		var apiErr *client.APIError
		if errors.As(r.err, &apiErr) {
			text = apiErr.Msg
		}
		p.errs[r.name] = text
		failed = append(failed, r.name)
	}
	p.items = slices.DeleteFunc(p.items, func(it server.DiscoverResult) bool { return slices.Contains(ok, it.Name) })
	p.cursor = min(p.cursor, len(p.items))
	cmds := []tea.Cmd{stateCmd(msg.state)}
	if len(failed) == 0 {
		e.close()
		first := ok[0]
		cmds = append(cmds, func() tea.Msg { return ui.SelectMsg{Item: ui.ServerItem(first)} },
			savedNotice("added "+joinNames(ok)+"; space starts "+plural(len(ok), "it", "them"), msg.state, nil))
		return tea.Sequence(cmds...)
	}
	text := fmt.Sprintf("%s not added; the list shows why", joinNames(failed))
	if len(ok) > 0 {
		text = "added " + joinNames(ok) + "; " + text
	}
	return tea.Batch(append(cmds, notice(ui.LevelError, text))...)
}

// view draws the proposals for a screen w x h: name, kind, port and
// folder on each row, the command under the cursor's row, then the row
// for an empty form.
func (p *picker) view(env *ui.Env, w, h int) string {
	s := overlay.Styles(env.Styles)
	g := env.Glyphs
	inner := form.Size(w)
	nameW := 4
	kindW := 4
	for _, it := range p.items {
		nameW = max(nameW, ansi.StringWidth(it.Name))
		kindW = max(kindW, ansi.StringWidth(it.Kind))
	}
	nameW, kindW = min(nameW, 20), min(kindW, 10)
	type line struct {
		text string
		item int
	}
	var rows []line
	if len(p.items) == 0 {
		rows = append(rows, line{s.Muted.Render(cutTo("No new projects found in "+p.root+".", inner, g.Ellipsis)), -1})
	}
	for i, it := range p.items {
		port := "auto"
		switch {
		case it.NoPort:
			port = "no port"
		case it.Port != 0:
			port = strconv.Itoa(it.Port)
		}
		head := fmt.Sprintf("%-*s  %-*s  %-7s  ", nameW, cutTo(it.Name, nameW, g.Ellipsis), kindW, cutTo(it.Kind, kindW, g.Ellipsis), port)
		// The folder as the config writes it, short under the projects
		// folder.
		dir := it.DirConfig
		if dir == "" {
			dir = it.DirDisplay
		}
		if it.DirectoryConfigured {
			dir += g.Separator + "folder already has servers"
		}
		dir = cutTo(viewfmt.PlainText(dir), inner-4-ansi.StringWidth(head), g.Ellipsis)
		mark := "  "
		if p.marked[it.Name] {
			mark = g.Done + " "
		}
		errRow := func() {
			if e := p.errs[it.Name]; e != "" {
				rows = append(rows, line{s.Surface.Render("    ") + s.Error.Render(cutTo(viewfmt.PlainText(e), inner-4, g.Ellipsis)), i})
			}
		}
		if i == p.cursor {
			rows = append(rows, line{overlay.Highlight(s, g.Cursor+" "+mark+head+dir, inner), i})
			rows = append(rows, line{s.Surface.Render("    ") + s.Muted.Render(cutTo(viewfmt.PlainText(it.Cmd), inner-4, g.Ellipsis)), i})
			errRow()
			continue
		}
		rows = append(rows, line{s.Surface.Render("  ") + s.Running.Render(mark) + s.Text.Render(head) + s.Muted.Render(dir), i})
		errRow()
	}
	manual := "Empty form (m): enter every key yourself"
	if p.manual() {
		rows = append(rows, line{overlay.Highlight(s, g.Cursor+" "+manual, inner), len(p.items)})
	} else {
		rows = append(rows, line{s.Surface.Render("  ") + s.Accent.Render(manual), len(p.items)})
	}
	avail := max(1, min(len(rows), h-7))
	first, last := -1, -1
	for i, r := range rows {
		if r.item == p.cursor {
			if first < 0 {
				first = i
			}
			last = i
		}
	}
	if last >= p.offset+avail {
		p.offset = last - avail + 1
	}
	if first >= 0 && first < p.offset {
		p.offset = first
	}
	p.offset = max(0, min(p.offset, len(rows)-avail))
	where := "In " + p.root
	if p.adding {
		where = "Adding " + joinNames(slices.Sorted(maps.Keys(p.marked))) + g.Ellipsis
	}
	out := []string{s.Muted.Render(cutTo(where, inner, g.Ellipsis))}
	p.rows = append(p.rows[:0], -1)
	for _, r := range rows[p.offset:min(len(rows), p.offset+avail)] {
		out = append(out, r.text)
		p.rows = append(p.rows, r.item)
	}
	out = append(out, "", overlay.HintFit(env.Styles, g, inner, p.hints(g.ID == theme.ASCII.ID)...))
	title := "New server"
	if n := len(p.items); n > 0 {
		title = fmt.Sprintf("New server: %d %s found", n, plural(n, "project", "projects"))
	}
	return overlay.Frame(env.Styles, g, title, out, inner)
}

// click moves the cursor to the item at content row y; false for none.
func (p *picker) click(y int) bool {
	if y < 0 || y >= len(p.rows) || p.rows[y] < 0 {
		return false
	}
	p.cursor = p.rows[y]
	return true
}

func cutTo(s string, w int, tail string) string {
	if w <= 0 {
		return ""
	}
	return ansi.Truncate(strings.ReplaceAll(s, "\n", " "), w, tail)
}

func plural(n int, one, other string) string {
	if n == 1 {
		return one
	}
	return other
}
