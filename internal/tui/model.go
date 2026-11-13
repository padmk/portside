package tui

import (
	"maps"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/padmk/portside/internal/client"
	"github.com/padmk/portside/internal/supervisor"
	"github.com/padmk/portside/internal/tui/act"
	"github.com/padmk/portside/internal/tui/containers"
	"github.com/padmk/portside/internal/tui/edit"
	"github.com/padmk/portside/internal/tui/keymap"
	"github.com/padmk/portside/internal/tui/live"
	"github.com/padmk/portside/internal/tui/overlay"
	"github.com/padmk/portside/internal/tui/remote"
	"github.com/padmk/portside/internal/tui/theme"
	"github.com/padmk/portside/internal/tui/ui"
	"github.com/padmk/portside/internal/viewfmt"
)

const (
	// spinnerInterval advances the spinner while something is in progress.
	spinnerInterval = 120 * time.Millisecond
	noticeTime      = 4 * time.Second
	unknownTime     = 2 * time.Second
	historyLimit    = 200
)

// notice is the message the footer shows instead of the key hints.
type notice struct {
	ui.NoticeMsg
	seq int
}

// historyEntry is one message of `:messages`; Item is what it is about,
// which enter on its line selects.
type historyEntry struct {
	At time.Time
	ui.NoticeMsg
	Item ui.Selection
}

// Timer messages carry the sequence number they were started with, so a
// stale timer does nothing.
type (
	toastTickMsg     struct{}
	noticeTimeoutMsg struct{ seq int }
	spinnerTickMsg   struct{}
	clockTickMsg     struct{}
)

// screenMode is how much of the screen the focused pane takes, as in
// lazygit: normal, half (the servers take half the width, or the main panel
// all of it) or full (only the focused pane).
type screenMode int

const (
	screenNormal screenMode = iota
	screenHalf
	screenFull
	screenModes
)

func (s screenMode) String() string { return [...]string{"normal", "half", "full"}[s] }

// model is the root model: it owns the environment, the layout and the
// chrome, and passes everything else to the panes.
type model struct {
	// panicked is a goroutine's panic (ui.PanicMsg) that quit the program;
	// Run returns it.
	panicked error

	env    ui.Env
	stream *live.Stream
	prefs  prefs

	family   theme.Family
	variant  string // "", "dark" or "light"
	termDark bool
	// mono is set when the terminal shows no colors (theme.Monochrome).
	mono bool

	left  []ui.Pane
	right ui.Pane
	focus int // index into panes()
	// leftActive is the left-column pane that keeps its full height: the
	// focused one, or the last focused while the main panel is.
	leftActive int
	screenMode screenMode
	mode       ui.Mode
	// modeOwner is the pane (index into panes()) that owns an input mode
	// other than normal and gets its keys; -1 for the command line.
	modeOwner int

	// ops holds the operation in progress on each item; opSeq numbers
	// them.
	ops   map[ui.Selection]op
	opSeq int
	// stateFilter is the display state the servers list is filtered to.
	stateFilter string
	// connErr is the last connection error kept in the history.
	connErr string

	width, height int
	rects         []rect // each visible pane's rectangle, for mouse routing

	notice    *notice
	noticeSeq int
	history   []historyEntry
	spinning  bool
	clocking  bool
	retry     time.Time
	// confirm is the open confirmation and dialog its choice and layout.
	confirm *ui.ConfirmMsg
	dialog  *overlay.Dialog
	// placed is set once the first snapshot picked the selection.
	placed bool
	// edit is the config editing: :config and the forms.
	edit edit.Editor
	// sys is the computers, the daemon and the agent setup; gen numbers
	// the sessions (:on), streams runs the current one's live stream (nil
	// in tests).
	sys     remote.Host
	gen     int
	streams *remote.Streams

	overlays
	toasting
	itemPanes
}

