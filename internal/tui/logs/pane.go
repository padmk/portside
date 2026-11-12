package logs

import (
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/padmk/portside/internal/supervisor"
	"github.com/padmk/portside/internal/tui/act"
	"github.com/padmk/portside/internal/tui/keymap"
	"github.com/padmk/portside/internal/tui/theme"
	"github.com/padmk/portside/internal/tui/ui"
)

const (
	// maxLines is how many lines the pane keeps.
	maxLines = 10000
	// wheelRows is how far one wheel step scrolls.
	wheelRows = 3
	// scrollStep is how far J and K scroll.
	scrollStep = 3
	// gutterWidth is the time column with its space.
	gutterWidth = 9
)

// Pane is the live log view of one server. It renders only its content;
// the detail container draws the frame around it. Ingesting output is in
// ingest.go, the viewport in view.go, visual selection in visual.go and
// the read state kept per server in memory.go.
type Pane struct {
	server  string
	feed    *feed
	loading bool
	// settle ends the feed once the output it has to send arrived: the
	// output of a compose job that ended (Settle).
	settle bool
	lost   bool
	// glyphs is the set of the last frame, for Status.
	glyphs theme.Glyphs

	buf  *Buffer
	base int // absolute index of buf.At(0)
	in   ingestState

	follow, wrap bool
	// unseen counts the lines added while paused.
	unseen int
	// gutter is the time column: automatic (stamps decides) until `t`
	// picks one for the server shown.
	gutter gutterMode
	stamps stampDecision
	// anchor is the absolute index of the bottom line while paused and
	// rowOff how many of its rows are below the screen.
	anchor, rowOff int
	// left is the first cell shown while lines are not wrapped.
	left int
	// width and rows are the line area of the last frame; top is the
	// sequence index of its top line.
	width, rows, top int

	search  search
	editing bool
	visual  bool
	// vfollow is whether the pane followed new output before visual mode.
	vfollow bool
	// vstart and vcur are the absolute indexes of the visual selection's
	// first line and its moving end.
	vstart, vcur int

	memory *memory
	// restore is the read state to apply when the next scrollback arrives.
	restore *readState
}

type gutterMode int

const (
	gutterAuto gutterMode = iota
	gutterShown
	gutterHidden
)

// New returns an empty log pane following new output.
func New() *Pane {
	return &Pane{buf: NewBuffer(maxLines), follow: true, search: search{current: -1}, in: newIngest(), glyphs: theme.Unicode, memory: &memory{}}
}

func (p *Pane) Scope() keymap.Scope { return keymap.ScopeLogs }

// Server is the server shown, empty for none.
func (p *Pane) Server() string { return p.server }

// Close ends the stream.
func (p *Pane) Close() {
	if p.feed != nil {
		p.feed.close()
		p.feed = nil
	}
}

// Show switches to a server's logs (none for an empty name): the read
// state of the previous server is kept, its stream ends, the lines are
// dropped and a new stream starts, which restores the new server's read
// state when it was shown before.
func (p *Pane) Show(env *ui.Env, server string) tea.Cmd {
	if server == p.server {
		return nil
	}
	if p.server != "" && !p.loading {
		p.memory.save(p.server, p.readState())
	}
	p.Close()
	if p.editing || p.visual {
		env.EnterMode(ui.ModeNormal)
	}
	*p = Pane{buf: p.buf, follow: true, wrap: p.wrap, search: search{current: -1},
		server: server, in: newIngest(), glyphs: env.Glyphs, memory: p.memory}
	p.buf.Reset()
	if server == "" {
		return nil
	}
	if st, ok := p.memory.take(server); ok {
		p.restore = &st
		p.gutter = st.gutter
	}
	p.loading = true
	p.feed = openFeed(env.ProgramContext(), env.Client, server)
	return p.feed.wait()
}

// settleWait is how long a feed that settles stays open for output still
// on its way: what the job wrote before it ended.
const settleWait = time.Second

// settleMsg ends the feed id once settleWait has passed.
type settleMsg struct{ id uint64 }

// Settle ends the stream once the output already sent has arrived,
// keeping the lines: for a compose job that ended, whose output does not
// change any more. The scrollback of a new connection holds all of it, so
// a pane still loading closes the feed when that arrives; one that has it
// waits settleWait for the last live output. Show of another source opens
// a new feed.
func (p *Pane) Settle() tea.Cmd {
	if p.feed == nil || p.settle {
		return nil
	}
	p.settle = true
	if p.loading {
		return nil
	}
	id := p.feed.id
	return tea.Tick(settleWait, func(time.Time) tea.Msg { return settleMsg{id} })
}

