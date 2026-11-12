package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/coder/websocket"

	"github.com/padmk/portside/internal/app"
	"github.com/padmk/portside/internal/client"
	"github.com/padmk/portside/internal/config"
	"github.com/padmk/portside/internal/docker"
	"github.com/padmk/portside/internal/ports"
	"github.com/padmk/portside/internal/server"
	"github.com/padmk/portside/internal/signing"
	"github.com/padmk/portside/internal/supervisor"
	"github.com/padmk/portside/internal/tui/keymap"
	"github.com/padmk/portside/internal/tui/live"
	"github.com/padmk/portside/internal/tui/theme"
	"github.com/padmk/portside/internal/tui/ui"
)

// The quality bar of design/tui.md: every frame at the three sizes, in
// every theme family and variant, every glyph set and without colors, is
// exactly the screen's size, its panes' borders line up, it carries no
// control sequence but SGR and OSC 8, and the ASCII set draws ASCII only.

var snapshotSizes = [][2]int{{80, 24}, {120, 40}, {200, 60}}

// Names that measure differently from their length: CJK (two cells per
// rune), an emoji, combining marks and a name longer than any column.
const (
	cjkName   = "数据同步服务"
	emojiName = "🚀launcher"
	longName  = "a-very-long-server-name-that-keeps-going-past-every-column"
)

// snapshotLogs is the selected server's scrollback: colors, a hyperlink,
// wide characters, a tab, a carriage-return progress line and a line
// longer than the widest screen.
var snapshotLogs = strings.Join([]string{
	"\x1b[32mINFO\x1b[0m listening on http://127.0.0.1:3101",
	"\x1b]8;;http://web.localhost\x1b\\web.localhost\x1b]8;;\x1b\\ ready",
	"WARN 数据同步服务 慢查询 🚀 took 1.2s\tretrying",
	"progress 10%\rprogress 100%",
	"\x1b[1;31mERROR\x1b[0m " + strings.Repeat("long-line-", 40),
	"café naïve e\u0301 combining",
	"",
}, "\r\n")

func snapshotState() *server.State {
	at := time.Date(2026, 11, 12, 11, 0, 0, 0, time.UTC)
	exit1, exit0, exit137 := 1, 0, 137
	next := at.Add(15 * time.Hour)
	history := make([]float64, 60)
	for i := range history {
		history[i] = float64((i * 37) % 100)
	}
	mem := make([]int64, 60)
	for i := range mem {
		mem[i] = int64(100+i) << 20
	}
	return &server.State{
		Config: server.ConfigInfo{Path: "/x/portside.toml", Revision: "r1"},
		Groups: []string{"shop", "数据"},
		Servers: []supervisor.View{
			{Name: "web", Group: "shop", Enabled: true, Status: supervisor.Running, Port: 3101, URL: "http://web.localhost:7878", Kind: "vite",
				PID: 101, StartedAt: &at, ReadyAt: &at, Listening: true, ListenPorts: []int{3101}, CPU: 12.5, Mem: 160 << 20, CPUHistory: history, MemHistory: mem},
			{Name: "api", Group: "shop", Enabled: true, Status: supervisor.Running, Unready: true, Port: 3102, PID: 102, StartedAt: &at, CPUHistory: history[:10]},
			{Name: "worker", Group: "shop", Enabled: true, Status: supervisor.Crashed, ExitCode: &exit1, ExitedAt: &at, Restarts: 3,
				Tail: []string{"starting worker", "ERROR boom"}},
			{Name: "mailer", Group: "shop", Status: supervisor.Stopped},
			{Name: cjkName, Group: "数据", Enabled: true, Status: supervisor.Running, Port: 4100, PID: 103, StartedAt: &at, CPUHistory: history},
			{Name: emojiName, Group: "数据", Enabled: true, Status: supervisor.Running, PID: 104, StartedAt: &at, Stale: true},
			{Name: longName, Enabled: true, Status: supervisor.Stopped, Port: 4321},
			{Name: "backup", Enabled: true, Oneshot: true, Done: true, Status: supervisor.Exited, ExitCode: &exit0, ExitedAt: &at},
			{Name: "nightly", Enabled: true, Oneshot: true, Schedule: "0 3 * * *", NextRun: &next, Status: supervisor.Stopped},
			{Name: "café-é", Enabled: true, Status: supervisor.Starting, PID: 105},
		},
		Docker: docker.State{Status: docker.Ready, Engine: "docker", Version: "27.0", Containers: []docker.Container{
			{ID: "a1", Name: "shop-db-1", Image: "postgres:16", Project: "shop", Service: "db", State: "running", Health: "healthy",
				Status: "Up 3 hours (healthy)", StartedAt: &at, Ports: []docker.Port{{Private: 5432, Public: 5432, Proto: "tcp", IP: "127.0.0.1"}},
				CPU: 3.2, Mem: 80 << 20, MemLimit: 2 << 30, CPUHistory: history},
			{ID: "a2", Name: "shop-redis-1", Image: "redis:7", Project: "shop", Service: "redis", State: "exited", ExitCode: &exit137, Failed: true, Status: "Exited (137) 2 minutes ago"},
			{ID: "a3", Name: "缓存-1", Image: "memcached:1.6-alpine-with-a-long-tag", State: "running", Status: "Up 2 days"},
		}},
	}
}

