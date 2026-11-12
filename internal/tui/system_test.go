package tui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/padmk/portside/internal/app"
	"github.com/padmk/portside/internal/client"
	"github.com/padmk/portside/internal/server"
	"github.com/padmk/portside/internal/supervisor"
	"github.com/padmk/portside/internal/tui/remote"
	"github.com/padmk/portside/internal/tui/ui"
)

func historyHas(m *model, text string) bool {
	for _, e := range m.history {
		if strings.Contains(e.Text, text) {
			return true
		}
	}
	return false
}

func TestOnReplacesTheSessionAndDropsLateReplies(t *testing.T) {
	m := testModel(t)
	// A restart whose reply comes after the switch: the request fails
	// at once, since the client points at a closed port.
	_, late := m.Update(ui.RunMsg{Action: "restart", Item: ui.ServerItem("web")})
	if m.env.Pending[ui.ServerItem("web")] == "" {
		t.Fatal("the restart is not pending")
	}
	cn := &client.Connection{}
	cn.Peer.Name, cn.Peer.Label, cn.Peer.DNSName = "studio", "studio", "studio.tail.ts.net"
	drive(m, func() tea.Msg {
		return remote.SwitchedMsg{Computer: "studio", Client: client.New(2), Conn: cn}
	})
	if m.env.Session.Computer != "studio" || m.env.Session.Conn != cn || m.gen != 1 {
		t.Fatalf("session %+v gen %d after the switch", m.env.Session, m.gen)
	}
	if m.env.State != nil || m.env.Selected.Kind != ui.SelectNone || len(m.env.Pending) != 0 || len(m.ops) != 0 {
		t.Fatalf("the switch kept the previous computer's state %v, selection %+v, pending %v", m.env.State != nil, m.env.Selected, m.env.Pending)
	}
	if !strings.Contains(m.topBar(), "studio") {
		t.Errorf("the top bar does not name the computer: %q", ansi.Strip(m.topBar()))
	}
	drive(m, late)
	if historyHas(m, "restart web") || len(m.env.Pending) != 0 {
		t.Error("the previous computer's reply reached the new session")
	}
	old := &server.State{Servers: []supervisor.View{{Name: "web", Status: supervisor.Running}}}
	m.Update(ui.GenMsg{Gen: 0, Msg: ui.StateMsg{State: old}})
	if m.env.State != nil {
		t.Fatal("a snapshot of the previous session's stream was taken")
	}
	now := &server.State{Servers: []supervisor.View{{Name: "db", Status: supervisor.Running}}}
	m.Update(ui.GenMsg{Gen: 1, Msg: ui.StateMsg{State: now}})
	if m.env.State != now || m.env.Selected != ui.ServerItem("db") {
		t.Fatalf("the new session's snapshot: state %v, selected %+v", m.env.State == now, m.env.Selected)
	}
	// Agent setup and the daemon's restart stay on this computer.
	drive(m, m.execute("setup"))
	if m.sys.Open() || m.notice == nil || !strings.Contains(m.notice.Text, "only on this computer") {
		t.Errorf(":setup on another computer: open %v, notice %+v", m.sys.Open(), m.notice)
	}
	// :on local returns.
	drive(m, m.execute("on local"))
	if m.env.Session.Remote() || m.gen != 2 || m.env.Client != m.sys.Local {
		t.Fatalf(":on local left session %+v gen %d", m.env.Session, m.gen)
	}
}

func TestUpgradeAsksAndReportsTheNewDaemon(t *testing.T) {
	defer func(v string) { app.Version = v }(app.Version)
	app.Version = "0.12.0"
	m := testModel(t)
	started := time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)
	st := *m.env.State
	st.Daemon = server.Health{Version: "0.11.0", PID: 42, Port: 7878, StartedAt: started}
	m.Update(ui.StateMsg{State: &st})

	drive(m, m.execute("restart-daemon"))
	if m.confirm == nil || m.confirm.Title != "Restart the daemon?" || m.dialog.Choice != 0 {
		t.Fatalf(":restart-daemon asks %+v", m.confirm)
	}
	press(m, "n")
	drive(m, m.execute("upgrade"))
	if m.confirm == nil || m.confirm.Title != "Upgrade the daemon?" || !strings.Contains(m.confirm.Body, "v0.11.0") || !strings.Contains(m.confirm.Body, "v0.12.0") {
		t.Fatalf(":upgrade asks %+v", m.confirm)
	}
	press(m, "y")
	if !strings.Contains(ansi.Strip(m.topBar()), "restarting, reconnecting") {
		t.Fatalf("the top bar during the upgrade: %q", ansi.Strip(m.topBar()))
	}
	// The connection drops and comes back without the toasts.
	m.Update(ui.ConnMsg{Connected: false})
	m.Update(ui.ConnMsg{Connected: true})
	if len(m.toasts) != 0 {
		t.Errorf("the restart toasted %+v", m.toasts)
	}
	// The same daemon answers first: still waiting.
	m.Update(ui.StateMsg{State: &st})
	if m.sys.Restarting() == "" {
		t.Fatal("the old daemon's snapshot ended the wait")
	}
	next := st
	next.Daemon.Version, next.Daemon.StartedAt = "0.12.0", started.Add(time.Minute)
	_, cmd := m.Update(ui.StateMsg{State: &next})
	drive(m, cmd)
	if m.sys.Restarting() != "" || !historyHas(m, "daemon upgraded in place from v0.11.0 to v0.12.0 (pid 42)") {
		t.Fatalf("after the new daemon answered: restarting %q, notice %+v", m.sys.Restarting(), m.notice)
	}
	// Now it runs this version: nothing to upgrade.
	drive(m, m.execute("upgrade"))
	if m.confirm != nil || m.notice == nil || !strings.Contains(m.notice.Text, "already runs this version (v0.12.0)") {
		t.Errorf(":upgrade on the same version: confirm %+v, notice %+v", m.confirm, m.notice)
	}
}

func TestTopBarPortOpensTheDaemonDetails(t *testing.T) {
	m := testModel(t)
	st := *m.env.State
	st.Daemon = server.Health{Version: "dev", PID: 7, Port: 7988}
	st.Config.StateDir = "/state"
	m.Update(ui.StateMsg{State: &st})
	m.screen()
	if m.portAt[1] <= m.portAt[0] {
		t.Fatal("the top bar keeps no place for the port")
	}
	m.Update(tea.MouseClickMsg{X: m.portAt[0], Y: 0, Button: tea.MouseLeft})
	screen := ansi.Strip(m.screen())
	for _, want := range []string{"Daemon", "pid 7", "/state/auth-token", "never sent"} {
		if !strings.Contains(screen, want) {
			t.Errorf("the daemon details lack %q", want)
		}
	}
}
