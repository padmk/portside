package tui

import (
	"slices"
	"strings"
	"testing"

	"github.com/padmk/portside/internal/docker"
	"github.com/padmk/portside/internal/tui/keymap"
	"github.com/padmk/portside/internal/tui/overlay"
	"github.com/padmk/portside/internal/tui/ports"
	"github.com/padmk/portside/internal/tui/ui"
)

// withContainers gives the test model's snapshot a compose project.
func withContainers(m *model) {
	st := *m.env.State
	st.Docker = docker.State{Status: docker.Ready, Containers: []docker.Container{
		{ID: "id-db", Name: "shop-db-1", Project: "shop", Service: "db", WorkingDir: "/srv/shop", State: "running", Server: "web"},
		{ID: "id-cache", Name: "shop-cache-1", Project: "shop", Service: "cache", WorkingDir: "/srv/shop", State: "exited"},
	}}
	m.Update(ui.StateMsg{State: &st})
}

func TestNumberKeysFocusContainersAndPorts(t *testing.T) {
	m := testModel(t)
	press(m, "2")
	if m.focused().Scope() != keymap.ScopeServers || m.notice == nil || !strings.Contains(m.notice.Text, "no containers pane") {
		t.Fatalf("2 without containers: focus %s, notice %+v", m.focused().Scope(), m.notice)
	}
	withContainers(m)
	press(m, "2")
	if m.focused().Scope() != keymap.ScopeContainers {
		t.Fatalf("2 focuses %s", m.focused().Scope())
	}
	// The container under the cursor is what the right pane shows.
	if m.env.Selected.Kind != ui.SelectContainer || m.env.Selected.Name != "shop-db-1" {
		t.Errorf("selection %+v, want the first container", m.env.Selected)
	}
	press(m, "3")
	if m.focused().Scope() != keymap.ScopePorts || !m.showPorts {
		t.Fatalf("3 focuses %s, ports shown %v", m.focused().Scope(), m.showPorts)
	}
	press(m, "esc")
	if m.showPorts || m.focused().Scope() == keymap.ScopePorts {
		t.Errorf("esc keeps the ports pane: shown %v, focus %s", m.showPorts, m.focused().Scope())
	}
	press(m, "1")
	if m.env.Selected.Kind != ui.SelectServer {
		t.Errorf("1 leaves the selection on %+v", m.env.Selected)
	}
	// Containers that go hide the pane and give the selection back.
	press(m, "2")
	st := *m.env.State
	st.Docker = docker.State{Status: docker.Ready}
	m.Update(ui.StateMsg{State: &st})
	if len(m.left) != 1 || m.env.Selected.Kind != ui.SelectServer {
		t.Errorf("after the containers went: %d left panes, selection %+v", len(m.left), m.env.Selected)
	}
}

func TestContainerActionsRunOnTheCursorAndAskBeforeDown(t *testing.T) {
	m := testModel(t)
	withContainers(m)
	press(m, "2", "j")
	press(m, "S")
	// The client reaches no daemon, so the request fails at once and says
	// what it ran on.
	if m.notice == nil || !strings.HasPrefix(m.notice.Text, "start shop-cache-1: ") {
		t.Errorf("S on the exited cache: notice %+v", m.notice)
	}
	press(m, "a")
	if m.notice == nil || !strings.Contains(m.notice.Text, "not running") {
		t.Errorf("a on an exited container: notice %+v", m.notice)
	}
	press(m, "d")
	if m.confirm == nil || !strings.Contains(m.confirm.Body, "all containers in shop") {
		t.Fatalf("d asks %+v", m.confirm)
	}
	press(m, "n")
	if _, busy := m.ops[ui.ProjectItem("shop")]; busy {
		t.Error("a cancelled down started")
	}
}

func TestPortTakeoverIsAServerAction(t *testing.T) {
	m := testModel(t)
	why := m.off("takeover", ui.ServerItem("blog"))
	if why != "its port is not held by another process" {
		t.Errorf("takeover on a stopped server: %q", why)
	}
}