// Owns reports whether msg is output of this pane's current feed: a
// container's two panes route each message to the one that owns it.
func (p *Pane) Owns(msg tea.Msg) bool {
	m, ok := msg.(feedMsg)
	return ok && p.feed != nil && m.id == p.feed.id
}

func (p *Pane) Update(env *ui.Env, msg tea.Msg) tea.Cmd {
	p.glyphs = env.Glyphs
	switch msg := msg.(type) {
	case feedMsg:
		// Only the message of the current feed schedules its next read:
		// another feed's (an earlier one, or another pane's) is dropped.
		if !p.Owns(msg) {
			return nil
		}
		p.apply(env, msg)
		if p.settle && msg.reset {
			p.Close()
			p.lost = false
			return nil
		}
		return p.feed.wait()
	case settleMsg:
		if p.feed != nil && p.feed.id == msg.id {
			p.Close()
			p.lost = false
		}
	case ui.LogsClearedMsg:
		if msg.Err != nil {
			return notice("clear logs of "+msg.Server+": "+msg.Err.Error(), ui.LevelError)
		}
		if msg.Server == p.server {
			p.leaveModes(env)
			p.resetLines()
		}
		return notice("cleared the logs of "+msg.Server, ui.LevelSuccess)
	case tea.KeyPressMsg:
		switch {
		case p.editing:
			return p.searchKey(env, msg)
		case p.visual:
			return p.visualKey(env, msg)
		}
	case tea.MouseWheelMsg:
		switch msg.Mouse().Button {
		case tea.MouseWheelUp:
			p.scroll(-wheelRows)
		case tea.MouseWheelDown:
			p.scroll(wheelRows)
		case tea.MouseWheelLeft:
			p.scrollLeft(-1)
		case tea.MouseWheelRight:
			p.scrollLeft(1)
		}
	}
	return nil
}

// leaveModes ends the search prompt and visual selection, giving the
// keys back.
func (p *Pane) leaveModes(env *ui.Env) {
	if p.editing || p.visual {
		p.editing, p.visual = false, false
		env.EnterMode(ui.ModeNormal)
	}
}

func (p *Pane) Do(env *ui.Env, a keymap.Action) tea.Cmd {
	p.glyphs = env.Glyphs
	page := max(1, p.rows)
	switch a.ID {
	case "down":
		p.scroll(1)
	case "up":
		p.scroll(-1)
	case "scroll-down":
		p.scroll(scrollStep)
	case "scroll-up":
		p.scroll(-scrollStep)
	case "half-down":
		p.scroll(max(1, page/2))
	case "half-up":
		p.scroll(-max(1, page/2))
	case "next-page", "page-down":
		p.scroll(page)
	case "prev-page", "page-up":
		p.scroll(-page)
	case "scroll-left":
		return p.scrollLeft(-1)
	case "scroll-right":
		return p.scrollLeft(1)
	case "top":
		if p.seqLen() > 0 {
			p.follow = false
			p.setPos(p.posWithTop(0))
		}
	case "bottom":
		p.setFollow(true)
	case "follow":
		p.setFollow(!p.follow)
	case "wrap":
		q := p.bottomPos()
		p.wrap = !p.wrap
		p.left = 0
		if !p.follow && p.seqLen() > 0 {
			p.setPos(pos{q.k, 0})
		}
		p.clampAnchor()
	case "timestamps":
		if p.showStamps() {
			p.gutter = gutterHidden
		} else {
			p.gutter = gutterShown
		}
		p.clampAnchor()
	case "search":
		if p.server == "" {
			return nil
		}
		p.startSearch(env)
	case "next-match":
		return p.jumpMatch(1)
	case "prev-match":
		return p.jumpMatch(-1)
	case "keep-matching":
		return p.toggleFilter()
	case "visual":
		p.startVisual(env)
	case "select-down", "select-up":
		if p.startVisual(env) {
			p.moveSelection(map[string]int{"select-down": 1, "select-up": -1}[a.ID])
		}
	case "yank":
		if p.seqLen() == 0 {
			return nil
		}
		abs := p.anchorAbs()
		return p.copyLines(abs, abs)
	case "attach":
		if p.server == "" {
			return nil
		}
		return Attach(env, p.server)
	case "clear-logs":
		if p.server == "" {
			return nil
		}
		return act.ConfirmClearLogs(env, p.server)
	}
	return nil
}

