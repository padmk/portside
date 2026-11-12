package tui

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/padmk/portside/internal/app"
	"github.com/padmk/portside/internal/client"
	"github.com/padmk/portside/internal/server"
	"github.com/padmk/portside/internal/supervisor"
	"github.com/padmk/portside/internal/tui/keymap"
	"github.com/padmk/portside/internal/tui/live"
	"github.com/padmk/portside/internal/tui/overlay"
	"github.com/padmk/portside/internal/tui/theme"
	"github.com/padmk/portside/internal/tui/ui"
)

// testModel is the root model at 100x30 with a fixed clock and a snapshot
// of a group with a running, a crashed and a disabled server and two
// ungrouped ones. Its client points at a closed loopback port.
func testModel(t *testing.T) *model {
	t.Helper()
	t.Setenv(app.Env("STATE_DIR"), t.TempDir())
	t.Setenv(app.Env("CONFIG"), t.TempDir()+"/none.toml")
	m := newModel(client.New(1), live.New(), ui.Session{}, prefs{}, theme.TerminalFamily, "", theme.Unicode)
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	m.env.Now = func() time.Time { return now }
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m.Update(ui.ConnMsg{Connected: true})
	m.Update(ui.StateMsg{State: &server.State{Groups: []string{"shop"}, Servers: []supervisor.View{
		{Name: "web", Group: "shop", Enabled: true, Status: supervisor.Running},
		{Name: "api", Group: "shop", Enabled: true, Status: supervisor.Running},
		{Name: "worker", Group: "shop", Enabled: true, Status: supervisor.Crashed},
		{Name: "mailer", Group: "shop", Status: supervisor.Stopped},
		{Name: "blog", Enabled: true, Status: supervisor.Stopped},
		{Name: "chatty", Enabled: true, Status: supervisor.Running},
	}}})
	return m
}

// drive feeds the messages of cmd back into the model, as the program
// does, skipping commands that do not finish at once (streams, requests).
func drive(m *model, cmd tea.Cmd) {
	for depth := 0; cmd != nil && depth < 8; depth++ {
		done := make(chan tea.Msg, 1)
		go func() { done <- cmd() }()
		var msg tea.Msg
		select {
		case msg = <-done:
		case <-time.After(50 * time.Millisecond):
			return
		}
		if batch, ok := msg.(tea.BatchMsg); ok {
			for _, c := range batch {
				drive(m, c)
			}
			return
		}
		if msg == nil {
			return
		}
		_, cmd = m.Update(msg)
	}
}

func press(m *model, keys ...string) {
	for _, k := range keys {
		var msg tea.KeyPressMsg
		switch k {
		case "enter":
			msg = tea.KeyPressMsg{Code: tea.KeyEnter}
		case "esc":
			msg = tea.KeyPressMsg{Code: tea.KeyEscape}
		case "tab":
			msg = tea.KeyPressMsg{Code: tea.KeyTab}
		case "space":
			msg = tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}
		case "ctrl+p":
			msg = tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl}
		default:
			r := []rune(k)
			msg = tea.KeyPressMsg{Code: r[0], Text: k}
		}
		_, cmd := m.Update(msg)
		drive(m, cmd)
	}
}

func typed(s string) []string { return strings.Split(s, "") }

func TestSearchCommandFromServersOwnsTheLogs(t *testing.T) {
	m := testModel(t)
	press(m, ":")
	press(m, typed("search")...)
	press(m, "enter")
	right := len(m.left)
	if m.mode != ui.ModeSearch || m.focus != right || m.modeOwner != right {
		t.Fatalf("after :search mode %v, focus %d, owner %d; want SEARCH owned and focused by the logs (%d)", m.mode, m.focus, m.modeOwner, right)
	}
	press(m, "enter", "esc")
	if m.mode != ui.ModeNormal {
		t.Fatalf("enter then esc left mode %v", m.mode)
	}
	press(m, "?")
	if m.help == nil {
		t.Fatal("? after the search does not open the help")
	}
}

func TestFilterCommandFromLogsOwnsTheServers(t *testing.T) {
	m := testModel(t)
	m.focusPane(len(m.left))
	press(m, ":")
	press(m, typed("filter")...)
	press(m, "enter")
	if m.mode != ui.ModeFilter || m.focus != 0 || m.modeOwner != 0 {
		t.Fatalf("after :filter mode %v, focus %d, owner %d; want FILTER owned and focused by the servers", m.mode, m.focus, m.modeOwner)
	}
	press(m, typed("blog")...)
	if m.env.Selected != ui.ServerItem("blog") {
		t.Errorf("filter blog selected %+v, want blog", m.env.Selected)
	}
	press(m, "esc")
	if m.mode != ui.ModeNormal {
		t.Fatalf("esc left mode %v", m.mode)
	}
}