func newModel(c *client.Client, stream *live.Stream, session ui.Session, p prefs, family theme.Family, variant string, glyphs theme.Glyphs) *model {
	keys := keymap.Default()
	left, right := newPanes()
	m := &model{
		env: ui.Env{
			Client:  c,
			Session: session,
			Glyphs:  glyphs,
			Keys:    keys,
			Pending: map[ui.Selection]string{},
			Now:     time.Now,
		},
		stream:    stream,
		prefs:     p,
		family:    family,
		variant:   variant,
		termDark:  true,
		left:      left,
		right:     right,
		modeOwner: -1,
		ops:       map[ui.Selection]op{},
	}
	m.sys.Local = c
	m.restyle()
	m.initLeft()
	return m
}

// panes lists the left column's panes, then the right pane.
func (m *model) panes() []ui.Pane { return append(append([]ui.Pane{}, m.left...), m.right) }

func (m *model) focused() ui.Pane { return m.panes()[m.focus] }

// restyle rebuilds the styles from the theme and the background.
func (m *model) restyle() {
	dark := m.termDark
	if m.variant != "" {
		dark = m.variant == "dark"
	}
	if m.mono {
		m.env.Styles = theme.NewMono(m.family.Variant(dark))
		return
	}
	m.env.Styles = theme.New(m.family.Variant(dark))
}

func (m *model) Init() tea.Cmd {
	return tea.Batch(tea.RequestBackgroundColor, m.sys.WatchConnection(&m.env))
}

// Update handles a message of the current session (a ui.GenMsg of an
// earlier one is dropped) and tags the commands it starts with the
// session, so their replies are dropped too once :on switched computer.
func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	// A panic quits whatever session it came from.
	if g, ok := msg.(ui.GenMsg); ok {
		if p, ok := g.Msg.(ui.PanicMsg); ok {
			msg = p
		}
	}
	if p, ok := msg.(ui.PanicMsg); ok {
		m.panicked = p
		return m, tea.Quit
	}
	if g, ok := msg.(ui.GenMsg); ok {
		if g.Gen != m.gen {
			return m, nil
		}
		msg = g.Msg
	}
	_, cmd := m.handle(msg)
	return m, remote.Tag(m.gen, cmd)
}

func (m *model) handle(msg tea.Msg) (tea.Model, tea.Cmd) {
	if cmd, ok := m.editUpdate(msg); ok {
		return m, cmd
	}
	if cmd, ok := m.sys.Update(&m.env, msg); ok {
		return m, cmd
	}
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.layout()
		return m, m.broadcast(msg)
	case tea.BackgroundColorMsg:
		m.termDark = msg.IsDark()
		m.restyle()
		return m, nil
	case tea.ColorProfileMsg:
		m.mono = theme.Monochrome(msg.Profile)
		m.restyle()
		return m, nil
	case tea.KeyPressMsg:
		return m, m.after(m.key(msg))
	case tea.MouseMsg:
		return m, m.after(m.mouse(msg))
	case ui.ActMsg:
		return m, m.after(m.actMsg(msg))
	case takeoverMsg:
		return m, m.after(m.takeoverConfirmed(msg))
	case ui.HidePaneMsg:
		if msg.Scope == keymap.ScopePorts && m.showPorts {
			return m, m.hidePorts()
		}
		return m, nil
	case ui.StateMsg:
		toasts := m.stateToasts(msg.State)
		m.env.State = msg.State
		m.keepSelection()
		m.syncLeft()
		m.layout()
		cmd := m.broadcast(msg)
		return m, tea.Batch(cmd, m.followSelection(), m.spin(), toasts, m.sys.Observe(msg.State))
	case ui.ConnMsg:
		toast := m.connToast(msg.Connected)
		m.env.Connected = msg.Connected
		m.retry = msg.Retry
		m.keepConnErr(msg)
		return m, tea.Batch(m.broadcast(msg), m.clock(), toast)
	case ui.NoticeMsg:
		return m, m.show(msg, noticeTime)
	case ui.ToastMsg:
		return m, m.toast(msg)
	case toastTickMsg:
		m.dropToasts()
		return m, nil
	case ui.OpDoneMsg:
		return m, tea.Batch(m.finish(msg), m.broadcast(msg), m.spin())
	case act.ShellDoneMsg:
		return m, m.show(act.ShellResult(msg), noticeTime)
	case ui.SelectMsg:
		m.env.Selected, m.env.Target = msg.Item, msg.Item
		return m, m.broadcast(msg)
	case ui.RunMsg:
		return m, m.act(msg.Action, msg.Item)
	case remote.SwitchedMsg:
		return m, m.switched(msg)
	case ui.FocusDetailMsg:
		// An input mode keeps the focus on its pane.
		if m.mode != ui.ModeNormal {
			return m, nil
		}
		m.focusPane(len(m.left))
		return m, nil
	case ui.FocusListMsg:
		m.focusPane(m.leftActive)
		return m, m.followSelection()
	case ui.ModeMsg:
		m.setMode(msg.Mode, m.focus)
		return m, nil
	case ui.ConfirmMsg:
		m.confirm = &msg
		m.dialog = &overlay.Dialog{Title: msg.Title, Body: msg.Body, Confirm: msg.Confirm}
		return m, nil
	case noticeTimeoutMsg:
		if m.notice != nil && m.notice.seq == msg.seq {
			m.notice = nil
		}
		return m, nil
	case spinnerTickMsg:
		m.spinning = false
		if !m.animating() {
			return m, nil
		}
		m.env.Frame++
		return m, m.spin()
	case clockTickMsg:
		m.clocking = false
		return m, m.clock()
	}
	return m, m.broadcast(msg)
}

