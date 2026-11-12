package remote

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/padmk/portside/internal/app"
	"github.com/padmk/portside/internal/client"
	"github.com/padmk/portside/internal/remote"
	"github.com/padmk/portside/internal/server"
	"github.com/padmk/portside/internal/share"
	"github.com/padmk/portside/internal/tui/ui"
	"github.com/padmk/portside/internal/viewfmt"
)

// connectTimeout bounds connect and pair, which find the other daemon's
// port over the tailnet first.
const connectTimeout = 45 * time.Second

// rowKind is what a row of the Computers overlay is about.
type rowKind int

const (
	rowThis rowKind = iota
	rowPeer
	rowMirror
)

// row is a row the cursor can be on.
type row struct {
	kind   rowKind
	peer   remote.Peer
	mirror remote.Mirror
}

// computers is the Computers overlay: this computer and its sharing, the
// computers on the tailnet that share their daemon, and the ports of the
// connected one.
type computers struct {
	seq      int
	ov       *server.RemoteOverview
	disc     *Discovery
	scanning bool
	// err is the last failure, shown under the lists (and as a notice).
	err    string
	busy   string
	cursor int
	code   *share.Code
	// pair is the code prompt and the computer it pairs with.
	pair     *prompt
	pairPeer Peer
	// clicks are the last frame's clickable lines.
	clicks clicks
}

// rows are the rows the cursor moves over, top to bottom.
func (c *computers) rows() []row {
	out := []row{{kind: rowThis}}
	if c.disc != nil {
		for _, p := range c.disc.Peers {
			out = append(out, row{kind: rowPeer, peer: p})
		}
	}
	if cn := c.conn(); cn != nil {
		for _, m := range cn.Mirrors {
			out = append(out, row{kind: rowMirror, mirror: m})
		}
	}
	return out
}

func (c *computers) conn() *remote.Info {
	if c.ov == nil {
		return nil
	}
	return c.ov.Connection
}

// connected reports whether this daemon's connection is to p.
func (c *computers) connected(p remote.Peer) bool {
	cn := c.conn()
	if cn == nil {
		return p.Connected && c.ov == nil
	}
	return strings.EqualFold(cn.Peer.DNSName, p.DNSName)
}

func (c *computers) current() (row, bool) {
	rs := c.rows()
	if len(rs) == 0 {
		return row{}, false
	}
	c.cursor = max(0, min(c.cursor, len(rs)-1))
	return rs[c.cursor], true
}

func (c *computers) move(n int) {
	c.cursor = max(0, min(c.cursor+n, len(c.rows())-1))
}

// open (re)loads the overview and scans the tailnet.
func (c *computers) open(local *client.Client) tea.Cmd {
	c.seq++
	c.scanning = true
	c.err = ""
	return tea.Batch(fetchOverview(local, c.seq), fetchPeers(local, c.seq))
}

// update takes a reply; ok is false for messages that are not the
// overlay's.
func (c *computers) update(local *client.Client, env *ui.Env, msg tea.Msg) (tea.Cmd, bool) {
	switch msg := msg.(type) {
	case overviewMsg:
		if msg.seq != c.seq {
			return nil, true
		}
		if msg.err != nil {
			c.err = msg.err.Error()
			return nil, true
		}
		c.ov = &msg.ov
		SyncConn(env, c.conn())
	case peersMsg:
		if msg.seq != c.seq {
			return nil, true
		}
		c.scanning = false
		if msg.err != nil {
			c.err = msg.err.Error()
			return nil, true
		}
		c.disc = &msg.disc
	case doneMsg:
		if msg.seq != c.seq {
			return nil, true
		}
		c.busy = ""
		if msg.err != nil {
			c.err = msg.what + ": " + msg.err.Error()
			return notice(ui.LevelError, c.err), true
		}
		c.err = ""
		if msg.info != nil && c.ov != nil {
			c.ov.Connection = msg.info
		}
		if msg.ov != nil {
			c.ov = msg.ov
		}
		if msg.code != nil {
			c.code = msg.code
			return nil, true
		}
		var cmds []tea.Cmd
		if msg.what == "disconnect" && c.ov != nil {
			c.ov.Connection = nil
		}
		// The session's addresses follow the change at once: a port whose
		// forwarding went off, or a connection that ended, resolves no more.
		if c.ov != nil || msg.what == "disconnect" {
			SyncConn(env, c.conn())
		}
		if msg.switchTo != "" {
			cmds = append(cmds, Switch(local, msg.switchTo))
		} else {
			cmds = append(cmds, notice(ui.LevelSuccess, msg.what+": done"))
		}
		if msg.what == "disconnect" && env.Session.Remote() {
			cmds = append(cmds, Switch(local, ""))
		}
		return tea.Batch(cmds...), true
	default:
		return nil, false
	}
	return nil, true
}