func TestFirstSnapshotSelectsTheServerThatNeedsAttention(t *testing.T) {
	m := testModel(t)
	if m.env.Selected != ui.ServerItem("worker") || m.env.Target != ui.ServerItem("worker") {
		t.Fatalf("first selection %+v, target %+v; want the crashed worker", m.env.Selected, m.env.Target)
	}
	// A later snapshot keeps the selection.
	press(m, "j")
	m.Update(ui.StateMsg{State: m.env.State})
	if m.env.Selected != ui.ServerItem("mailer") {
		t.Errorf("after j and a snapshot: %+v, want mailer", m.env.Selected)
	}
	for _, c := range []struct {
		servers []supervisor.View
		want    string
	}{
		{[]supervisor.View{{Name: "a", Status: supervisor.Stopped}, {Name: "b", Status: supervisor.Running}}, "b"},
		{[]supervisor.View{{Name: "a", Status: supervisor.Running}, {Name: "b", Status: supervisor.Running, Unready: true}}, "b"},
		{[]supervisor.View{{Name: "a", Status: supervisor.Stopped}, {Name: "b", Status: supervisor.Stopped}}, "a"},
		{[]supervisor.View{{Name: "a", Status: supervisor.Running}, {Name: "b", Status: supervisor.Exited, ExitCode: new(1)}}, "b"},
		{[]supervisor.View{{Name: "a", Status: supervisor.Stopped}, {Name: "b", Status: supervisor.Exited, ExitCode: new(0)}}, "a"},
	} {
		if got := firstToShow(c.servers).Name; got != c.want {
			t.Errorf("firstToShow %+v = %s, want %s", c.servers, got, c.want)
		}
	}
}

func TestRightClickTargetsTheRowUnderThePointer(t *testing.T) {
	m := testModel(t)
	m.Update(ui.SelectMsg{Item: ui.ServerItem("web")})
	// Rows: top bar, border, shop, web, api, worker, mailer, other, blog.
	m.Update(tea.MouseClickMsg{X: 6, Y: 8, Button: tea.MouseRight})
	if m.menu == nil || m.menu.target != ui.ServerItem("blog") {
		t.Fatalf("right click on blog: menu %+v", m.menu)
	}
	// The selection moves before the item runs; the menu keeps blog.
	m.env.Target, m.env.Selected = ui.ServerItem("web"), ui.ServerItem("web")
	for i, it := range m.menu.Items {
		if it.ActionID == "restart" {
			m.menuRun(i)
		}
	}
	if _, ok := m.ops[ui.ServerItem("blog")]; !ok {
		t.Errorf("restart from the menu went to %v, want blog", m.ops)
	}
	if _, ok := m.ops[ui.ServerItem("web")]; ok {
		t.Error("restart from the menu reached web")
	}
}

func TestEmptyFilterLeavesNothingToActOn(t *testing.T) {
	m := testModel(t)
	m.Update(ui.SelectMsg{Item: ui.ServerItem("web")})
	press(m, "/")
	press(m, typed("no-such-server")...)
	press(m, "enter")
	if m.env.Target.Kind != ui.SelectNone {
		t.Fatalf("a filter that matches nothing keeps the target %+v", m.env.Target)
	}
	// A new snapshot does not bring the hidden server back as the target.
	m.Update(ui.StateMsg{State: m.env.State})
	press(m, "d")
	if m.confirm != nil {
		t.Fatalf("d with no row asks %q", m.confirm.Body)
	}
	if m.notice == nil || m.notice.Text != "delete: nothing selected" {
		t.Errorf("d with no row: notice %+v", m.notice)
	}
	press(m, "space")
	if len(m.ops) != 0 {
		t.Errorf("space with no row started %v", m.ops)
	}
	// The right pane keeps showing web, marked as a preview.
	if !strings.Contains(ansi.Strip(m.screen()), "web (preview)") {
		t.Error("the detail does not say it shows a preview")
	}
}

