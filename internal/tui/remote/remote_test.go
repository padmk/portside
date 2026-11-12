package remote

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/padmk/portside/internal/agentsetup"
	"github.com/padmk/portside/internal/app"
	"github.com/padmk/portside/internal/client"
	"github.com/padmk/portside/internal/server"
	"github.com/padmk/portside/internal/tui/theme"
	"github.com/padmk/portside/internal/tui/ui"
)

func testEnv(session ui.Session) *ui.Env {
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	return &ui.Env{Session: session, Styles: theme.New(theme.TerminalFamily.Variant(true)), Glyphs: theme.Unicode,
		Now: func() time.Time { return now }}
}

// fixed answers of GET /api/remote and GET /api/remote/peers: sharing on
// with a paired device, connected to studio with two ports.
const (
	overviewJSON = `{"share":{"enabled":true,"active":true,"url":"https://acropolis.tail.ts.net:7979","devices":[{"id":"d1","name":"laptop","dnsName":"laptop.tail.ts.net","pairedAt":"2026-11-01T00:00:00Z","connected":true}]},
		"connection":{"peer":{"name":"studio","dnsName":"studio.tail.ts.net","label":"studio","os":"linux"},"status":"open","localPort":7878,"since":"2026-11-12T11:00:00Z",
		"mirrors":[{"port":3000,"server":"web","state":"mirrored"},{"port":5432,"local":15432,"container":"db","state":"off"}]}}`
	peersJSON = `{"tailnet":{"available":true,"self":"acropolis"},"peers":[
		{"name":"studio","dnsName":"studio.tail.ts.net","label":"studio","os":"linux","version":"0.11.3","paired":true,"servers":{"total":5,"running":3},"connected":true},
		{"name":"mini","dnsName":"mini.tail.ts.net","label":"mini","os":"macOS","version":"0.10.0","paired":false,"connected":false}],"prompt":"..."}`
)

func fixedComputers(t *testing.T) *computers {
	t.Helper()
	c := &computers{seq: 1}
	var ov server.RemoteOverview
	var d Discovery
	if err := json.Unmarshal([]byte(overviewJSON), &ov); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(peersJSON), &d); err != nil {
		t.Fatal(err)
	}
	env := testEnv(ui.Session{})
	c.update(nil, env, overviewMsg{seq: 1, ov: ov})
	c.update(nil, env, peersMsg{seq: 1, disc: d})
	c.update(nil, env, peersMsg{seq: 0, disc: Discovery{}}) // a reply to an earlier scan
	return c
}

func TestComputersRowsFromTheRemoteAPI(t *testing.T) {
	c := fixedComputers(t)
	var kinds []rowKind
	for _, r := range c.rows() {
		kinds = append(kinds, r.kind)
	}
	if want := []rowKind{rowThis, rowPeer, rowPeer, rowMirror, rowMirror}; !slices.Equal(kinds, want) {
		t.Fatalf("rows %v, want %v", kinds, want)
	}
	lines, _ := c.lines(testEnv(ui.Session{Computer: "studio"}), 90, time.Now())
	text := ansi.Strip(strings.Join(lines, "\n"))
	for _, want := range []string{
		"acropolis · enter returns here",
		"Sharing on · reachable at https://acropolis.tail.ts.net:7979",
		"Paired devices: laptop (connected now)",
		"● studio  paired · connected · v0.11.3 · 3 of 5 running",
		"mini    not paired · v0.10.0",
		"Connected to studio",
		"3000  web",
		"forwarded to localhost:3000",
		"5432  db",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the overlay lacks %q:\n%s", want, text)
		}
	}
	// The cursor on mini offers pairing, on a port its forwarding.
	c.cursor = 2
	if h := c.hints(testEnv(ui.Session{}), "enter"); h[0] != [2]string{"enter", "pair"} {
		t.Errorf("hints on an unpaired computer %v", h)
	}
	c.cursor = 4
	if h := c.hints(testEnv(ui.Session{}), "enter"); h[0] != [2]string{"f", "forwarding on/off"} {
		t.Errorf("hints on a port %v", h)
	}
	if got := c.names(); !slices.Equal(got, []string{"local", "studio", "mini"}) {
		t.Errorf(":on completes %v", got)
	}
}

