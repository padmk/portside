package remote

import (
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/padmk/portside/internal/app"
	"github.com/padmk/portside/internal/client"
	"github.com/padmk/portside/internal/tui/overlay"
	"github.com/padmk/portside/internal/tui/ui"
)

// view is which overlay of the host is open.
type view int

const (
	viewNone view = iota
	viewComputers
	viewDaemon
	viewAgents
)

// Host owns the overlays of this package and the restart in progress.
// Local is the client of this computer's daemon: the computers, the
// daemon's upgrade and the agents always go there, whatever computer the
// session targets.
type Host struct {
	Local *client.Client

	open    view
	comp    computers
	agents  agents
	restart *restarting
	seq     int
	scroll  int
	// cursorAt is the highlighted line of the last view, -1 for none.
	cursorAt int
	// connSeq numbers the connection loops (WatchConnection); a message
	// of an earlier one is dropped.
	connSeq int
	// From the last frame, for clicks: the environment it was drawn with
	// (the root's), where the overlay is, and the daemon overlay's
	// clickable lines; lastRow and lastClick tell a double click.
	env          *ui.Env
	geo          geometry
	daemonClicks clicks
	lastRow      int
	lastClick    time.Time
}

// Open reports whether an overlay of the host takes the keys.
func (h *Host) Open() bool { return h.open != viewNone }

// Close closes the open overlay.
func (h *Host) Close() {
	h.open = viewNone
	h.comp.pair = nil
}

// Computers opens the Computers overlay and scans the tailnet.
func (h *Host) Computers() tea.Cmd {
	h.open, h.scroll = viewComputers, 0
	return h.comp.open(h.Local)
}

// AskPair opens the Computers overlay on the code prompt for p, which
// answered that it needs pairing.
func (h *Host) AskPair(p Peer) tea.Cmd {
	cmd := h.Computers()
	h.comp.askPair(p)
	return cmd
}

// Daemon opens the daemon's details.
func (h *Host) Daemon() { h.open, h.scroll = viewDaemon, 0 }

// Setup opens the coding-agent setup, or says why it is off.
func (h *Host) Setup(env *ui.Env) tea.Cmd {
	if why := SetupOff(env); why != "" {
		return notice(ui.LevelWarn, "setup: "+why)
	}
	h.open, h.scroll = viewAgents, 0
	return h.agents.open(h.Local)
}

// Doctor suspends the terminal UI for `doctor`, or says why it is off.
func (h *Host) Doctor(env *ui.Env) tea.Cmd {
	if why := SetupOff(env); why != "" {
		return notice(ui.LevelWarn, "doctor: "+why)
	}
	return runCLI(env, 0, "doctor", "doctor")
}

// Upgrade asks before replacing the daemon with this binary in place.
func (h *Host) Upgrade(env *ui.Env) tea.Cmd {
	if why := UpgradeOff(env); why != "" {
		return notice(ui.LevelWarn, "upgrade: "+why)
	}
	return h.confirmRestart(env, VerbUpgrade)
}

// RestartDaemon asks before restarting the daemon in place.
func (h *Host) RestartDaemon(env *ui.Env) tea.Cmd {
	if why := RestartOff(env); why != "" {
		return notice(ui.LevelWarn, "restart-daemon: "+why)
	}
	return h.confirmRestart(env, VerbRestart)
}

// Names are the computers `:on` completes.
func (h *Host) Names() []string { return h.comp.names() }

// Update takes the host's messages; ok is false for the others.
func (h *Host) Update(env *ui.Env, msg tea.Msg) (tea.Cmd, bool) {
	switch msg := msg.(type) {
	case beginMsg:
		return h.begin(msg), true
	case sentMsg:
		r := h.restart
		if r == nil || r.seq != msg.seq {
			return nil, true
		}
		switch {
		case msg.err != nil:
			h.restart = nil
			return notice(ui.LevelError, r.verb+": the daemon keeps running "+versionLabel(r.version)+": "+msg.err.Error()), true
		case msg.old && r.verb == VerbUpgrade:
			h.restart = nil
			return notice(ui.LevelError, "upgrade: the daemon ("+versionLabel(r.version)+") cannot be upgraded in place; :restart-daemon replaces it and restarts its servers"), true
		case msg.old:
			// A daemon from before upgrades in place: the command stops and
			// starts it, through the login service when that runs it, which
			// restarts its servers (the confirmation said so).
			return runCLI(env, r.seq, "restart-daemon", "daemon", "restart"), true
		}
		return nil, true
	case restartTimeoutMsg:
		if r := h.restart; r != nil && r.seq == msg.seq {
			h.restart = nil
			return notice(ui.LevelError, r.verb+": the daemon did not answer again within a minute; `"+app.ID+" daemon logs` shows why"), true
		}
		return nil, true
	case execDoneMsg:
		if msg.err != nil {
			// A failed fallback leaves the daemon as it was: no reconnect
			// to wait for.
			if r := h.restart; r != nil && msg.seq != 0 && r.seq == msg.seq {
				h.restart = nil
			}
			return notice(ui.LevelError, msg.what+": "+msg.err.Error()), true
		}
		return notice(ui.LevelInfo, msg.what+": done"), true
	case tea.PasteMsg:
		if h.open == viewComputers && h.comp.pair != nil {
			h.comp.pair.insert(msg.Content)
			return nil, true
		}
		return nil, false
	}
	if cmd, ok := h.updateConn(env, msg); ok {
		return cmd, true
	}
	if cmd, ok := h.comp.update(h.Local, env, msg); ok {
		return cmd, true
	}
	return h.agents.update(msg)
}