func TestSnapshotsKeepAContainerOrProjectSelection(t *testing.T) {
	m := testModel(t)
	withContainers(m)
	press(m, "j", "2")
	server := m.lastServer
	ctr := m.env.Selected
	if ctr.Kind != ui.SelectContainer {
		t.Fatalf("2 selects %+v", ctr)
	}
	withContainers(m)
	m.Update(ui.SelectMsg{Item: ctr})
	if m.env.Selected != ctr || m.env.Target != ctr {
		t.Fatalf("a snapshot moved the selection to %+v, target %+v", m.env.Selected, m.env.Target)
	}
	// A project header as the target survives a snapshot too.
	press(m, "k")
	if m.env.Target.Kind != ui.SelectProject {
		t.Fatalf("k from the first container targets %+v", m.env.Target)
	}
	target := m.env.Target
	withContainers(m)
	if m.env.Target != target {
		t.Errorf("a snapshot moved the project target to %+v", m.env.Target)
	}
	// Back in the servers list, the cursor's server is selected again.
	press(m, "1")
	if m.env.Selected != server || m.env.Target != server {
		t.Errorf("1 selects %+v, target %+v, want %+v", m.env.Selected, m.env.Target, server)
	}
}

func TestPaletteListsEachTargetActionOncePerTarget(t *testing.T) {
	m := testModel(t)
	withContainers(m)
	labels := map[string]int{}
	for _, it := range m.paletteItems() {
		labels[it.Label]++
	}
	for _, generic := range []string{"Containers: Restart", "Containers: Start or stop", "Ports: Open in browser", "Ports: Kill the process (asks first)"} {
		if labels[generic] > 0 {
			t.Errorf("generic entry %q listed", generic)
		}
	}
	for _, want := range []string{"Restart container shop-db-1", "Start container shop-cache-1", "Stop project shop", "Restart web", "Restart group shop (2)"} {
		if labels[want] != 1 {
			t.Errorf("%q listed %d times", want, labels[want])
		}
	}
	for l, n := range labels {
		if n > 1 {
			t.Errorf("%q listed %d times", l, n)
		}
	}
	// From the servers list, a container's entry runs on the container.
	press(m, "ctrl+p")
	press(m, typed("start container shop-cache")...)
	it, _ := m.palette.Selected()
	if it.Label != "Start container shop-cache-1" || it.Key != "" {
		t.Fatalf("start container shop-cache highlights %+v", it)
	}
	press(m, "enter")
	if m.notice == nil || !strings.HasPrefix(m.notice.Text, "start shop-cache-1: ") {
		t.Errorf("the palette did not start the container: notice %+v", m.notice)
	}
	// On the containers pane, the cursor's container has its keys.
	press(m, "2")
	var restart overlay.PaletteItem
	for _, it := range m.paletteItems() {
		if it.Label == "Restart container shop-db-1" {
			restart = it
		}
	}
	if restart.Key != "r" {
		t.Errorf("the cursor's container: %+v", restart)
	}
}

func TestKillOnAManagedPortAsksToStopTheServer(t *testing.T) {
	m := testModel(t)
	l := ports.Listener{Server: "api", Managed: true}
	l.Port, l.PID, l.Command = 3102, 42, "python3"
	cmd := m.listenerAct("kill", ports.Item(l), l, false)
	if len(m.ops) != 0 {
		t.Fatalf("kill on a managed port stopped at once: %v", m.ops)
	}
	c, ok := cmd().(ui.ConfirmMsg)
	if !ok || c.Title != "Stop server api" || !strings.Contains(c.Body, "Stop server api on port 3102?") || c.Confirm != "Stop" {
		t.Fatalf("kill on a managed port asks %+v", c)
	}
	again, ok := c.Yes().(ui.ActMsg)
	if !ok || !again.Confirmed || again.ID != "kill" {
		t.Fatalf("the confirmation runs %+v", again)
	}
	m.listenerAct(again.ID, again.Item, l, true)
	if _, ok := m.ops[ui.ServerItem("api")]; !ok {
		t.Errorf("the confirmed kill does not stop api: %v", m.ops)
	}
}