// key handles a key; closed is set when the overlay closes.
func (c *computers) key(local *client.Client, env *ui.Env, msg tea.KeyPressMsg) (cmd tea.Cmd, closed bool) {
	if c.pair != nil {
		done, submit := c.pair.key(msg)
		if !done {
			return nil, false
		}
		code, peer := c.pair.value(), c.pairPeer
		c.pair = nil
		if !submit {
			return nil, false
		}
		return c.request(local, "pair "+peer.Name, http.MethodPost, "/api/remote/pair",
			map[string]string{"peer": peer.DNSName, "code": code}, new(remote.Info), peer.DNSName), false
	}
	r, _ := c.current()
	switch msg.String() {
	case "esc", "q":
		return nil, true
	case "j", "down":
		c.move(1)
	case "k", "up":
		c.move(-1)
	case "g", "home":
		c.cursor = 0
	case "G", "end":
		c.cursor = len(c.rows()) - 1
	case "R", "ctrl+r":
		return c.open(local), false
	case "enter":
		switch r.kind {
		case rowThis:
			if !env.Session.Remote() {
				return notice(ui.LevelInfo, "this session is on this computer already"), false
			}
			return Switch(local, ""), true
		case rowPeer:
			if !r.peer.Paired {
				c.askCode(r.peer)
				return nil, false
			}
			c.busy = "connecting to " + r.peer.Name
			return Switch(local, r.peer.DNSName), true
		case rowMirror:
			return c.forward(local, r.mirror), false
		}
	case "c":
		if r.kind != rowPeer {
			return notice(ui.LevelInfo, "c connects to the computer under the cursor"), false
		}
		if !r.peer.Paired {
			c.askCode(r.peer)
			return nil, false
		}
		return c.request(local, "connect "+r.peer.Name, http.MethodPost, "/api/remote/connect",
			map[string]string{"peer": r.peer.DNSName}, new(remote.Info), ""), false
	case "p":
		if r.kind != rowPeer {
			return notice(ui.LevelInfo, "p pairs with the computer under the cursor"), false
		}
		c.askCode(r.peer)
	case "d":
		if c.conn() == nil {
			return notice(ui.LevelInfo, "not connected to another computer"), false
		}
		return c.request(local, "disconnect", http.MethodPost, "/api/remote/disconnect", nil, nil, ""), false
	case "s":
		return c.toggleShare(local), false
	case "n":
		if c.ov == nil || !c.ov.Share.Enabled {
			return notice(ui.LevelInfo, "turn sharing on first (s): a pairing code lets another device connect to this computer"), false
		}
		return c.request(local, "new pairing code", http.MethodPost, "/api/remote/share/code", nil, new(share.Code), ""), false
	case "f":
		if r.kind != rowMirror {
			return notice(ui.LevelInfo, "f turns forwarding of the port under the cursor on or off"), false
		}
		return c.forward(local, r.mirror), false
	}
	return nil, false
}

func (c *computers) askCode(p remote.Peer) {
	c.pair = &prompt{label: "Pairing code from " + p.Name}
	c.pairPeer = Peer{Name: p.Name, DNSName: p.DNSName}
}

// askPair opens the code prompt for a computer that answered that it
// needs pairing.
func (c *computers) askPair(p Peer) {
	c.pair = &prompt{label: "Pairing needed. Code from " + p.Name}
	c.pairPeer = p
}

