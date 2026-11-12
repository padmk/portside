package remote

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/padmk/portside/internal/agentsetup"
	"github.com/padmk/portside/internal/app"
	"github.com/padmk/portside/internal/client"
	"github.com/padmk/portside/internal/tui/ui"
)

// SetupOff is why `:setup` and `:doctor` do nothing: coding agents are set
// up on this computer only, never through another one.
func SetupOff(env *ui.Env) string {
	if env.Session.Remote() {
		return "only on this computer; :on local returns"
	}
	return ""
}

// agents is the coding-agent setup overlay: the setup items of the agents
// installed for this account (GET /api/agents) with a check box each, and
// the results once applied (POST /api/agents/apply).
type agents struct {
	seq     int
	loading bool
	items   []agentsetup.Item
	chosen  map[string]bool
	results []agentsetup.Result
	err     string
	busy    bool
	cursor  int
	// clicks are the last frame's clickable lines.
	clicks clicks
}

func (a *agents) open(c *client.Client) tea.Cmd {
	a.seq++
	a.loading, a.err, a.results, a.busy = true, "", nil, false
	return fetchAgents(c, a.seq)
}

// setItems takes the items and checks the ones applying would fix.
func (a *agents) setItems(items []agentsetup.Item) {
	a.items, a.chosen, a.cursor = items, map[string]bool{}, 0
	for _, it := range items {
		if it.Fixable() {
			a.chosen[it.ID] = true
		}
	}
}

// ids are the checked items' ids in list order.
func (a *agents) ids() []string {
	var out []string
	for _, it := range a.items {
		if a.chosen[it.ID] {
			out = append(out, it.ID)
		}
	}
	return out
}

func (a *agents) update(msg tea.Msg) (tea.Cmd, bool) {
	switch msg := msg.(type) {
	case agentsMsg:
		if msg.seq != a.seq {
			return nil, true
		}
		a.loading = false
		if msg.err != nil {
			a.err = "coding agents: " + msg.err.Error()
			return notice(ui.LevelError, a.err), true
		}
		a.setItems(msg.items)
	case appliedMsg:
		if msg.seq != a.seq {
			return nil, true
		}
		a.busy = false
		if msg.err != nil {
			a.err = "set up coding agents: " + msg.err.Error()
			return notice(ui.LevelError, a.err), true
		}
		a.results = msg.results
		return notice(resultLevel(msg.results), resultSummary(msg.results)), true
	default:
		return nil, false
	}
	return nil, true
}

func resultLevel(rs []agentsetup.Result) ui.Level {
	if agentsetup.Failed(rs) {
		return ui.LevelError
	}
	return ui.LevelSuccess
}

// resultSummary is the notice after applying: what changed and what
// failed.
func resultSummary(rs []agentsetup.Result) string {
	var changed, failed []string
	for _, r := range rs {
		switch {
		case !r.OK:
			failed = append(failed, r.Label)
		case r.Changed:
			changed = append(changed, r.Label)
		}
	}
	switch {
	case len(failed) > 0:
		return "coding agents: " + strings.Join(failed, ", ") + " failed; :setup shows how to run it yourself"
	case len(changed) > 0:
		return "coding agents: connected " + strings.Join(changed, ", ")
	}
	return "coding agents: everything was set up already"
}

// key handles a key; closed is set when the overlay closes.
func (a *agents) key(c *client.Client, msg tea.KeyPressMsg) (tea.Cmd, bool) {
	switch msg.String() {
	case "esc", "q":
		return nil, true
	case "j", "down":
		a.cursor = min(a.cursor+1, max(0, len(a.items)-1))
	case "k", "up":
		a.cursor = max(0, a.cursor-1)
	case "space", "x":
		if a.results == nil && a.cursor < len(a.items) {
			id := a.items[a.cursor].ID
			a.chosen[id] = !a.chosen[id]
		}
	case "enter", "ctrl+s":
		if a.results != nil {
			return nil, true
		}
		if a.busy || a.loading {
			return nil, false
		}
		ids := a.ids()
		if len(ids) == 0 {
			return notice(ui.LevelInfo, "nothing checked; space checks an item"), false
		}
		a.busy = true
		return applyAgents(c, a.seq, ids), false
	case "R":
		return a.open(c), false
	}
	return nil, false
}