func TestCommandsResolveTheirArgumentByKind(t *testing.T) {
	m := testModel(t)
	withContainers(m)
	// The client reaches no daemon: each request fails at once and its
	// notice names what it ran on.
	for _, c := range []struct{ line, want string }{
		{"restart shop-db-1", "restart shop-db-1: "},
		{"stop-project shop", "stop project shop: "},
		{"restart blog", "restart blog: "},
		{"restart shop-db-x", `no server or group named "shop-db-x"`},
	} {
		m.ops = map[ui.Selection]op{}
		m.env.Pending = map[ui.Selection]string{}
		press(m, ":")
		press(m, typed(c.line)...)
		press(m, "enter")
		if m.notice == nil || !strings.Contains(m.notice.Text, c.want) {
			t.Errorf(":%s: notice %+v, want %q", c.line, m.notice, c.want)
		}
	}
	// The palette's entries for containers and projects show a command
	// that the command line runs on the same item.
	for _, it := range m.paletteItems() {
		if it.Label != "Restart container shop-cache-1" && it.Label != "Stop project shop" {
			continue
		}
		id, args, _ := keymap.ParseCommand(it.Command)
		a, err := m.env.Keys.Command(id)
		if err != nil || len(args) != 1 {
			t.Fatalf("%q shows %q", it.Label, it.Command)
		}
		a = m.commandVariant(a, args)
		if a.Scope != keymap.ScopeContainers || m.checkName(a, args[0]) != nil {
			t.Errorf("%q: :%s resolves to the %s action", it.Label, it.Command, a.Scope)
		}
	}
	// Global actions carry their real reason in the palette.
	for _, it := range m.paletteItems() {
		if it.Command == "upgrade" && it.Off != m.systemOff("upgrade") {
			t.Errorf("upgrade entry off %q, want %q", it.Off, m.systemOff("upgrade"))
		}
	}
}

func TestMenusResolveForTheShownSource(t *testing.T) {
	m := testModel(t)
	withContainers(m)
	item := func(id string) overlay.MenuItem {
		for _, it := range m.menu.Items {
			if it.ActionID == id {
				return it
			}
		}
		t.Fatalf("menu %q has no %s", m.menu.Title, id)
		return overlay.MenuItem{}
	}
	// The crashed worker's logs: toggles with their state, attach off.
	m.focusPane(len(m.left))
	press(m, "x")
	if m.menu == nil || m.menu.Title != "Logs of worker" {
		t.Fatalf("the server's logs menu: %+v", m.menu)
	}
	if item("follow").Check != overlay.CheckOn || item("wrap").Check != overlay.CheckOff {
		t.Errorf("follow %v, wrap %v; want on and off", item("follow").Check, item("wrap").Check)
	}
	if item("attach").Off != "not running" {
		t.Errorf("attach to the crashed worker: off %q", item("attach").Off)
	}
	press(m, "esc", "w", "x")
	if item("wrap").Check != overlay.CheckOn {
		t.Error("w does not check wrap in the menu")
	}
	press(m, "esc")
	// A container's logs offer its shell, off while it is not running.
	press(m, "2", "j")
	m.focusPane(len(m.left))
	press(m, "x")
	if m.menu == nil || m.menu.Title != "Logs of container shop-cache-1" {
		t.Fatalf("the container's logs menu: %+v", m.menu)
	}
	if sh := item("attach"); sh.Label != "Shell" || sh.Off == "" {
		t.Errorf("the exited container's shell item %+v", sh)
	}
	press(m, "esc", "]", "x") // Details
	if m.menu == nil || m.menu.target.Name != "shop-cache-1" {
		t.Fatalf("the container's Details menu: %+v", m.menu)
	}
	if r := item("restart"); r.Key != ":restart" {
		t.Errorf("Details restart item %+v", r)
	}
}

// TestPaletteRanksTheActionWordFirst: an action prefix finds the cursor
// target's action first, the daemon-wide restart never comes before a
// server's, and scattered letters still find their entry.
func TestPaletteRanksTheActionWordFirst(t *testing.T) {
	m := testModel(t)
	withContainers(m)
	press(m, "j", "j", "j")
	target := m.env.Target.Name
	shown := func(query string) []string {
		press(m, "ctrl+p")
		press(m, typed(query)...)
		defer press(m, "esc")
		var out []string
		for range 400 {
			it, ok := m.palette.Selected()
			if !ok || len(out) > 0 && it.Label == out[0] {
				break
			}
			out = append(out, it.Label)
			m.palette.Down()
		}
		return out
	}
	res := shown("res")
	if len(res) == 0 || res[0] != "Restart "+target {
		t.Fatalf("res ranks %q first, want Restart %s", res, target)
	}
	if d, w := slices.Index(res, "Restart the daemon in place"), slices.Index(res, "Restart web"); d < 0 || w < 0 || d < w {
		t.Errorf("res ranks the daemon at %d, Restart web at %d", d, w)
	}
	if !slices.Contains(shown("rw"), "Restart web") {
		t.Error("rw does not find Restart web")
	}
}