// broadcast passes a message to every pane.
func (m *model) broadcast(msg tea.Msg) tea.Cmd {
	var cmds []tea.Cmd
	for i := range m.panes() {
		cmds = append(cmds, m.update(i, msg))
	}
	return tea.Batch(cmds...)
}

// update passes a message to the pane at index i (into panes()) and
// applies the input mode it entered.
func (m *model) update(i int, msg tea.Msg) tea.Cmd {
	cmd := m.panes()[i].Update(&m.env, msg)
	m.takeMode(i)
	return cmd
}

// do runs an action in the pane at index i and applies the input mode it
// entered.
func (m *model) do(i int, a keymap.Action) tea.Cmd {
	cmd := m.panes()[i].Do(&m.env, a)
	m.takeMode(i)
	return cmd
}

// takeMode applies a mode the pane at index i asked for with EnterMode.
func (m *model) takeMode(i int) {
	if mode, ok := m.env.TakeMode(); ok {
		m.setMode(mode, i)
	}
}

// setMode switches the input mode; a mode other than normal belongs to
// the pane at index owner, which is focused with it.
func (m *model) setMode(mode ui.Mode, owner int) {
	m.mode = mode
	if mode == ui.ModeNormal {
		m.modeOwner = -1
		return
	}
	m.modeOwner = owner
	if owner >= 0 && owner != m.focus {
		m.focusPane(owner)
	}
}

// keepConnErr keeps a connection error in the history, once until it
// changes, so `:messages` explains an authentication or protocol failure.
func (m *model) keepConnErr(msg ui.ConnMsg) {
	if msg.Connected {
		m.connErr = ""
		return
	}
	if msg.Err == nil || msg.Err.Error() == m.connErr {
		return
	}
	m.connErr = msg.Err.Error()
	m.remember(ui.NoticeMsg{Text: "connection: " + m.connErr, Level: ui.LevelError})
}

// keepSelection keeps the selection on a server that exists. The first
// snapshot selects the first server that needs attention (crashed or not
// answering), else the first running one, else the first; a server that
// disappears gives way to the first one.
func (m *model) keepSelection() {
	st := m.env.State
	if st == nil {
		return
	}
	sel := m.env.Selected
	switch sel.Kind {
	case ui.SelectContainer:
		return
	case ui.SelectServer:
		if m.hasServer(sel.Name) {
			m.placed = true
			m.keepTarget()
			return
		}
	default:
	}
	m.env.Selected = ui.Selection{}
	if ordered := viewfmt.Ordered(st); len(ordered) > 0 {
		m.env.Selected = ui.ServerItem(ordered[0].Name)
		if !m.placed {
			m.env.Selected = ui.ServerItem(firstToShow(ordered).Name)
		}
		m.env.Target = m.env.Selected
		m.placed = true
	}
	m.keepTarget()
}