func snapshotListeners() []server.ListenerView {
	return []server.ListenerView{
		{Listener: ports.Listener{Port: 3101, PID: 101, Command: "node vite", Cwd: "/x/shop/web", Addresses: []string{"127.0.0.1"}}, Server: "web", Managed: true},
		{Listener: ports.Listener{Port: 4100, PID: 103, Command: "python3 -m http.server", Cwd: "/x/数据/同步", Addresses: []string{"*"}}, Server: cjkName, Managed: true},
		{Listener: ports.Listener{Port: 5432, PID: 900, Command: "com.docker.backend", Addresses: []string{"127.0.0.1"}}, Via: "containers", Container: &server.ContainerRef{Name: "shop-db-1", Service: "db"}},
		{Listener: ports.Listener{Port: 8080, PID: 901, Command: strings.Repeat("java -jar something-long ", 6), Addresses: []string{"[::1]"}}},
		{Listener: ports.Listener{Port: 22, PID: 1, Command: "sshd"}, Protected: "a system process"},
	}
}

// snapshotDaemon answers the requests the frames make (the ports, the
// edit form's spec, the Computers overlay, the logs) with proven replies,
// and streams snapshotLogs to every terminal WebSocket until done closes.
func snapshotDaemon(t *testing.T, done <-chan struct{}) int {
	t.Helper()
	state := t.TempDir()
	t.Setenv(app.Env("STATE_DIR"), state)
	t.Setenv(app.Env("CONFIG"), filepath.Join(t.TempDir(), "none.toml"))
	token := signing.RandomHex(32)
	if err := os.WriteFile(filepath.Join(state, "auth-token"), []byte(token+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	epoch := signing.NewEpoch()
	port := 4100
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reply := func(status int, v any) {
			var body []byte
			if s, ok := v.(string); ok {
				body = []byte(s)
			} else {
				body, _ = json.Marshal(v)
			}
			signing.ProveReply(w.Header(), r.Header, token, epoch, status, body)
			w.WriteHeader(status)
			_, _ = w.Write(body)
		}
		p := r.URL.Path
		switch {
		case p == "/api/health":
			reply(http.StatusOK, client.Health{App: app.ID, Version: "test", PID: 1})
		case p == "/api/listeners":
			reply(http.StatusOK, map[string]any{"listeners": snapshotListeners()})
		case strings.HasSuffix(p, "/spec"):
			reply(http.StatusOK, config.Spec{Name: "web", Group: "shop", Dir: "shop/web", Cmd: "pnpm dev --port {PORT}", Port: &port,
				Env: map[string]string{"NODE_ENV": "development", "LONG": strings.Repeat("x", 120)}})
		case p == "/api/remote":
			reply(http.StatusOK, server.RemoteOverview{})
		case p == "/api/remote/peers":
			reply(http.StatusOK, map[string]any{"prompt": ""})
		case strings.HasSuffix(p, "/terminal"):
			h := http.Header{}
			signing.Prove(h, r.Header, token, epoch)
			for k, v := range h {
				w.Header()[k] = v
			}
			ws, err := websocket.Accept(w, r, nil)
			if err != nil {
				return
			}
			defer ws.CloseNow()
			_ = ws.Write(r.Context(), websocket.MessageBinary, []byte(snapshotLogs))
			<-done
		case strings.HasSuffix(p, "/logs"):
			reply(http.StatusOK, "")
		default:
			reply(http.StatusNotFound, map[string]string{"error": "no such endpoint"})
		}
	}))
	t.Cleanup(srv.Close)
	_, p, _ := net.SplitHostPort(srv.Listener.Addr().String())
	n, _ := strconv.Atoi(p)
	return n
}