func TestComputersKeysAskBeforeSharingAndMaskTheCode(t *testing.T) {
	c := fixedComputers(t)
	c.ov.Share.Enabled = false
	env := testEnv(ui.Session{})
	local := client.New(1)
	cmd, _ := c.key(local, env, tea.KeyPressMsg{Code: 's', Text: "s"})
	if m, ok := cmd().(ui.ConfirmMsg); !ok || !strings.Contains(m.Body, "reachable on your tailnet") {
		t.Fatalf("s with sharing off: %T %+v", cmd(), cmd())
	}
	c.cursor = 2 // mini, not paired: enter asks for its code
	c.key(local, env, tea.KeyPressMsg{Code: tea.KeyEnter})
	if c.pair == nil || c.pairPeer.DNSName != "mini.tail.ts.net" {
		t.Fatalf("enter on an unpaired computer: prompt %+v for %+v", c.pair, c.pairPeer)
	}
	for _, r := range "12 34-56" {
		c.key(local, env, tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	lines, _ := c.lines(env, 90, time.Now())
	text := ansi.Strip(strings.Join(lines, "\n"))
	if strings.Contains(text, "1234") || !strings.Contains(text, "•••••••") || c.pair.value() != "1234-56" {
		t.Errorf("the code shows as %q, value %q", text[strings.Index(text, "Pairing code"):], c.pair.value())
	}
	c.key(local, env, tea.KeyPressMsg{Code: tea.KeyEscape})
	if c.pair != nil {
		t.Error("esc keeps the prompt")
	}
}

func TestTagDropsNothingOfBubbleTeaAndTagsBatches(t *testing.T) {
	type reply struct{}
	cmd := Tag(3, tea.Batch(func() tea.Msg { return reply{} }, func() tea.Msg { return reply{} }))
	batch, ok := cmd().(tea.BatchMsg)
	if !ok || len(batch) != 2 {
		t.Fatalf("a batch became %T", cmd())
	}
	if g, ok := batch[0]().(ui.GenMsg); !ok || g.Gen != 3 {
		t.Errorf("a batch's command gives %+v", batch[0]())
	}
	if _, ok := Tag(3, tea.Quit)().(tea.QuitMsg); !ok {
		t.Error("quit was tagged")
	}
}

func TestAgentsChecksWhatApplyingFixes(t *testing.T) {
	a := &agents{seq: 1}
	a.update(agentsMsg{seq: 1, items: []agentsetup.Item{
		{ID: "claude-mcp", Label: "Claude Code MCP server", State: agentsetup.OK, Applicable: true},
		{ID: "codex-mcp", Label: "Codex MCP server", State: agentsetup.Missing, Applicable: true},
		{ID: "skill", Label: "Skill for Claude Code and Codex", State: agentsetup.Outdated, Applicable: true},
		{ID: "skill-foreign", Label: "Skill", State: agentsetup.Foreign, Applicable: true, Detail: "a file exists"},
	}})
	if got := a.ids(); !slices.Equal(got, []string{"codex-mcp", "skill"}) {
		t.Fatalf("checked %v", got)
	}
	a.key(nil, tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}) // the cursor is on the first
	if got := a.ids(); !slices.Equal(got, []string{"claude-mcp", "codex-mcp", "skill"}) {
		t.Fatalf("after space %v", got)
	}
	cmd, _ := a.update(appliedMsg{seq: 1, results: []agentsetup.Result{
		{ID: "codex-mcp", Label: "Codex MCP server", OK: true, Changed: true},
		{ID: "skill", Label: "Skill for Claude Code and Codex", Error: "permission denied", Command: "portside skill install --force"},
	}})
	n, _ := cmd().(ui.NoticeMsg)
	if n.Level != ui.LevelError || !strings.Contains(n.Text, "Skill for Claude Code and Codex failed") {
		t.Errorf("the notice after applying %+v", n)
	}
	lines, _ := a.lines(testEnv(ui.Session{}))
	text := ansi.Strip(strings.Join(lines, "\n"))
	for _, want := range []string{"ok     Codex MCP server: connected", "failed Skill for Claude Code and Codex: permission denied", "run it yourself: portside skill install --force"} {
		if !strings.Contains(text, want) {
			t.Errorf("the results lack %q:\n%s", want, text)
		}
	}
}

// TestSessionFollowsForwardingChanges: on a session on studio, turning
// forwarding of 3000 off in the Computers overlay stops the session from
// resolving it, and a disconnect leaves it with no addresses at all.
func TestSessionFollowsForwardingChanges(t *testing.T) {
	c := fixedComputers(t)
	env := testEnv(ui.Session{Computer: "studio"})
	c.update(nil, env, overviewMsg{seq: 1, ov: *c.ov})
	if got := env.Session.PortURL("web", 3000, 3000); got != "http://localhost:3000" {
		t.Fatalf("forwarded port resolves to %q", got)
	}
	info := *c.ov.Connection
	info.Mirrors = slices.Clone(info.Mirrors)
	info.Mirrors[0].State = "off"
	c.update(nil, env, doneMsg{seq: 1, what: "forwarding of 3000", info: &info})
	if got := env.Session.PortURL("", 0, 3000); got != "" {
		t.Errorf("port 3000 with forwarding off still resolves to %q", got)
	}
	if got := env.Session.URL("web", "http://localhost:3000/"); !strings.Contains(got, "web.studio.localhost:7878") {
		t.Errorf("the server's own port goes through the proxy, got %q", got)
	}
	c.update(nil, env, doneMsg{seq: 1, what: "disconnect"})
	if env.Session.Conn != nil || env.Session.URL("web", "http://localhost:3000/") != "" {
		t.Errorf("a disconnected session still resolves addresses: %+v", env.Session.Conn)
	}
}

// TestConnectionLoopRefreshesTheSession: the loop's reply replaces the
// session's connection, and a reply of an earlier loop is dropped.
func TestConnectionLoopRefreshesTheSession(t *testing.T) {
	var ov server.RemoteOverview
	if err := json.Unmarshal([]byte(overviewJSON), &ov); err != nil {
		t.Fatal(err)
	}
	env := testEnv(ui.Session{Computer: "studio", Conn: &client.Connection{}})
	h := &Host{}
	h.WatchConnection(env)
	if cmd, ok := h.Update(env, connMsg{seq: h.connSeq - 1, ov: ov}); !ok || cmd != nil || env.Session.Conn.LocalPort != 0 {
		t.Fatal("a reply of an earlier loop was taken")
	}
	cmd, ok := h.Update(env, connMsg{seq: h.connSeq, ov: ov})
	if !ok || cmd == nil {
		t.Fatal("the loop does not read again")
	}
	if env.Session.Conn == nil || env.Session.Conn.LocalPort != 7878 || len(env.Session.Conn.Mirrors) != 2 {
		t.Errorf("session connection = %+v", env.Session.Conn)
	}
	ov.Connection = nil
	h.Update(env, connMsg{seq: h.connSeq, ov: ov})
	if env.Session.Conn != nil {
		t.Error("a session whose connection ended keeps resolving addresses")
	}
}

// TestExecResultCarriesItsSession: a suspended session's result is
// tagged with the session that started it, so the root drops it after
// :on switched; a session ended by the program's end quits.
func TestExecResultCarriesItsSession(t *testing.T) {
	done := execDone(3, func(err error) tea.Msg { return execDoneMsg{what: "doctor", err: err} })
	g, ok := done(nil).(ui.GenMsg)
	if !ok || g.Gen != 3 {
		t.Fatalf("result = %#v, want a GenMsg of session 3", done(nil))
	}
	if _, ok := g.Msg.(execDoneMsg); !ok {
		t.Errorf("tagged message = %#v", g.Msg)
	}
	if _, ok := done(ui.ErrEnding).(tea.QuitMsg); !ok {
		t.Error("a session ended by the program's end does not quit")
	}
}

// TestRestartFallbackIsDisclosedAndCleared: the restart confirmation says
// that an old daemon is stopped and started, which restarts its servers,
// and a failed fallback ends the reconnect state.
func TestRestartFallbackIsDisclosedAndCleared(t *testing.T) {
	env := testEnv(ui.Session{})
	env.State, env.Connected = &server.State{}, true
	env.State.Daemon.Version = app.Version
	h := &Host{}
	cm, ok := h.RestartDaemon(env)().(ui.ConfirmMsg)
	if !ok {
		t.Fatal("restart-daemon does not ask first")
	}
	if !strings.Contains(cm.Body, "in place") || !strings.Contains(cm.Body, "stopped and started instead, which restarts its servers") {
		t.Errorf("confirmation = %q", cm.Body)
	}
	h.begin(cm.Yes().(beginMsg))
	seq := h.restart.seq
	if cmd, _ := h.Update(env, sentMsg{seq: seq, old: true}); cmd == nil || h.Restarting() == "" {
		t.Fatal("an old daemon gets no fallback")
	}
	h.Update(env, execDoneMsg{what: "restart-daemon", err: errors.New("exit status 1"), seq: seq})
	if h.Restarting() != "" {
		t.Error("the reconnect banner stays after the fallback failed")
	}
}

// clickOn clicks the first cell of text in the host's overlay drawn at
// w x hgt.
func clickOn(t *testing.T, h *Host, env *ui.Env, text string) tea.Cmd {
	t.Helper()
	const w, hgt = 120, 40
	blank := strings.TrimSuffix(strings.Repeat(strings.Repeat(" ", w)+"\n", hgt), "\n")
	for y, row := range strings.Split(ansi.Strip(h.View(env, blank, w, hgt)), "\n") {
		if i := strings.Index(row, text); i >= 0 {
			return h.Mouse(tea.MouseClickMsg(tea.Mouse{X: ansi.StringWidth(row[:i]), Y: y, Button: tea.MouseLeft}))
		}
	}
	t.Fatalf("%q is not in the overlay", text)
	return nil
}

// TestHostOverlaysTakeClicks: a click on a row selects it, on a port's
// forwarding turns it, on the sharing line or an action label runs it,
// and on an agent's check box checks it.
func TestHostOverlaysTakeClicks(t *testing.T) {
	env := testEnv(ui.Session{})
	h := &Host{open: viewComputers, comp: *fixedComputers(t)}
	clickOn(t, h, env, "mini")
	if r, _ := h.comp.current(); r.kind != rowPeer || r.peer.Name != "mini" {
		t.Fatalf("a click on mini selected %+v", r)
	}
	if cmd := clickOn(t, h, env, "off"); cmd == nil || h.comp.busy != "forwarding of port 5432 on" {
		t.Errorf("a click on the forwarding state: busy %q", h.comp.busy)
	}
	if r, _ := h.comp.current(); r.kind != rowMirror || r.mirror.Port != 5432 {
		t.Errorf("the click did not select port 5432: %+v", r)
	}
	h.comp.busy = ""
	if cmd := clickOn(t, h, env, "Sharing on"); cmd == nil || h.comp.busy != "sharing off" {
		t.Errorf("a click on the sharing line: busy %q", h.comp.busy)
	}
	h.comp.busy = ""
	if cmd := clickOn(t, h, env, "disconnect"); cmd == nil || h.comp.busy != "disconnect" {
		t.Errorf("a click on the disconnect label: busy %q", h.comp.busy)
	}

	h = &Host{open: viewAgents}
	h.agents.setItems([]agentsetup.Item{{ID: "mcp", Label: "Claude Code MCP server", State: agentsetup.Missing},
		{ID: "skill", Label: "Codex skill", State: agentsetup.OK}})
	clickOn(t, h, env, "Codex skill")
	if !h.agents.chosen["skill"] || h.agents.cursor != 1 {
		t.Errorf("a click on the check box: chosen %v, cursor %d", h.agents.chosen, h.agents.cursor)
	}
	if cmd := clickOn(t, h, env, "set up the checked"); cmd == nil || !h.agents.busy {
		t.Error("a click on the action label does not set up")
	}
}