func TestReturningToAGroupHeaderKeepsItTheTarget(t *testing.T) {
	m := testModel(t)
	m.Update(ui.SelectMsg{Item: ui.ServerItem("web")})
	press(m, "k", "3", "1")
	if m.env.Target != ui.GroupItem("shop") {
		t.Fatalf("back on the servers: target %+v, want the shop header under the cursor", m.env.Target)
	}
	press(m, "x")
	if m.menu == nil || m.menu.target != ui.GroupItem("shop") || m.menu.Title != "group shop (4)" {
		t.Fatalf("x on the header after 3, 1: menu %+v", m.menu)
	}
}

func TestRightClickFromAnotherPaneTargetsTheRow(t *testing.T) {
	m := testModel(t)
	withContainers(m)
	press(m, "2")
	if m.env.Selected.Kind != ui.SelectContainer {
		t.Fatalf("2 selects %+v", m.env.Selected)
	}
	// The servers list takes the focus in the same message as the click,
	// while the selection still is the container.
	m.focusPane(0)
	m.Update(tea.MouseClickMsg{X: 6, Y: 8, Button: tea.MouseRight})
	if m.menu == nil || m.menu.target != ui.ServerItem("blog") || m.menu.Title != "blog" {
		t.Fatalf("right click on blog from the containers: menu %+v", m.menu)
	}
	if m.env.Selected != ui.ServerItem("blog") {
		t.Errorf("the right pane shows %+v, want blog", m.env.Selected)
	}
}

func TestGroupHeaderMenuCountsEligibleMembers(t *testing.T) {
	m := testModel(t)
	m.Update(ui.SelectMsg{Item: ui.ServerItem("web")})
	press(m, "k") // from web to the shop header
	if m.env.Target != ui.GroupItem("shop") {
		t.Fatalf("target %+v, want the shop header", m.env.Target)
	}
	press(m, "x")
	if m.menu == nil {
		t.Fatal("x on a group header opens no menu")
	}
	var labels []string
	for _, it := range m.menu.Items {
		labels = append(labels, it.Label)
	}
	// worker is the only enabled member without a process; mailer is
	// disabled.
	want := "Restart group (2)|Start group (1)|Stop group (2)|All actions"
	if got := strings.Join(labels, "|"); got != want {
		t.Errorf("menu %q, want %q", got, want)
	}
	if m.menu.target != ui.GroupItem("shop") || m.menu.Title != "group shop (4)" {
		t.Errorf("menu target %+v, title %q", m.menu.target, m.menu.Title)
	}
}

func TestOperationsDoNotOverlap(t *testing.T) {
	m := testModel(t)
	web := ui.ServerItem("web")
	m.act("restart", web)
	first := m.ops[web].id
	if first == 0 || m.env.Pending[web] != "restarting" {
		t.Fatalf("restart web: ops %v, pending %v", m.ops, m.env.Pending)
	}
	m.act("restart", web)
	m.act("restart", ui.GroupItem("shop"))
	if m.opSeq != first {
		t.Fatalf("a second restart and a group restart over web started operations (%d)", m.opSeq)
	}
	if m.notice == nil || m.notice.Text != "web is already restarting" {
		t.Errorf("notice %+v", m.notice)
	}
	// The first completes and a second restart follows; a late reply of
	// the first must not clear the second's mark.
	m.Update(ui.OpDoneMsg{ID: first, Items: []ui.Selection{web}})
	m.act("restart", web)
	second := m.ops[web].id
	m.Update(ui.OpDoneMsg{ID: first, Items: []ui.Selection{web}})
	if m.ops[web].id != second || m.env.Pending[web] == "" {
		t.Errorf("a stale completion cleared operation %d: ops %v", second, m.ops)
	}
}

func TestFilterStateUsesDisplayStateAndKeepsTheCursorVisible(t *testing.T) {
	m := testModel(t)
	press(m, ":")
	press(m, typed("filter-state crashed")...)
	press(m, "enter")
	if m.env.Selected != ui.ServerItem("worker") || m.env.Target != ui.ServerItem("worker") {
		t.Errorf("filter-state crashed: selected %+v, target %+v; want worker", m.env.Selected, m.env.Target)
	}
	screen := m.screen()
	if strings.Contains(screen, "blog") || !strings.Contains(screen, "worker") {
		t.Errorf("filter-state crashed shows:\n%s", screen)
	}
}