// harness runs a model's commands as the program does, concurrently, and
// feeds their messages back on the test's goroutine.
type harness struct {
	t    *testing.T
	m    *model
	msgs chan tea.Msg
}

func (h *harness) run(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	go func() {
		msg := cmd()
		select {
		case h.msgs <- msg:
		default: // a full queue drops late ticks; settle has what it needs
		}
	}()
}

// settle handles messages until ok holds; it fails after a few seconds.
func (h *harness) settle(what string, ok func() bool) {
	h.t.Helper()
	deadline := time.After(5 * time.Second)
	for !ok() {
		select {
		case msg := <-h.msgs:
			h.handle(msg)
		case <-deadline:
			h.t.Fatalf("waiting for %s:\n%s", what, ansi.Strip(h.m.screen()))
		}
	}
}

func (h *harness) handle(msg tea.Msg) {
	switch msg := msg.(type) {
	case nil:
		return
	case tea.BatchMsg:
		for _, c := range msg {
			h.run(c)
		}
		return
	}
	// tea.Sequence's message is an unexported slice of commands: run them
	// in order.
	if v := reflect.ValueOf(msg); v.Kind() == reflect.Slice && v.Type().Elem() == reflect.TypeOf(tea.Cmd(nil)) {
		cmds := make([]tea.Cmd, v.Len())
		for i := range cmds {
			cmds[i] = v.Index(i).Interface().(tea.Cmd)
		}
		h.run(func() tea.Msg {
			for _, c := range cmds {
				if c != nil {
					h.msgs <- c()
				}
			}
			return nil
		})
		return
	}
	_, cmd := h.m.Update(msg)
	h.run(cmd)
}

func (h *harness) send(msg tea.Msg) { h.handle(msg) }

func (h *harness) key(k string) { h.send(keyMsg(h.t, k)) }

// snapshotModel is the root model on the fake daemon with the fixture
// state, the selected server's logs loaded and the ports pane open behind
// the focused servers list.
func snapshotModel(t *testing.T) *harness {
	t.Helper()
	done := make(chan struct{})
	port := snapshotDaemon(t, done)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() { cancel(); close(done) })
	m := newModel(client.New(port), live.New(), ui.Session{}, prefs{}, theme.TerminalFamily, "", theme.Unicode)
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	m.env.Now = func() time.Time { return now }
	m.env.Context = ctx
	h := &harness{t: t, m: m, msgs: make(chan tea.Msg, 4096)}
	h.send(tea.WindowSizeMsg{Width: 120, Height: 40})
	h.send(ui.ConnMsg{Connected: true})
	h.send(ui.StateMsg{State: snapshotState()})
	h.settle("the logs", func() bool { return strings.Contains(ansi.Strip(m.screen()), "listening on") })
	h.run(m.togglePorts())
	h.settle("the ports", func() bool { return strings.Contains(ansi.Strip(m.screen()), "com.docker") })
	h.run(m.focusScope(keymap.ScopeServers))
	return h
}

// style switches the model to a theme family's variant, a glyph set and
// colors or none.
func (m *model) style(f theme.Family, dark bool, g theme.Glyphs, mono bool) {
	m.family, m.variant, m.env.Glyphs, m.mono = f, map[bool]string{true: "dark", false: "light"}[dark], g, mono
	m.restyle()
}