// stateWord is how an item's state reads.
func stateWord(st agentsetup.State) string {
	switch st {
	case agentsetup.OK:
		return "connected"
	case agentsetup.Missing:
		return "not set up"
	case agentsetup.Outdated:
		return "outdated"
	case agentsetup.Foreign:
		return "a skill with this name that " + app.Name + " did not install"
	case agentsetup.Unavailable:
		return "its command was not found"
	}
	return string(st)
}

func (a *agents) lines(env *ui.Env) (lines []string, cursorLine int) {
	s := env.Styles.OnRaised()
	g := env.Glyphs
	cursorLine = -1
	cl := &a.clicks
	*cl = (*cl)[:0]
	add := func(l string) {
		lines = append(lines, l)
		cl.line(-1)
	}
	add(s.Muted.Render("The MCP server and the skill let Claude Code and Codex find, start and debug your servers."))
	add("")
	switch {
	case a.loading:
		add(s.Muted.Render("Checking the coding agents" + g.Ellipsis))
	case a.results != nil:
		for _, r := range a.results {
			mark, style := "ok", s.Running
			if !r.OK {
				mark, style = "failed", s.Error
			}
			text := r.Label
			switch {
			case !r.OK && r.Error != "":
				text += ": " + r.Error
			case r.Changed:
				text += ": connected"
			default:
				text += ": already connected"
			}
			add(style.Render(pad(mark, 7, "")) + s.Text.Render(plain(text)))
			if r.Command != "" {
				add("       " + s.Muted.Render("run it yourself: ") + s.Text.Render(plain(r.Command)))
			}
		}
	case len(a.items) == 0 && a.err == "":
		add(s.Muted.Render("No coding agent (Claude Code or Codex) is installed for this account."))
	default:
		labelW := 0
		for _, it := range a.items {
			labelW = max(labelW, ansi.StringWidth(plain(it.Label)))
		}
		for i, it := range a.items {
			box := "[ ]"
			if a.chosen[it.ID] {
				box = "[x]"
			}
			if i == a.cursor {
				cursorLine = len(lines)
			}
			check := s.Text.Render(box + " " + pad(plain(it.Label), labelW, ""))
			lines = append(lines, check+s.Muted.Render("  "+stateWord(it.State)))
			// The check box and the label check the item.
			cl.line(i)
			cl.on(0, ansi.StringWidth(check), "space")
			if it.Detail != "" && it.State != agentsetup.OK {
				add("    " + s.Muted.Render(plain(it.Detail)))
			}
		}
	}
	if a.err != "" {
		add("")
		add(s.Error.Render(plain(a.err)))
	}
	if a.busy {
		add("")
		add(s.Muted.Render("Setting up" + g.Ellipsis))
	}
	if !a.loading {
		add("")
		lines = append(lines, actionLine(env.Styles, g, cl, a.actions()))
	}
	return lines, cursorLine
}

// actions are the overlay's clickable actions.
func (a *agents) actions() [][2]string {
	if a.results != nil {
		return [][2]string{{"enter", "close"}, {"R", "check again"}}
	}
	return [][2]string{{"enter", "set up the checked"}, {"R", "check again"}}
}

func (a *agents) hints(enter string) [][2]string {
	if a.results != nil {
		return [][2]string{{enter, "close"}, {"R", "check again"}, {"esc", "close"}}
	}
	return [][2]string{{"space", "check"}, {enter, "set up the checked"}, {"R", "check again"}, {"esc", "close"}}
}