// firstToShow is the server the first frame opens on, from servers in
// list order: the first that needs attention, else the first running,
// else the first. It is the one owner of that rule.
func firstToShow(servers []supervisor.View) supervisor.View {
	for _, v := range servers {
		if needsAttention(v) {
			return v
		}
	}
	for _, v := range servers {
		if v.Display() == supervisor.Display(supervisor.Running) {
			return v
		}
	}
	return servers[0]
}

// needsAttention reports whether a server broke: crashed or blocked,
// running but not answering, or exited with an error.
func needsAttention(v supervisor.View) bool {
	switch v.Display() {
	case supervisor.Display(supervisor.Crashed), supervisor.Display(supervisor.Blocked), supervisor.DisplayUnready:
		return true
	case supervisor.Display(supervisor.Exited):
		return v.ExitCode != nil && *v.ExitCode != 0
	case supervisor.DisplayExternal, supervisor.DisplayDone:
	}
	return false
}

// keepTarget falls back to the selected server when the target is gone.
func (m *model) keepTarget() {
	t := m.env.Target
	switch t.Kind {
	case ui.SelectGroup:
		if len(m.members(t.Name)) > 0 {
			return
		}
	case ui.SelectServer:
		if m.hasServer(t.Name) {
			return
		}
	case ui.SelectContainer, ui.SelectProject, ui.SelectPort:
		return
	case ui.SelectNone:
	}
	m.env.Target = m.env.Selected
}

// animating reports whether a spinner is on screen: a server starting or
// stopping, or an action in progress.
func (m *model) animating() bool {
	if len(m.env.Pending) > 0 || containers.Animating(m.env.State) {
		return true
	}
	if m.env.State == nil {
		return false
	}
	for _, v := range m.env.State.Servers {
		switch v.Display() {
		case supervisor.Display(supervisor.Starting), supervisor.Display(supervisor.Stopping):
			return true
		case supervisor.DisplayExternal, supervisor.DisplayUnready, supervisor.DisplayDone:
		}
	}
	return false
}

// spin starts the spinner timer when something animates and none runs.
func (m *model) spin() tea.Cmd {
	if m.spinning || !m.animating() {
		return nil
	}
	m.spinning = true
	return tea.Tick(spinnerInterval, func(time.Time) tea.Msg { return spinnerTickMsg{} })
}

// clock redraws once a second while disconnected, for the countdown to the
// next attempt.
func (m *model) clock() tea.Cmd {
	if m.clocking || m.env.Connected {
		return nil
	}
	m.clocking = true
	return tea.Tick(time.Second, func(time.Time) tea.Msg { return clockTickMsg{} })
}

// show puts a notice in the footer for d and in the history.
func (m *model) show(n ui.NoticeMsg, d time.Duration) tea.Cmd {
	m.remember(n)
	return m.flash(n, d)
}

func (m *model) remember(n ui.NoticeMsg) { m.rememberAbout(n, ui.Selection{}) }

// rememberAbout keeps a message about an item in the history.
func (m *model) rememberAbout(n ui.NoticeMsg, item ui.Selection) {
	m.history = append(m.history, historyEntry{At: m.env.Now(), NoticeMsg: n, Item: item})
	if len(m.history) > historyLimit {
		m.history = m.history[len(m.history)-historyLimit:]
	}
}

// flash puts a notice in the footer for d without keeping it.
func (m *model) flash(n ui.NoticeMsg, d time.Duration) tea.Cmd {
	m.noticeSeq++
	seq := m.noticeSeq
	m.notice = &notice{NoticeMsg: n, seq: seq}
	return tea.Tick(d, func(time.Time) tea.Msg { return noticeTimeoutMsg{seq: seq} })
}

