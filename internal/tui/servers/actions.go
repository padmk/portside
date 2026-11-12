package servers

import (
	tea "charm.land/bubbletea/v2"

	"github.com/padmk/portside/internal/tui/keymap"
	"github.com/padmk/portside/internal/tui/ui"
)

func notice(level ui.Level, text string) tea.Cmd {
	return func() tea.Msg { return ui.NoticeMsg{Level: level, Text: text} }
}

// Do runs the list's own actions: the cursor, folding and the filter. The
// server and group actions act on env.Target and run in the root model,
// which resolves their target once for keys, menus and commands.
func (p *Pane) Do(env *ui.Env, a keymap.Action) tea.Cmd {
	rows := p.rows(env, false)
	cur := p.index(env, rows)
	switch a.ID {
	case "down":
		return p.moveTo(env, rows, cur+1)
	case "up":
		return p.moveTo(env, rows, cur-1)
	case "next-page":
		return p.moveTo(env, rows, cur+max(1, p.listH-margin))
	case "prev-page":
		return p.moveTo(env, rows, cur-max(1, p.listH-margin))
	case "top":
		return p.moveTo(env, rows, 0)
	case "bottom":
		return p.moveTo(env, rows, len(rows)-1)
	case "collapse-all", "expand-all":
		cmd := p.foldAll(env, a.ID == "collapse-all")
		return tea.Batch(cmd, p.keepVisibleTarget(env))
	case "clear-filter":
		p.query, p.states, p.stateLabel = "", nil, ""
		return p.keepVisibleTarget(env)
	case "filter":
		p.filtering = true
		env.EnterMode(ui.ModeFilter)
		return nil
	case "open-detail":
		if p.onHeader {
			p.collapsed[p.headerGroup] = !p.collapsed[p.headerGroup]
			return p.keepVisibleTarget(env)
		}
		return p.openDetail(env)
	}
	return nil
}

// foldAll folds or unfolds every group; folding puts the cursor on the
// header of the group it was in.
func (p *Pane) foldAll(env *ui.Env, fold bool) tea.Cmd {
	rows := p.rows(env, true)
	if len(rows) == 0 || !rows[0].header {
		return notice(ui.LevelInfo, "no groups")
	}
	group := p.headerGroup
	if !p.onHeader {
		group = rows[p.index(env, rows)].group
	}
	for _, r := range rows {
		if r.header {
			if fold {
				p.collapsed[r.group] = true
			} else {
				delete(p.collapsed, r.group)
			}
		}
	}
	if fold {
		p.onHeader, p.headerGroup = true, group
		env.Target = ui.GroupItem(group)
	}
	return nil
}
