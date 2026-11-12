package tui

import (
	"slices"

	tea "charm.land/bubbletea/v2"

	"github.com/padmk/portside/internal/tui/keymap"
	"github.com/padmk/portside/internal/tui/overlay"
	"github.com/padmk/portside/internal/tui/ui"
)

// formActions are the servers' actions that open a form without a
// target (internal/tui/edit); "edit" acts on a server and is one of
// targetActions.
var formActions = map[string]bool{"new": true}

// formGlobals are the global actions of config editing, by id.
func (m *model) formGlobals() map[string]func() tea.Cmd {
	return map[string]func() tea.Cmd{
		"config":   func() tea.Cmd { return m.edit.Config(&m.env, m.gen) },
		"settings": func() tea.Cmd { return m.edit.Settings(&m.env) },
		"scan":     func() tea.Cmd { return m.formAction("scan", "") },
	}
}

// formAction opens the new server proposals: of the projects folder for
// "new", of the folder arg names for "scan".
func (m *model) formAction(id, arg string) tea.Cmd {
	switch id {
	case "new", "scan":
		return m.edit.NewServer(&m.env, arg)
	}
	return nil
}

// editUpdate passes a message to the config editing and applies the input
// mode a form asked for; false when the message is not its own.
func (m *model) editUpdate(msg tea.Msg) (tea.Cmd, bool) {
	cmd, ok := m.edit.Update(&m.env, msg)
	if ok {
		m.takeEditMode()
	}
	return cmd, ok
}

// editKey passes a key to the open form or proposals.
func (m *model) editKey(msg tea.KeyPressMsg) tea.Cmd {
	cmd := m.edit.Key(&m.env, msg)
	m.takeEditMode()
	return cmd
}

// takeEditMode applies the mode a form asked for; the form owns it (no
// pane does), and it ends with the form.
func (m *model) takeEditMode() {
	if mode, ok := m.env.TakeMode(); ok {
		m.setMode(mode, -1)
	}
	if !m.edit.Open() && m.mode == ui.ModeInsert && m.modeOwner < 0 {
		m.setMode(ui.ModeNormal, -1)
	}
}

// withFormMenu adds the config editing to the servers' menu on a server
// or an empty list: a new server, then the config file and the settings.
// A group header's menu keeps to the group.
func (m *model) withFormMenu(menu overlay.Menu, t ui.Selection) overlay.Menu {
	if t.Kind == ui.SelectGroup {
		return menu
	}
	for _, id := range []string{"new", "config", "settings"} {
		if slices.ContainsFunc(menu.Items, func(it overlay.MenuItem) bool { return it.ActionID == id }) {
			continue
		}
		for _, a := range m.env.Keys.InScope(keymap.ScopeServers, keymap.ScopeGlobal) {
			if a.ID == id {
				menu.Items = append(menu.Items, overlay.ItemFor(a, m.ascii()))
				break
			}
		}
	}
	return menu
}