// key handles a key press: ctrl+c always quits, even over a dialog or in
// text input; the dialog, then an open
// overlay or the command line take every key, modes other than normal pass
// keys to the focused pane, and in normal mode the key runs its action in
// the focused pane's scope or the global one.
func (m *model) key(msg tea.KeyPressMsg) tea.Cmd {
	k := msg.String()
	switch {
	case k == "ctrl+c":
		return tea.Quit
	case m.confirm != nil:
		return m.answer(k)
	case m.edit.Open():
		return m.editKey(msg)
	case m.sys.Open():
		return m.sys.Key(&m.env, msg)
	case m.help != nil:
		m.scrollKey(k)
		return nil
	case m.messages != nil:
		return m.messagesKey(k)
	case m.menu != nil:
		return m.menuKey(k)
	case m.palette != nil:
		return m.paletteKey(msg)
	case m.mode == ui.ModeCommand && m.cmdLine != nil:
		return m.cmdKey(msg)
	case m.mode != ui.ModeNormal && m.modeOwner >= 0:
		return m.update(m.modeOwner, msg)
	}
	if k == "esc" {
		m.notice = nil
	}
	a, ok := m.env.Keys.Lookup(k, m.focused().Scope(), keymap.ScopeGlobal)
	if !ok {
		if k == "esc" {
			return nil
		}
		return m.flash(ui.NoticeMsg{Text: overlay.KeyLabel(k, m.ascii()) + " does nothing here; ? lists the keys", Level: ui.LevelWarn}, unknownTime)
	}
	return m.run(a)
}

// answer handles a key while the confirmation dialog is open: tab, the
// arrows and h/l move between Cancel and the confirmation, enter runs the
// selected one, y confirms, n, q and esc cancel.
func (m *model) answer(k string) tea.Cmd {
	switch k {
	case "tab", "shift+tab", "left", "right", "h", "l":
		m.dialog.Toggle()
	case "enter", "space":
		return m.decide(m.dialog.Choice == 1)
	case "y", "Y":
		return m.decide(true)
	case "n", "N", "esc", "q":
		return m.decide(false)
	}
	return nil
}

// decide closes the dialog and runs its confirmation when yes.
func (m *model) decide(yes bool) tea.Cmd {
	c := m.confirm
	m.confirm, m.dialog = nil, nil
	if yes {
		return c.Yes
	}
	return nil
}

// globals are the global actions the root model carries out, by id.
// Every global action of the registry has one (model_test.go checks), and
// the pane scopes' actions run in panes (paneActions) or on a target
// (targetActions).
func (m *model) globals() map[string]func() tea.Cmd {
	main := func(id string) func() tea.Cmd {
		// The main panel's keys work from every pane, as in lazygit.
		return func() tea.Cmd {
			a, _ := m.env.Keys.ByID(id)
			return m.do(len(m.left), a)
		}
	}
	none := func(f func()) func() tea.Cmd { return func() tea.Cmd { f(); return nil } }
	g := map[string]func() tea.Cmd{
		"quit":     func() tea.Cmd { return tea.Quit },
		"help":     none(m.openHelp),
		"menu":     m.openMenuAtCursor,
		"command":  none(m.openCmdLine),
		"palette":  none(m.openPalette),
		"messages": none(m.openMessages),
		"filter-state": func() tea.Cmd {
			return m.filterState("")
		},
		"focus-next": none(func() { m.focusPane((m.focus + 1) % len(m.panes())) }),
		"focus-prev": none(func() {
			n := len(m.panes())
			m.focusPane((m.focus + n - 1) % n)
		}),
		"focus-left":       none(func() { m.focusPane(m.leftActive) }),
		"focus-servers":    func() tea.Cmd { return m.focusScope(keymap.ScopeServers) },
		"focus-containers": func() tea.Cmd { return m.focusScope(keymap.ScopeContainers) },
		"focus-ports":      m.focusPorts,
		"focus-right":      none(func() { m.focusPane(len(m.left)) }),
		"focus-main":       none(func() { m.focusPane(len(m.left)) }),
		"screen-next":      func() tea.Cmd { return m.setScreen((m.screenMode + 1) % screenModes) },
		"screen-prev":      func() tea.Cmd { return m.setScreen((m.screenMode + screenModes - 1) % screenModes) },
		"reconnect":        none(m.stream.Reconnect),
		"theme":            m.cycleTheme,
		"icons":            m.cycleIcons,
		"ports":            m.togglePorts,
		"start-docker":     func() tea.Cmd { return containers.StartEngine(m.env.Client) },
	}
	maps.Copy(g, m.formGlobals())
	maps.Copy(g, m.systemGlobals())
	for _, id := range []string{"tab-next", "tab-prev", "scroll-down", "scroll-up", "half-down", "half-up", "page-down", "page-up"} {
		g[id] = main(id)
	}
	return g
}