// setFollow turns following new output on or off; off keeps the bottom
// line where it is.
func (p *Pane) setFollow(on bool) {
	if on {
		p.follow, p.unseen, p.rowOff = true, 0, 0
		return
	}
	if p.follow && p.seqLen() > 0 {
		p.anchor, p.rowOff = p.seqAt(p.seqLen()-1), 0
	}
	p.follow = false
}

// Back clears the search or the filter of matching lines; false when
// there is neither, so esc goes back to the servers.
func (p *Pane) Back() bool {
	if p.search.re == nil && !p.search.only {
		return false
	}
	p.clearSearch()
	p.clampAnchor()
	return true
}

// On reports whether a toggle action ("follow", "wrap", "timestamps",
// "keep-matching") is on, for the menus' check marks.
func (p *Pane) On(id string) bool {
	switch id {
	case "follow":
		return p.follow
	case "wrap":
		return p.wrap
	case "timestamps":
		return p.showStamps()
	case "keep-matching":
		return p.filtering()
	}
	return false
}

// Status is the log mode for the frame's bottom border: independent
// pieces such as "PAUSED · 240 new · search error 3/17 · wrap".
func (p *Pane) Status() string {
	var parts []string
	if p.lost {
		parts = append(parts, "stream lost, retrying")
	}
	if p.server != "" && !p.loading {
		switch {
		case p.settle && p.feed == nil && p.follow:
			// A settled stream gets no more output to follow.
			parts = append(parts, "complete")
		case p.follow:
			parts = append(parts, "follow")
		default:
			parts = append(parts, "PAUSED")
			if p.unseen > 0 {
				parts = append(parts, strconv.Itoa(p.unseen)+" new")
			}
		}
		if s := p.searchStatus(); s != "" {
			parts = append(parts, s)
		}
		switch {
		case p.wrap:
			parts = append(parts, "wrap")
		case p.left > 0:
			parts = append(parts, "col "+strconv.Itoa(p.left+1))
		}
	}
	sep := p.glyphs.Separator
	if sep == "" {
		sep = " · "
	}
	return strings.Join(parts, sep)
}

// View renders the lines that fit, newest at the bottom, and the search
// prompt while it is open.
func (p *Pane) View(env *ui.Env, w, h int, focused bool) string {
	p.glyphs = env.Glyphs
	if w <= 0 || h <= 0 {
		return ""
	}
	rows := h
	if p.editing {
		rows--
	}
	if p.width != w || p.rows != rows {
		p.width, p.rows = w, rows
		p.clampAnchor()
	}
	var out []string
	switch {
	case p.server == "":
		out = centered(env, "Select a server to see its logs", w, rows)
	case p.loading:
		out = centered(env, "Loading logs"+p.dots(), w, rows)
	case p.seqLen() == 0 && p.filtering():
		out = centered(env, "No lines match "+p.search.query, w, rows)
	case p.seqLen() == 0:
		out = centered(env, "No output yet", w, rows, p.emptyContext(env))
	default:
		out = p.visibleRows(env, rows)
	}
	if p.editing {
		out = append(out, p.prompt(env, w))
	}
	return strings.Join(out, "\n")
}

// dots ends a text that says something is under way: "…", or "..." in
// the ASCII set.
func (p *Pane) dots() string {
	if p.glyphs.ID == theme.ASCII.ID {
		return "..."
	}
	return "…"
}

// emptyContext is the line under "No output yet" that says why and what
// to do: a stopped server's state and the key that starts it; "" else.
func (p *Pane) emptyContext(env *ui.Env) string {
	if env.State == nil {
		return ""
	}
	for _, v := range env.State.Servers {
		if v.Name == p.server && v.Display() == supervisor.Display(supervisor.Stopped) {
			return "stopped" + env.Glyphs.Separator + "space starts it"
		}
	}
	return ""
}

// centered is text in the middle of w x h, and under it context when
// there is one and room for it.
func centered(env *ui.Env, text string, w, h int, context ...string) []string {
	out := make([]string, h)
	blank := strings.Repeat(" ", w)
	for i := range out {
		out[i] = blank
	}
	if h == 0 {
		return out
	}
	line := func(y int, text string) {
		text = ansi.Truncate(text, w, env.Glyphs.Ellipsis)
		pad := max(0, (w-ansi.StringWidth(text))/2)
		out[y] = ui.Fit(strings.Repeat(" ", pad)+env.Styles.Text2.Render(text), w)
	}
	y := (h - 1) / 2
	line(y, text)
	for _, c := range context {
		if y++; c != "" && y < h {
			line(y, c)
		}
	}
	return out
}

func notice(text string, l ui.Level) tea.Cmd {
	return func() tea.Msg { return ui.NoticeMsg{Text: text, Level: l} }
}