// Key handles a key while an overlay is open.
func (h *Host) Key(env *ui.Env, msg tea.KeyPressMsg) tea.Cmd {
	var cmd tea.Cmd
	closed := false
	switch h.open {
	case viewComputers:
		cmd, closed = h.comp.key(h.Local, env, msg)
	case viewAgents:
		cmd, closed = h.agents.key(h.Local, msg)
	case viewDaemon:
		switch msg.String() {
		case "esc", "q", "enter":
			closed = true
		case "u":
			cmd = h.Upgrade(env)
		case "r":
			cmd = h.RestartDaemon(env)
		case "j", "down":
			h.scroll++
		case "k", "up":
			h.scroll = max(0, h.scroll-1)
		}
	case viewNone:
	}
	if closed {
		h.Close()
	}
	return cmd
}

// View draws the open overlay centered over the screen (the root dims
// it).
func (h *Host) View(env *ui.Env, screen string, w, hgt int) string {
	inner := max(20, min(96, w-8))
	title, lines, cursor := "", []string(nil), -1
	switch h.open {
	case viewComputers:
		title = "Computers"
		lines, cursor = h.comp.lines(env, inner, env.Now())
	case viewAgents:
		title = "Coding agents"
		lines, cursor = h.agents.lines(env, inner)
	case viewDaemon:
		title = "Daemon"
		lines = h.daemonView(env, inner)
	default:
		return screen
	}
	rows := max(1, hgt-4)
	lines = h.window(lines, cursor, rows)
	h.placed(env, w, hgt, inner, h.scroll, len(lines))
	if h.cursorAt >= 0 && h.cursorAt < len(lines) {
		lines[h.cursorAt] = overlay.Highlight(env.Styles, lines[h.cursorAt], inner)
	}
	box := overlay.Frame(env.Styles, env.Glyphs, title, lines, inner)
	return overlay.Center(screen, box)
}

// window keeps the cursor's line, or the scroll offset, inside rows lines
// and records where the cursor's line landed.
func (h *Host) window(lines []string, cursor, rows int) []string {
	h.cursorAt = -1
	if len(lines) <= rows {
		h.scroll = 0
		h.cursorAt = cursor
		return lines
	}
	off := min(h.scroll, len(lines)-rows)
	if cursor >= 0 {
		off = max(0, min(cursor-rows/2, len(lines)-rows))
	}
	h.scroll = off
	if cursor >= 0 {
		h.cursorAt = cursor - off
	}
	return lines[off : off+rows]
}

// daemonView is the daemon's details as label and value rows, long
// values wrapped under their value column.
func (h *Host) daemonView(env *ui.Env, inner int) []string {
	s := overlay.Styles(env.Styles)
	const labelW = 11
	var out []string
	cl := &h.daemonClicks
	*cl = (*cl)[:0]
	add := func(l string) {
		out = append(out, l)
		cl.line(-1)
	}
	for _, kv := range daemonLines(env, env.Now()) {
		for i, part := range wrap(kv[1], inner-labelW) {
			label := ""
			if i == 0 {
				label = kv[0]
			}
			add(s.Muted.Render(pad(label, labelW, "")) + s.Text.Render(part))
		}
	}
	if r := h.Restarting(); r != "" {
		add("")
		add(s.Warn.Render(r + env.Glyphs.Ellipsis))
	}
	if !env.Session.Remote() {
		add("")
		for _, l := range wrap("u replaces the daemon with this binary when it is newer; r restarts it in place. Both keep servers running; a daemon too old to restart in place is stopped and started, which restarts its servers.", inner) {
			add(s.Text2.Render(l))
		}
	}
	add("")
	return append(out, actionLines(env.Styles, env.Glyphs, cl, inner, h.daemonHints(env))...)
}

// daemonHints are the Daemon overlay's keys: upgrade and restart on this
// computer, and the way out.
func (h *Host) daemonHints(env *ui.Env) [][2]string {
	if env.Session.Remote() {
		return [][2]string{{"esc", "close"}}
	}
	return [][2]string{{"u", "upgrade"}, {"r", "restart"}, {"esc", "close"}}
}

// wrap breaks text into lines of at most w cells, at spaces and slashes
// where it can.
func wrap(text string, w int) []string {
	if w <= 0 {
		return []string{text}
	}
	return strings.Split(ansi.Wrap(text, w, "/"), "\n")
}