// request sends an action; switchTo, when set, makes the session follow
// the connection once it succeeds.
func (c *computers) request(local *client.Client, what, method, path string, body, into any, switchTo string) tea.Cmd {
	c.busy = what
	timeout := requestTimeout
	if strings.HasPrefix(what, "connect") || strings.HasPrefix(what, "pair") {
		timeout = connectTimeout
	}
	inner := call(local, c.seq, what, method, path, body, into, timeout)
	if switchTo == "" {
		return inner
	}
	return func() tea.Msg {
		d, _ := inner().(doneMsg)
		if d.err == nil {
			d.switchTo = switchTo
		}
		return d
	}
}

// forward turns forwarding of a port of the connected computer on or off.
func (c *computers) forward(local *client.Client, m remote.Mirror) tea.Cmd {
	on := m.State == "off"
	verb := "off"
	if on {
		verb = "on"
	}
	return c.request(local, "forwarding of port "+strconv.Itoa(m.Port)+" "+verb, http.MethodPut, "/api/remote/forwarding",
		map[string]any{"port": m.Port, "on": on}, new(remote.Info), "")
}

// toggleShare turns sharing off at once, or on after a confirmation: it
// exposes this daemon to the tailnet.
func (c *computers) toggleShare(local *client.Client) tea.Cmd {
	if c.ov == nil {
		return notice(ui.LevelInfo, "still loading this computer's sharing")
	}
	if c.ov.Share.Enabled {
		return c.request(local, "sharing off", http.MethodPut, "/api/remote/share", map[string]bool{"enabled": false}, new(server.RemoteOverview), "")
	}
	seq := c.seq
	yes := call(local, seq, "sharing on", http.MethodPut, "/api/remote/share", map[string]bool{"enabled": true}, new(server.RemoteOverview), requestTimeout)
	return func() tea.Msg {
		return ui.ConfirmMsg{
			Title: "Share this computer?",
			Body: "Turns sharing on: this daemon becomes reachable on your tailnet, and devices you pair get full control of the dev servers on this computer. " +
				"It is saved as share = true in the config.",
			Confirm: "Share",
			Yes:     yes,
		}
	}
}

// names are the computers :on completes: this one, then the ones found.
func (c *computers) names() []string {
	out := []string{LocalName}
	if c.disc != nil {
		for _, p := range c.disc.Peers {
			out = append(out, p.Name)
		}
	} else if cn := c.conn(); cn != nil {
		out = append(out, cn.Peer.Name)
	}
	return out
}

