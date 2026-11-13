package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/padmk/portside/internal/docker"
	"github.com/padmk/portside/internal/server"
	"github.com/padmk/portside/internal/supervisor"
	"github.com/padmk/portside/internal/tui/ui"
)

// Group, project and container names come from the config and the engine
// unchecked: with a newline, an escape sequence or a bidirectional
// override in them, every frame keeps its rows and none of those reaches
// the terminal, in the lists, the command line's completions, the palette,
// the menus and the dialogs.
func TestUntrustedNamesAreDrawnSafely(t *testing.T) {
	m := testModel(t)
	group := "sh\nop\u202eevil\x1b[31m"
	st := server.State{Groups: []string{group}, Servers: []supervisor.View{
		{Name: "web", Group: group, Enabled: true, Status: supervisor.Running},
		{Name: "api", Group: group, Enabled: true, Status: supervisor.Running},
	}}
	st.Docker = docker.State{Status: docker.Ready, Containers: []docker.Container{
		{ID: "id-db", Name: "db\u202e1", Project: "pr\noj\u202e\x1b[31m", Service: "d\nb\u202e", State: "running"},
	}}
	m.Update(ui.StateMsg{State: &st})
	typeText := func(s string) {
		for _, r := range s {
			m.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
		}
	}
	check := func(what string) {
		t.Helper()
		rows := strings.Split(m.screen(), "\n")
		if len(rows) != m.height {
			t.Errorf("%s: %d rows, want %d", what, len(rows), m.height)
		}
		for i, row := range rows {
			if w := ansi.StringWidth(row); w > m.width {
				t.Errorf("%s: row %d is %d cells wide", what, i, w)
			}
			if strings.Contains(row, "\u202e") || strings.Contains(row, "pwned") {
				t.Errorf("%s: row %d holds a name's control: %q", what, i, row)
			}
		}
	}
	check("the lists")
	press(m, ":")
	typeText("start-group ")
	check("the command line's completions")
	press(m, "esc")
	m.Update(tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
	typeText("group")
	check("the palette's groups")
	press(m, "esc", "x")
	check("the server menu")
	press(m, "esc", "2")
	check("the containers")
	press(m, "x")
	check("the container menu")
	press(m, "esc")
	m.Update(tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
	typeText("proj")
	check("the palette's projects")
	press(m, "esc", "d")
	if m.notice == nil {
		t.Fatal("d on a container outside a compose project says nothing")
	}
	check("a notice naming a container")
	m.Update(ui.ConfirmMsg{Title: "Stop group " + group, Body: "Stop " + group + "?\n\nSecond paragraph.", Confirm: "Stop"})
	check("a confirmation naming a group")
}
