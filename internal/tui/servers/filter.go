package servers

import (
	"slices"
	"strconv"
	"strings"
	"unicode"

	tea "charm.land/bubbletea/v2"

	"github.com/padmk/portside/internal/supervisor"
	"github.com/padmk/portside/internal/tui/ui"
	"github.com/padmk/portside/internal/viewfmt"
)

// matches reports whether every term is in the server's name, group,
// directory, port or display state (the word its lamp shows); terms are
// lower case.
func matches(v supervisor.View, terms []string) bool {
	if len(terms) == 0 {
		return true
	}
	fields := []string{v.Name, v.Group, v.DirDisplay, string(v.Display())}
	if v.Port > 0 {
		fields = append(fields, ":"+strconv.Itoa(v.Port))
	}
	hay := strings.ToLower(strings.Join(fields, "\x00"))
	for _, t := range terms {
		if !strings.Contains(hay, t) {
			return false
		}
	}
	return true
}

// inStates reports whether the server's display state is one of states;
// no states keep every server.
func inStates(v supervisor.View, states []supervisor.Display) bool {
	return len(states) == 0 || slices.Contains(states, v.Display())
}

// filterKey edits the filter while it is typed: enter keeps it, esc clears
// it, both return to normal mode; up and down move through what it keeps.
func (p *Pane) filterKey(env *ui.Env, msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		p.filtering, p.query = false, ""
		env.EnterMode(ui.ModeNormal)
		return p.keepVisibleTarget(env)
	case "enter":
		p.filtering = false
		p.query = strings.TrimSpace(p.query)
		env.EnterMode(ui.ModeNormal)
		return p.keepVisibleTarget(env)
	case "up", "ctrl+p":
		rows := p.rows(env, false)
		return p.moveTo(env, rows, p.index(env, rows)-1)
	case "down", "ctrl+n":
		rows := p.rows(env, false)
		return p.moveTo(env, rows, p.index(env, rows)+1)
	case "backspace":
		r := []rune(p.query)
		if len(r) > 0 {
			p.query = string(r[:len(r)-1])
		}
	case "ctrl+u":
		p.query = ""
	case "ctrl+w":
		q := strings.TrimRightFunc(p.query, unicode.IsSpace)
		p.query = q[:strings.LastIndexFunc(q, unicode.IsSpace)+1]
	default:
		if msg.Text == "" {
			return nil
		}
		p.query += viewfmt.PlainText(msg.Text)
	}
	return p.keepVisibleTarget(env)
}