// lines draws the overlay's content inner cells wide.
func (c *computers) lines(env *ui.Env, inner int, now time.Time) (lines []string, cursorLine int) {
	s := env.Styles.OnRaised()
	g := env.Glyphs
	cursorLine = -1
	rows := c.rows()
	c.cursor = max(0, min(c.cursor, len(rows)-1))
	ri := 0
	cl := &c.clicks
	*cl = (*cl)[:0]
	add := func(l string) {
		lines = append(lines, l)
		cl.line(-1)
	}
	addRow := func(l string) {
		if ri == c.cursor {
			cursorLine = len(lines)
		}
		lines = append(lines, l)
		cl.line(ri)
		ri++
	}
	// toggle makes the whole of the last line run key.
	toggle := func(key string) { cl.on(0, ansi.StringWidth(lines[len(lines)-1]), key) }
	mark := func(on bool) string {
		if on {
			return s.Accent.Render(g.State("running", 0)) + s.Surface.Render(" ")
		}
		return s.Surface.Render("  ")
	}
	section := func(t string) { add(s.Bold.Render(t)) }

	section("This computer")
	self := ""
	if c.disc != nil {
		self = c.disc.Tailnet.Self
	}
	here := s.Text.Render("this computer")
	if self != "" {
		here = s.Text.Render(plain(self))
	}
	if !env.Session.Remote() {
		here += s.Muted.Render(" · this session")
	} else {
		here += s.Muted.Render(" · enter returns here")
	}
	addRow(mark(!env.Session.Remote()) + here)
	if c.ov != nil {
		sh := c.ov.Share
		switch {
		case sh.Enabled && sh.Active && sh.URL != "":
			add("    " + s.Running.Render("Sharing on") + s.Muted.Render(" · reachable at "+plain(sh.URL)))
			toggle("s")
		case sh.Enabled:
			text := "Sharing on · not reachable on the tailnet yet"
			if sh.Error != "" {
				text = "Sharing on · " + plain(sh.Error)
			}
			add("    " + s.Warn.Render(text))
			toggle("s")
		default:
			add("    " + s.Muted.Render("Sharing off: s lets your other devices connect"))
			toggle("s")
		}
		if c.code != nil {
			left := c.code.ExpiresAt.Sub(now).Round(time.Second)
			text := s.Muted.Render("expired")
			if left > 0 {
				text = s.Muted.Render("expires in " + left.String() + "; enter it on the other computer, under Computers")
			}
			add("    " + s.Text.Render("Pairing code ") + s.AccentBold.Render(plain(c.code.Code)) + s.Muted.Render(" · ") + text)
		}
		if devs := sh.Devices; len(devs) > 0 {
			var parts []string
			for _, d := range devs {
				p := plain(d.Name)
				if d.Connected {
					p += " (connected now)"
				}
				parts = append(parts, p)
			}
			add("    " + s.Muted.Render("Paired devices: "+strings.Join(parts, ", ")))
		}
	}

	add("")
	section("Other computers")
	switch {
	case c.disc == nil && c.scanning:
		add("  " + s.Muted.Render("Looking for computers on your tailnet"+g.Ellipsis))
	case c.disc != nil && !c.disc.Tailnet.Available:
		why := "Tailscale is not running or not installed on this computer."
		if c.disc.Tailnet.Error != "" {
			why = plain(c.disc.Tailnet.Error)
		}
		add("  " + s.Warn.Render(why))
		add("  " + s.Muted.Render("Computers connect over Tailscale; see docs/other-computers.md."))
	case c.disc != nil && len(c.disc.Peers) == 0:
		add("  " + s.Muted.Render("No other computer is sharing its dashboard."))
		add("  " + s.Muted.Render("Turn sharing on there: s in its Computers overlay, or `"+app.ID+" share on`."))
	}
	if c.disc != nil {
		nameW := 0
		for _, p := range c.disc.Peers {
			nameW = max(nameW, ansi.StringWidth(plain(p.Name)))
		}
		nameW = min(nameW, max(8, inner/3))
		for _, p := range c.disc.Peers {
			on := env.Session.Remote() && connIs(p.Name, p.Label, p.DNSName, env.Session.Computer)
			addRow(mark(on) + s.Text.Render(pad(plain(p.Name), nameW, g.Ellipsis)) + s.Surface.Render("  ") + s.Muted.Render(c.peerState(p)))
		}
		if c.scanning {
			add("  " + s.Muted.Render("Scanning again"+g.Ellipsis))
		}
	}

	if cn := c.conn(); cn != nil {
		add("")
		title := "Connected to " + plain(cn.Peer.Name)
		switch cn.Status {
		case remote.Connecting:
			title = "Connecting to " + plain(cn.Peer.Name) + g.Ellipsis
		case remote.Reconnecting:
			title = "Reconnecting to " + plain(cn.Peer.Name) + g.Ellipsis
		}
		section(title)
		if cn.Error != "" {
			add("  " + s.Error.Render(plain(cn.Error)))
		}
		if len(cn.Mirrors) == 0 {
			add("  " + s.Muted.Render("No ports to forward"))
		}
		for _, m := range cn.Mirrors {
			what := m.Server
			if what == "" {
				what = m.Container
			}
			head := "  " + s.Port.Render(pad(strconv.Itoa(m.Port), 6, "")) + s.Text.Render(pad(plain(what), 16, g.Ellipsis)) + s.Surface.Render(" ")
			state := mirrorState(s.Muted, m)
			addRow(head + state)
			// The forwarding state turns it on or off.
			x := ansi.StringWidth(head)
			cl.on(x, x+ansi.StringWidth(state), "f")
		}
	}
	if c.err != "" {
		add("")
		add(s.Error.Render(plain(c.err)))
	}
	if c.busy != "" {
		add(s.Muted.Render(plain(c.busy) + g.Ellipsis))
	}
	if c.pair == nil {
		add("")
		lines = append(lines, actionLine(env.Styles, g, cl, c.actions()))
	}
	if c.pair != nil {
		add("")
		add(s.Text.Render(plain(c.pair.label)+": ") + s.AccentBold.Render(c.pair.masked(g.ID == "ascii")) + s.Accent.Render(g.Cursor))
		add(s.Muted.Render("Get a code on " + plain(c.pairPeer.Name) + ": open Computers there, or run `" + app.ID + " share code`."))
	}
	return lines, cursorLine
}