func TestRemoteSessionKeepsDesktopActionsHere(t *testing.T) {
	m := testModel(t)
	var conn client.Connection
	if err := json.Unmarshal([]byte(`{"peer":{"name":"mbp","label":"mbp"},"localPort":7878,"mirrors":[{"port":3000,"local":4000,"state":"mirrored"}]}`), &conn); err != nil {
		t.Fatal(err)
	}
	m.env.Session = ui.Session{Computer: "mbp", Conn: &conn}
	for _, id := range []string{"shell", "editor", "terminal"} {
		m.notice = nil
		drive(m, m.act(id, ui.ServerItem("web")))
		if m.notice == nil || !strings.Contains(m.notice.Text, "only on this computer") {
			t.Errorf("%s on another computer: notice %+v", id, m.notice)
		}
	}
	if got := m.env.Session.URL("web", "http://10.0.0.2:3000/x"); got != "http://localhost:4000/x" {
		t.Errorf("forwarded URL %q", got)
	}
}

func TestEveryActionHasAnExecutor(t *testing.T) {
	m := testModel(t)
	globals := m.globals()
	for _, a := range m.env.Keys.All() {
		switch {
		case a.Scope == keymap.ScopeGlobal:
			if globals[a.ID] == nil {
				t.Errorf("global %s has no executor", a.ID)
			}
		case a.Scope == keymap.ScopeServers && targetActions[a.ID]:
		case a.Scope == keymap.ScopeServers && formActions[a.ID]:
		default:
			if !slices.Contains(paneActions[a.Scope], a.ID) {
				t.Errorf("%s %s: no pane carries it out", a.Scope, a.ID)
			}
		}
	}
	for id := range globals {
		if a, ok := m.env.Keys.ByID(id); !ok || a.Scope != keymap.ScopeGlobal {
			t.Errorf("executor %s has no global action", id)
		}
	}
}

func TestFooterKeepsMenuAndHelpAtEveryWidth(t *testing.T) {
	m := testModel(t)
	for _, w := range []int{40, 60, 80, 100, 160} {
		m.Update(tea.WindowSizeMsg{Width: w, Height: 30})
		raw := m.footer()
		f := ansi.Strip(raw)
		if !strings.Contains(f, "x menu") || !strings.Contains(f, "? keys") {
			t.Errorf("width %d footer %q lacks x menu or ? keys", w, f)
		}
		if lipgloss.Width(raw) > w {
			t.Errorf("width %d footer is %d wide: %q", w, lipgloss.Width(raw), f)
		}
	}
	// The space hint says what it does to the crashed worker.
	m.Update(tea.WindowSizeMsg{Width: 160, Height: 30})
	if f := ansi.Strip(m.footer()); !strings.Contains(f, "␣ start") || strings.Contains(f, "start/stop") {
		t.Errorf("footer on worker %q, want ␣ start", f)
	}
	// Actions the menu marks off for the crashed worker are not offered;
	// the next useful ones take their place.
	if f := ansi.Strip(m.footer()); strings.Contains(f, "a attach") || strings.Contains(f, "o open") || !strings.Contains(f, "e edit") || !strings.Contains(f, "logs") {
		t.Errorf("footer on worker %q offers what is off or lacks edit and logs", f)
	}
	// Narrow, the main panel says how to go back.
	m.Update(tea.WindowSizeMsg{Width: 60, Height: 20})
	m.focusPane(len(m.left))
	if f := ansi.Strip(m.footer()); !strings.Contains(f, "esc back") {
		t.Errorf("narrow main panel footer %q lacks esc back", f)
	}
}

func TestDetailTabsMenuActsOnTheShownServer(t *testing.T) {
	m := testModel(t)
	m.focusPane(len(m.left))
	press(m, "]") // Details
	if m.focused().Scope() != keymap.ScopeDetail {
		t.Fatalf("after ] the scope is %s", m.focused().Scope())
	}
	if !strings.Contains(ansi.Strip(m.footer()), "x menu") {
		t.Errorf("details footer %q lacks x menu", m.footer())
	}
	press(m, "x")
	if m.menu == nil || m.menu.Title != "worker" || m.menu.target != ui.ServerItem("worker") {
		t.Fatalf("details menu %+v", m.menu)
	}
	for _, it := range m.menu.Items {
		if !strings.HasPrefix(it.Key, ":") && it.ActionID != "palette" {
			t.Errorf("details menu item %s shows key %q, which the tab does not bind", it.ActionID, it.Key)
		}
		if it.ActionID == "attach" && it.Off != "not running" {
			t.Errorf("attach to the crashed worker: off %q", it.Off)
		}
	}
}

