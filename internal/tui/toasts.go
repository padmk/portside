package tui

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/padmk/portside/internal/server"
	"github.com/padmk/portside/internal/supervisor"
	"github.com/padmk/portside/internal/tui/overlay"
	"github.com/padmk/portside/internal/tui/ui"
	"github.com/padmk/portside/internal/viewfmt"
)

// toasting is what the toasts at the top right are made from.
type toasting struct {
	toasts []overlay.Toast
	// displays holds each server's display state in the last snapshot,
	// nil before the first; problem marks servers whose crash or lost
	// readiness was announced and whose recovery is not yet.
	displays map[string]supervisor.Display
	problem  map[string]bool
	// connected is set once the events connection was up.
	connected bool
}

// toast shows a toast for overlay.ToastLife and keeps it in the history; a
// tick after its life redraws the screen without it.
func (m *model) toast(t ui.ToastMsg) tea.Cmd {
	text := t.Title
	if t.Body != "" {
		text += ": " + t.Body
	}
	m.rememberAbout(ui.NoticeMsg{Text: text, Level: t.Level}, t.Item)
	return m.showToast(t)
}

// showToast shows a toast without keeping it in the history.
func (m *model) showToast(t ui.ToastMsg) tea.Cmd {
	m.dropToasts()
	m.toasts = append(m.toasts, overlay.Toast{Title: plainText(t.Title), Body: plainText(t.Body), Level: t.Level, At: m.env.Now(), Item: t.Item})
	return tea.Tick(overlay.ToastLife+50*time.Millisecond, func(time.Time) tea.Msg { return toastTickMsg{} })
}

// dropToasts forgets the toasts older than their life.
func (m *model) dropToasts() {
	now := m.env.Now()
	kept := m.toasts[:0]
	for _, t := range m.toasts {
		if now.Sub(t.At) < overlay.ToastLife {
			kept = append(kept, t)
		}
	}
	m.toasts = kept
}

// stateToasts announces the servers that crashed, stopped answering or
// recovered since the last snapshot; the first snapshot announces nothing.
// Several of one kind in one snapshot (a group going down) make one toast
// naming them, each kept in `:messages` with its note.
func (m *model) stateToasts(st *server.State) tea.Cmd {
	if st == nil {
		return nil
	}
	first := m.displays == nil
	prev := m.displays
	m.displays = make(map[string]supervisor.Display, len(st.Servers))
	if m.problem == nil {
		m.problem = map[string]bool{}
	}
	var crashed, unready, recovered []ui.ToastMsg
	for _, v := range st.Servers {
		d := v.Display()
		m.displays[v.Name] = d
		old, seen := prev[v.Name]
		if first || !seen || old == d {
			continue
		}
		item := ui.ServerItem(v.Name)
		switch d {
		case supervisor.Display(supervisor.Crashed):
			m.problem[v.Name] = true
			crashed = append(crashed, ui.ToastMsg{Title: v.Name + " crashed", Body: viewfmt.StateNote(v), Level: ui.LevelError, Item: item})
		case supervisor.DisplayUnready:
			m.problem[v.Name] = true
			unready = append(unready, ui.ToastMsg{Title: v.Name + " stopped answering", Body: viewfmt.StateNote(v), Level: ui.LevelWarn, Item: item})
		case supervisor.Display(supervisor.Running):
			if m.problem[v.Name] {
				delete(m.problem, v.Name)
				recovered = append(recovered, ui.ToastMsg{Title: v.Name + " recovered", Level: ui.LevelSuccess, Item: item})
			}
		case supervisor.Display(supervisor.Stopped):
			delete(m.problem, v.Name)
		case supervisor.DisplayExternal, supervisor.DisplayDone:
		}
	}
	for name := range m.problem {
		if _, ok := m.displays[name]; !ok {
			delete(m.problem, name)
		}
	}
	return tea.Batch(m.toastAll(crashed, "servers crashed"), m.toastAll(unready, "servers stopped answering"), m.toastAll(recovered, "servers recovered"), m.containerToasts(st))
}

// toastAll shows one toast, or for several one toast naming them all
// ("3 servers crashed: web, api, worker", selecting the first), keeping
// each in the history.
func (m *model) toastAll(ts []ui.ToastMsg, what string) tea.Cmd {
	switch len(ts) {
	case 0:
		return nil
	case 1:
		return m.toast(ts[0])
	}
	names := make([]string, len(ts))
	for i, t := range ts {
		text := t.Title
		if t.Body != "" {
			text += ": " + t.Body
		}
		m.rememberAbout(ui.NoticeMsg{Text: text, Level: t.Level}, t.Item)
		names[i] = t.Item.Name
	}
	return m.showToast(ui.ToastMsg{
		Title: fmt.Sprintf("%d %s", len(ts), what),
		Body:  strings.Join(names, ", ") + "; :messages has the details",
		Level: ts[0].Level, Item: ts[0].Item,
	})
}

// connToast announces a lost events connection and its return.
func (m *model) connToast(connected bool) tea.Cmd {
	switch {
	case m.sys.Restarting() != "":
		// An upgrade or a restart in place drops the connection on
		// purpose; the top bar says so and a notice follows.
	case connected && !m.env.Connected && m.connected:
		return m.toast(ui.ToastMsg{Title: "Reconnected", Level: ui.LevelSuccess})
	case connected:
		m.connected = true
	case m.env.Connected:
		return m.toast(ui.ToastMsg{Title: "Lost the connection to the daemon", Level: ui.LevelWarn})
	}
	return nil
}