func TestSnapshotMatrixStaysAligned(t *testing.T) {
	h := snapshotModel(t)
	m := h.m
	type look struct {
		name string
		f    theme.Family
		dark bool
		mono bool
	}
	var looks []look
	for _, f := range theme.Families() {
		looks = append(looks, look{f.ID + "-dark", f, true, false}, look{f.ID + "-light", f, false, false})
	}
	looks = append(looks, look{"mono", theme.TerminalFamily, true, true})
	frames := 0
	for _, size := range snapshotSizes {
		h.send(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		for _, l := range looks {
			for _, g := range theme.GlyphSets() {
				m.style(l.f, l.dark, g, l.mono)
				name := fmt.Sprintf("%dx%d %s %s", size[0], size[1], l.name, g.ID)
				checkFrame(t, name, m.screen(), size[0], size[1], g)
				checkPaneBorders(t, name, m.screen(), m.rects, g)
				frames++
			}
		}
	}
	t.Logf("%d frames", frames)
}

// TestSnapshotOverlaysStayAligned opens each overlay once per size, in
// the Terminal theme and in a theme that paints the screen's background.
func TestSnapshotOverlaysStayAligned(t *testing.T) {
	h := snapshotModel(t)
	m := h.m
	confirm := ui.ConfirmMsg{Title: "Delete server", Body: "Remove " + cjkName + " from the config? Its logs stay until the daemon restarts.", Confirm: "Delete", Yes: func() tea.Msg { return nil }}
	overlays := []struct {
		name  string
		open  func()
		shown func() bool
	}{
		{"containers focused", func() { h.run(m.focusScope(keymap.ScopeContainers)) }, func() bool { return m.focused().Scope() == keymap.ScopeContainers }},
		{"ports focused", func() { h.run(m.focusScope(keymap.ScopePorts)) }, func() bool { return m.focused().Scope() == keymap.ScopePorts }},
		{"help", func() { h.key("?") }, func() bool { return m.help != nil }},
		{"menu", func() { h.key("x") }, func() bool { return m.menu != nil }},
		{"palette", func() { h.key("ctrl+p") }, func() bool { return m.palette != nil }},
		{"confirmation", func() { h.send(confirm) }, func() bool { return m.confirm != nil }},
		{"edit form", func() { h.run(m.edit.EditServer(&m.env, "web")) }, func() bool {
			return m.edit.Open() && strings.Contains(ansi.Strip(m.screen()), "pnpm dev")
		}},
		{"computers", func() { h.run(m.sys.Computers()) }, func() bool { return m.sys.Open() }},
	}
	painted := theme.Families()[1]
	for _, f := range []theme.Family{theme.TerminalFamily, painted} {
		m.style(f, true, theme.Unicode, false)
		for _, size := range snapshotSizes {
			h.send(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
			for _, o := range overlays {
				name := fmt.Sprintf("%dx%d %s %s", size[0], size[1], f.ID, o.name)
				o.open()
				h.settle(name, o.shown)
				frame := m.screen()
				// The confirmation is composed on a canvas, which leaves
				// out a row's trailing blanks that carry no style: the
				// terminal's own background, which the renderer clears to
				// anyway. A theme that paints the screen keeps every cell.
				trimmed := m.confirm != nil && f.ID == theme.TerminalFamily.ID
				checkFrameRows(t, name, frame, size[0], size[1], theme.Unicode, trimmed)
				if !(m.help != nil || m.menu != nil || m.palette != nil || m.confirm != nil || m.edit.Open() || m.sys.Open()) {
					checkPaneBorders(t, name, frame, m.rects, theme.Unicode)
				}
				// Close it the way the user does; the focus returns to
				// the servers.
				for range 3 {
					if overlayOpen(m) {
						h.key("esc")
					}
				}
				if overlayOpen(m) {
					t.Fatalf("%s: esc does not close it", name)
				}
				h.run(m.focusScope(keymap.ScopeServers))
			}
		}
	}
}

func overlayOpen(m *model) bool {
	return m.help != nil || m.menu != nil || m.palette != nil || m.messages != nil || m.confirm != nil || m.edit.Open() || m.sys.Open()
}

// checkFrame checks a frame's size, its escape sequences and, in the ASCII
// set, that it draws ASCII only.
func checkFrame(t *testing.T, name, frame string, w, h int, g theme.Glyphs) {
	t.Helper()
	checkFrameRows(t, name, frame, w, h, g, false)
}

// checkFrameRows is checkFrame; with trimmed, a row may end early when
// what it leaves out is blank.
func checkFrameRows(t *testing.T, name, frame string, w, h int, g theme.Glyphs, trimmed bool) {
	t.Helper()
	lines := strings.Split(frame, "\n")
	if len(lines) != h {
		t.Errorf("%s: %d rows, want %d", name, len(lines), h)
	}
	for i, l := range lines {
		if got := lipgloss.Width(l); got != w && !(trimmed && got < w) {
			t.Errorf("%s: row %d is %d cells wide, want %d: %q", name, i, got, w, ansi.Strip(l))
		}
	}
	if err := onlySGRAndHyperlinks(frame); err != nil {
		t.Errorf("%s: %v", name, err)
	}
	if g.ID == theme.ASCII.ID {
		for i, l := range lines {
			if r, ok := chromeRune(ansi.Strip(l)); ok {
				t.Errorf("%s: row %d draws %q (%U), not ASCII: %q", name, i, r, r, ansi.Strip(l))
			}
		}
	}
}

// fixtureRunes are the characters beyond ASCII that the fixture's own
// data carries (names, logs, paths): the ASCII set shows them as they are.
var fixtureRunes = func() map[rune]bool {
	data, _ := json.Marshal([]any{snapshotState(), snapshotListeners()})
	set := map[rune]bool{}
	for _, r := range string(data) + snapshotLogs {
		if r >= utf8.RuneSelf {
			set[r] = true
		}
	}
	return set
}()

// chromeRune returns the first character beyond ASCII in s that is not
// the fixture's data, so the frame drew it.
func chromeRune(s string) (rune, bool) {
	for _, r := range s {
		if r >= utf8.RuneSelf && !fixtureRunes[r] {
			return r, true
		}
	}
	return 0, false
}

// onlySGRAndHyperlinks reports the first escape sequence other than SGR
// (CSI ... m) and OSC 8, and any other control character but the newline.
func onlySGRAndHyperlinks(s string) error {
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c == '\n':
			i++
		case c == 0x1b && i+1 < len(s) && s[i+1] == '[':
			j := i + 2
			for j < len(s) && (s[j] < 0x40 || s[j] > 0x7e) {
				j++
			}
			if j >= len(s) || s[j] != 'm' {
				return fmt.Errorf("a CSI sequence that is not SGR at byte %d: %q", i, s[i:min(len(s), j+1)])
			}
			i = j + 1
		case c == 0x1b && i+1 < len(s) && s[i+1] == ']':
			j := i + 2
			for j < len(s) && s[j] != 0x07 && !(s[j] == 0x1b && j+1 < len(s) && s[j+1] == '\\') {
				j++
			}
			if !strings.HasPrefix(s[i+2:], "8;") {
				return fmt.Errorf("an OSC sequence that is not a hyperlink at byte %d: %q", i, s[i:min(len(s), j+2)])
			}
			if j < len(s) && s[j] == 0x1b {
				j++
			}
			i = j + 1
		case c < 0x20 || c == 0x7f:
			return fmt.Errorf("control character %#x at byte %d: %q", c, i, s[max(0, i-20):min(len(s), i+20)])
		default:
			r, n := utf8.DecodeRuneInString(s[i:])
			if r >= 0x80 && r <= 0x9f {
				return fmt.Errorf("C1 control %U at byte %d", r, i)
			}
			i += n
		}
	}
	return nil
}