func TestConfirmationDefaultsToCancel(t *testing.T) {
	m := testModel(t)
	ran := 0
	ask := func() {
		m.Update(ui.ConfirmMsg{Title: "Delete server", Body: "Remove web from the config?", Confirm: "Delete", Yes: func() tea.Msg { ran++; return nil }})
	}
	run := func(cmd tea.Cmd) {
		if cmd != nil {
			cmd()
		}
	}
	ask()
	if !strings.Contains(m.screen(), "Remove web from the config?") {
		t.Error("the dialog does not show its body")
	}
	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	run(cmd)
	if ran != 0 || m.confirm != nil {
		t.Fatalf("enter on the default choice ran it (%d) or left the dialog open", ran)
	}
	ask()
	press(m, "tab")
	_, cmd = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	run(cmd)
	if ran != 1 {
		t.Fatalf("tab then enter ran %d times", ran)
	}
	ask()
	press(m, "q")
	if m.confirm != nil || ran != 1 {
		t.Fatal("q does not cancel the dialog")
	}
	// A click on the confirmation runs it.
	ask()
	m.screen()
	d := m.dialog
	for cx := 0; cx < 60; cx++ {
		for cy := 0; cy < 12; cy++ {
			if d.Hit(cx, cy) == 1 {
				_, cmd = m.Update(tea.MouseClickMsg{X: m.dialogAt[0] + cx, Y: m.dialogAt[1] + cy, Button: tea.MouseLeft})
				run(cmd)
				if ran != 2 || m.confirm != nil {
					t.Fatalf("a click on the confirmation: ran %d, open %v", ran, m.confirm != nil)
				}
				return
			}
		}
	}
	t.Fatal("the dialog has no clickable confirmation")
}

func TestCommandsCheckTheirArguments(t *testing.T) {
	m := testModel(t)
	for _, c := range []struct{ line, want string }{
		{"clear-logs blgo", `no server named "blgo"; did you mean blog?`},
		{"restart web api", "takes one name"},
		{"messages now", "takes no argument"},
		{"start-group web", "web is a server in group shop"},
		{"filter-state crashd", "did you mean crashed?"},
		{"restart mailer", "restart mailer: disabled"},
		{"stop blog", "stop blog: not running"},
	} {
		m.notice = nil
		drive(m, m.execute(c.line))
		if m.notice == nil || !strings.Contains(m.notice.Text, c.want) {
			t.Errorf(":%s: notice %+v, want %q", c.line, m.notice, c.want)
		}
	}
	// clear-logs takes the name it is given, not the selection.
	drive(m, m.execute("clear-logs blog"))
	if m.confirm == nil || !strings.Contains(m.confirm.Body, "blog") {
		t.Errorf(":clear-logs blog asks %+v", m.confirm)
	}
}

func TestCommandLineShowsCandidatesWhileTyping(t *testing.T) {
	m := testModel(t)
	press(m, ":")
	press(m, typed("restart b")...)
	rows := m.footerRows()
	if len(rows) != 2 || !strings.Contains(rows[0], "blog") {
		t.Fatalf("candidates while typing: %q", rows)
	}
	press(m, "tab")
	if got := m.cmdLine.Value(); got != "restart blog" {
		t.Errorf("tab completed %q", got)
	}
}

func TestPaletteRunsWhatItShows(t *testing.T) {
	m := testModel(t)
	press(m, "ctrl+p")
	if m.palette == nil {
		t.Fatal("ctrl+p opens no palette")
	}
	press(m, typed("restart blog")...)
	it, ok := m.palette.Selected()
	if !ok || it.Command != "restart blog" {
		t.Fatalf("restart blog highlights %+v", it)
	}
	// The request goes to the closed test port and fails: the failure
	// names the restart of blog.
	press(m, "enter")
	if m.opSeq != 1 || m.notice == nil || !strings.HasPrefix(m.notice.Text, "restart blog") {
		t.Errorf("the palette did not restart blog: op %d, notice %+v", m.opSeq, m.notice)
	}
	// The target's own entries carry the key that does the same here.
	press(m, "ctrl+p")
	press(m, typed("start worker")...)
	it, _ = m.palette.Selected()
	if it.Command != "toggle worker" || it.Key != "␣" {
		t.Errorf("start worker: %+v", it)
	}
	press(m, "esc")
	if m.palette != nil {
		t.Error("esc does not close the palette")
	}
}