// run carries out an action: a server action on the cursor's target, a
// global one here, the rest in the focused pane. The focused list's cursor
// is made the target first, so the action takes the row it shows.
func (m *model) run(a keymap.Action) tea.Cmd {
	if sync := m.followSelection(); sync != nil {
		return tea.Batch(sync, m.run(a))
	}
	if a.Scope == keymap.ScopeServers && targetActions[a.ID] {
		return m.act(a.ID, m.cursorTarget())
	}
	if a.Scope == keymap.ScopeServers && formActions[a.ID] {
		return m.formAction(a.ID, "")
	}
	if a.Scope != keymap.ScopeGlobal {
		return m.do(m.focus, a)
	}
	if f, ok := m.globals()[a.ID]; ok {
		return f()
	}
	return nil
}

// focusScope focuses the left-column pane of a scope, or says it is not
// shown.
func (m *model) focusScope(sc keymap.Scope) tea.Cmd {
	for i, p := range m.left {
		if p.Scope() == sc {
			m.focusPane(i)
			return nil
		}
	}
	what := map[keymap.Scope]string{
		keymap.ScopeContainers: "no containers pane: it shows while Docker reports containers",
		keymap.ScopePorts:      "no ports pane is open",
	}[sc]
	return m.flash(ui.NoticeMsg{Text: what, Level: ui.LevelInfo}, unknownTime)
}

// focusPane focuses the pane at index i (into panes()); a left-column
// pane becomes the one that keeps its full height.
func (m *model) focusPane(i int) {
	m.focus = i
	if i < len(m.left) {
		m.leftActive = i
	}
	m.layout()
}

// setScreen switches the screen mode and says which one it is.
func (m *model) setScreen(s screenMode) tea.Cmd {
	m.screenMode = s
	m.layout()
	return m.flash(ui.NoticeMsg{Text: "screen " + s.String(), Level: ui.LevelInfo}, unknownTime)
}

// cycleTheme switches to the next theme family and saves it.
func (m *model) cycleTheme() tea.Cmd {
	families := theme.Families()
	next := families[0]
	for i, f := range families {
		if f.ID == m.family.ID {
			next = families[(i+1)%len(families)]
		}
	}
	m.family = next
	m.restyle()
	m.prefs.Theme, m.prefs.Variant = next.ID, m.variant
	flash := m.flash(ui.NoticeMsg{Text: "theme " + next.Label, Level: ui.LevelInfo}, noticeTime)
	return tea.Batch(flash, m.savePrefs())
}

// cycleIcons switches to the next glyph set and saves it.
func (m *model) cycleIcons() tea.Cmd {
	sets := theme.GlyphSets()
	next := sets[0]
	for i, g := range sets {
		if g.ID == m.env.Glyphs.ID {
			next = sets[(i+1)%len(sets)]
		}
	}
	m.env.Glyphs = next
	m.prefs.Icons = next.ID
	flash := m.flash(ui.NoticeMsg{Text: "icons " + next.ID, Level: ui.LevelInfo}, noticeTime)
	return tea.Batch(flash, m.savePrefs())
}

// savePrefs writes the preferences; a failure becomes an error notice.
func (m *model) savePrefs() tea.Cmd {
	if err := m.prefs.save(); err != nil {
		return m.show(ui.NoticeMsg{Text: "saving preferences: " + err.Error(), Level: ui.LevelError}, noticeTime)
	}
	return nil
}