// actions are the Computers overlay's actions that need no row, as
// clickable labels: sharing, a pairing code, disconnect and a new scan.
// The row actions run on a double click (enter) or a click on their part
// of the row.
func (c *computers) actions() [][2]string {
	share := [2]string{"s", "share on"}
	var out [][2]string
	if c.ov != nil && c.ov.Share.Enabled {
		out = append(out, [2]string{"s", "share off"}, [2]string{"n", "pairing code"})
	} else {
		out = append(out, share)
	}
	if c.conn() != nil {
		out = append(out, [2]string{"d", "disconnect"})
	}
	return append(out, [2]string{"R", "scan"})
}

// peerState is a computer's paired, connected, version and servers.
func (c *computers) peerState(p remote.Peer) string {
	var parts []string
	if p.Paired {
		parts = append(parts, "paired")
	} else {
		parts = append(parts, "not paired")
	}
	if c.connected(p) {
		parts = append(parts, "connected")
	}
	if p.Version != "" {
		parts = append(parts, versionLabel(p.Version))
	}
	if p.Servers != nil {
		if p.Servers.Total == 0 {
			parts = append(parts, "no servers")
		} else {
			parts = append(parts, fmt.Sprintf("%d of %d running", p.Servers.Running, p.Servers.Total))
		}
	}
	return strings.Join(parts, " · ")
}

// mirrorState says how a forwarded port opens here.
func mirrorState(muted lipgloss.Style, m remote.Mirror) string {
	switch m.State {
	case "mirrored":
		local := m.Port
		if m.Local > 0 {
			local = m.Local
		}
		return muted.Render("forwarded to localhost:" + strconv.Itoa(local))
	case "busy":
		return muted.Render("busy here: the port is taken on this computer")
	case "off":
		return muted.Render("off")
	case "limit":
		return muted.Render("over the limit")
	}
	return muted.Render(m.State)
}

// hints are the footer's keys for the row under the cursor.
func (c *computers) hints(env *ui.Env, enter string) [][2]string {
	if c.pair != nil {
		return [][2]string{{"type", "the code"}, {enter, "pair and connect"}, {"esc", "cancel"}}
	}
	r, _ := c.current()
	var out [][2]string
	switch r.kind {
	case rowThis:
		if env.Session.Remote() {
			out = append(out, [2]string{enter, "back to this computer"})
		}
	case rowPeer:
		if r.peer.Paired {
			out = append(out, [2]string{enter, "switch to it"}, [2]string{"c", "connect"})
		} else {
			out = append(out, [2]string{enter, "pair"})
		}
		out = append(out, [2]string{"p", "pair"})
	case rowMirror:
		out = append(out, [2]string{"f", "forwarding on/off"})
	}
	if c.conn() != nil {
		out = append(out, [2]string{"d", "disconnect"})
	}
	share := "share on"
	if c.ov != nil && c.ov.Share.Enabled {
		share = "share off"
		out = append(out, [2]string{"s", share}, [2]string{"n", "pairing code"})
	} else {
		out = append(out, [2]string{"s", share})
	}
	return append(out, [2]string{"R", "scan"}, [2]string{"esc", "close"})
}

func plain(s string) string { return viewfmt.PlainText(s) }

// pad cuts s to w cells with ell or pads it with spaces.
func pad(s string, w int, ell string) string {
	if ansi.StringWidth(s) > w {
		s = ansi.Truncate(s, w, ell)
	}
	return s + strings.Repeat(" ", max(0, w-ansi.StringWidth(s)))
}

func versionLabel(v string) string {
	if v != "" && v[0] >= '0' && v[0] <= '9' {
		return "v" + v
	}
	return v
}

func notice(l ui.Level, text string) tea.Cmd {
	return func() tea.Msg { return ui.NoticeMsg{Text: text, Level: l} }
}