// checkPaneBorders checks that each expanded pane's border is whole: the
// corners on its first and last rows, the sides on the rows between, in
// the glyph set's light or heavy border.
func checkPaneBorders(t *testing.T, name, frame string, rects []rect, g theme.Glyphs) {
	t.Helper()
	lines := strings.Split(ansi.Strip(frame), "\n")
	cell := func(x, y int) string {
		if y < 0 || y >= len(lines) {
			return ""
		}
		return ansi.Cut(lines[y], x, x+1)
	}
	is := func(got string, pick func(lipgloss.Border) string) bool {
		return got == pick(g.Border) || got == pick(g.Heavy)
	}
	for _, r := range rects {
		if r.compact || r.h < 2 {
			continue
		}
		right, bottom := r.x+r.w-1, r.y+r.h-1
		check := func(x, y int, what string, pick func(lipgloss.Border) string) {
			if got := cell(x, y); !is(got, pick) {
				t.Errorf("%s: pane %d %s at %d,%d is %q, want %q: row %q", name, r.pane, what, x, y, got, pick(g.Border), lines[y])
			}
		}
		check(r.x, r.y, "top left", func(b lipgloss.Border) string { return b.TopLeft })
		check(right, r.y, "top right", func(b lipgloss.Border) string { return b.TopRight })
		check(r.x, bottom, "bottom left", func(b lipgloss.Border) string { return b.BottomLeft })
		check(right, bottom, "bottom right", func(b lipgloss.Border) string { return b.BottomRight })
		for y := r.y + 1; y < bottom; y++ {
			check(r.x, y, "left side", func(b lipgloss.Border) string { return b.Left })
			check(right, y, "right side", func(b lipgloss.Border) string { return b.Right })
		}
	}
}
