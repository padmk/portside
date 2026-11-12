package containers

import (
	tea "charm.land/bubbletea/v2"

	"github.com/padmk/portside/internal/tui/keymap"
	"github.com/padmk/portside/internal/tui/ui"
)

// Main is the right pane: the server pane it wraps while a server is
// selected (the detail pane), the container pane while a container is.
// Both get every message, so each follows the selection and ends its
// streams when it stops showing; keys and drawing go to the one shown.
type Main struct {
	Server    ui.Pane
	Container *Detail
	// container is set while the last call saw a container selected; Scope
	// takes no environment.
	container bool
}

// NewMain wraps the server pane.
func NewMain(server ui.Pane) *Main { return &Main{Server: server, Container: NewDetail()} }

// active is the pane for the selection.
func (m *Main) active(env *ui.Env) ui.Pane {
	if env.Selected.Kind == ui.SelectContainer {
		return m.Container
	}
	return m.Server
}

// Scope is the shown pane's.
func (m *Main) Scope() keymap.Scope {
	if m.container {
		return m.Container.Scope()
	}
	return m.Server.Scope()
}

func (m *Main) Update(env *ui.Env, msg tea.Msg) tea.Cmd {
	m.container = env.Selected.Kind == ui.SelectContainer
	switch msg.(type) {
	case tea.MouseMsg, tea.KeyPressMsg:
		// Pointer and raw keys belong to the pane on screen.
		return m.active(env).Update(env, msg)
	}
	return tea.Batch(m.Server.Update(env, msg), m.Container.Update(env, msg))
}

func (m *Main) Do(env *ui.Env, a keymap.Action) tea.Cmd {
	m.container = env.Selected.Kind == ui.SelectContainer
	return m.active(env).Do(env, a)
}

func (m *Main) View(env *ui.Env, w, h int, focused bool) string {
	m.container = env.Selected.Kind == ui.SelectContainer
	return m.active(env).View(env, w, h, focused)
}

// Close ends both panes' streams.
func (m *Main) Close() {
	if c, ok := m.Server.(interface{ Close() }); ok {
		c.Close()
	}
	m.Container.Close()
}