func TestHelpStartsWithTheFocusedPane(t *testing.T) {
	m := testModel(t)
	m.focusPane(len(m.left))
	got := m.helpScopes()
	want := []keymap.Scope{keymap.ScopeLogs, keymap.ScopeGlobal, keymap.ScopeServers, keymap.ScopeDetail}
	if !slices.Equal(got, want) {
		t.Errorf("help scopes from the logs %v, want %v", got, want)
	}
}

func TestToastsSelectTheirServerAndCoalesce(t *testing.T) {
	m := testModel(t)
	st := *m.env.State
	st.Servers = slices.Clone(st.Servers)
	st.Servers[4].Status = supervisor.Crashed // blog
	m.Update(ui.StateMsg{State: &st})
	m.screen()
	if m.toastAt.w == 0 {
		t.Fatal("no toast after blog crashed")
	}
	m.Update(tea.MouseClickMsg{X: m.toastAt.x + 2, Y: m.toastAt.y + 1, Button: tea.MouseLeft})
	if m.env.Selected != ui.ServerItem("blog") {
		t.Errorf("a click on the toast selected %+v", m.env.Selected)
	}
	// Two crash at once: one toast, both in the history.
	st2 := st
	st2.Servers = slices.Clone(st.Servers)
	st2.Servers[0].Status, st2.Servers[1].Status = supervisor.Crashed, supervisor.Crashed
	before := len(m.history)
	m.toasts = nil
	m.Update(ui.StateMsg{State: &st2})
	if len(m.toasts) != 1 || !strings.HasPrefix(m.toasts[0].Title, "2 servers crashed") {
		t.Errorf("toasts %+v", m.toasts)
	}
	if len(m.history)-before != 2 {
		t.Errorf("history grew by %d, want 2", len(m.history)-before)
	}
}

func TestServerMenuFitsTheSmallestScreen(t *testing.T) {
	m := testModel(t)
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	press(m, "x", "k") // up from the first item wraps to the last
	if m.menu == nil {
		t.Fatal("x opens no menu")
	}
	_, h := m.menu.Size()
	if m.menu.y < m.bodyTop() || m.menu.y+h > m.height-1 {
		t.Fatalf("the menu spans rows %d to %d of 24", m.menu.y, m.menu.y+h)
	}
	last := m.menu.Items[len(m.menu.Items)-1]
	if m.menu.Cursor != len(m.menu.Items)-1 || !strings.Contains(ansi.Strip(m.screen()), last.Label) {
		t.Errorf("the last item %q is not on screen with the cursor on it", last.Label)
	}
	// The menu holds the server's and its group's actions, then the
	// palette, which has the rest.
	for _, section := range []string{"Server", "Group"} {
		if !slices.ContainsFunc(m.menu.Items, func(it overlay.MenuItem) bool { return it.Section == section }) {
			t.Errorf("no %s section in the server menu", section)
		}
	}
	for _, it := range m.menu.Items {
		if it.Section != "Server" && it.Section != "Group" && it.ActionID != "palette" {
			t.Errorf("the server menu holds %s (%s)", it.ActionID, it.Section)
		}
	}
	if last.ActionID != "palette" {
		t.Errorf("the last item is %s, want the palette", last.ActionID)
	}
}

func TestCtrlCQuitsOverADialogAndQClosesOverlays(t *testing.T) {
	m := testModel(t)
	m.Update(ui.ConfirmMsg{Title: "Delete server", Body: "Remove web?", Confirm: "Delete", Yes: func() tea.Msg { return nil }})
	_, cmd := m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	if cmd == nil {
		t.Fatal("ctrl+c over a dialog does nothing")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Error("ctrl+c over a dialog does not quit")
	}
	m.confirm, m.dialog = nil, nil
	// q closes an overlay, never the program; in the palette it is text.
	for _, open := range []string{"?", "x"} {
		press(m, open)
		_, cmd := m.Update(tea.KeyPressMsg{Code: 'q', Text: "q"})
		if m.help != nil || m.menu != nil {
			t.Errorf("q leaves the %s overlay open", open)
		}
		if cmd != nil {
			if _, ok := cmd().(tea.QuitMsg); ok {
				t.Errorf("q in the %s overlay quits", open)
			}
		}
	}
	press(m, "ctrl+p", "q")
	if m.palette == nil || m.palette.Query() != "q" {
		t.Error("q in the palette is not typed")
	}
}
