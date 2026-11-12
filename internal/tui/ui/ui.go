// Package ui holds what every part of the terminal UI shares: the
// environment panes draw and act with, the pane contract and the messages
// that cross between panes and the root model.
package ui

import (
	"context"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/padmk/portside/internal/client"
	"github.com/padmk/portside/internal/server"
	"github.com/padmk/portside/internal/supervisor"
	"github.com/padmk/portside/internal/tui/keymap"
	"github.com/padmk/portside/internal/tui/theme"
)

// Env is the shared state the root model owns and passes to every pane.
// A command that runs later in a goroutine captures what it needs (the
// client, a target, a revision) when it is created and never reads Env.
type Env struct {
	Client *client.Client
	// Context ends with the program; sessions that suspend the UI (attach,
	// a shell) end with it too.
	Context context.Context
	// Session is what the terminal UI targets: this computer or another.
	Session Session
	Styles  theme.Styles
	Glyphs  theme.Glyphs
	Keys    *keymap.Registry
	// State is the daemon's latest snapshot; nil until the first arrives.
	State *server.State
	// Connected is false while the events connection is down; State then
	// holds the last snapshot.
	Connected bool
	// Pending holds the verb of the operation in progress on each item
	// ("restarting"), for its spinner.
	Pending map[Selection]string
	// Selected is the server the right pane shows.
	Selected Selection
	// Target is what the list cursor is on and what actions that take
	// the selection act on: a server, or a group header (the right pane
	// then keeps showing Selected).
	Target Selection
	// Frame advances with the spinner tick, for animated glyphs.
	Frame int
	Now   func() time.Time

	modeReq *Mode
}

// ProgramContext is Context, or a background context while none is set
// (a model built without Run, as in tests).
func (e *Env) ProgramContext() context.Context {
	if e.Context == nil {
		return context.Background()
	}
	return e.Context
}

// EnterMode switches the input mode as part of the pane call that asks
// for it: the root model applies it when that call returns, before any
// command runs, and the calling pane owns the mode (it gets the keys and
// the focus until it leaves the mode).
func (e *Env) EnterMode(m Mode) { e.modeReq = &m }

// TakeMode returns and clears the mode a pane asked for with EnterMode.
func (e *Env) TakeMode() (Mode, bool) {
	if e.modeReq == nil {
		return ModeNormal, false
	}
	m := *e.modeReq
	e.modeReq = nil
	return m, true
}

// Session is the computer the terminal UI acts on.
type Session struct {
	// Computer names the other computer the session targets (--on);
	// empty for this one.
	Computer string
	// Conn is the connection to that computer, which knows how its
	// servers' URLs open from here.
	Conn *client.Connection
}

// Remote reports whether the session targets another computer: apps and
// shells it would open run on the wrong computer.
func (s Session) Remote() bool { return s.Computer != "" }

// URL is how this computer opens a server's URL: unchanged on this
// computer, through the forwarded port or this daemon's proxy for another
// one, and empty when there is no way.
func (s Session) URL(server, raw string) string {
	if raw == "" || !s.Remote() {
		return raw
	}
	if s.Conn == nil {
		return ""
	}
	return s.Conn.LocalURL(server, raw)
}

// PortURL is how this computer opens port of the computer the session
// targets: http://localhost:<port> on this one; on another one the local
// port it is forwarded to, or, when port is the configured port of
// server (serverPort), this daemon's proxy for that server. An auxiliary
// port (a container's, another listener of a server) never falls back to
// the server's proxy, which reaches the server's own port: without a
// forwarded port it has no address here, "".
func (s Session) PortURL(server string, serverPort, port int) string {
	raw := "http://localhost:" + strconv.Itoa(port)
	if port <= 0 || !s.Remote() {
		return raw
	}
	if s.Conn == nil {
		return ""
	}
	if server != "" && port == serverPort {
		return s.Conn.LocalURL(server, raw)
	}
	for _, m := range s.Conn.Mirrors {
		if m.Port == port && m.State == "mirrored" {
			local := port
			if m.Local > 0 {
				local = m.Local
			}
			return "http://localhost:" + strconv.Itoa(local)
		}
	}
	return ""
}

// NoAddress says why port of the targeted computer has no address here
// (PortURL is empty).
func (s Session) NoAddress(port int) string {
	if s.Remote() && s.Conn == nil {
		return "not connected to " + s.Computer
	}
	return "no local address for :" + strconv.Itoa(port) + " on " + s.Computer
}

// SelectionKind is what kind of item is selected.
type SelectionKind int

const (
	SelectNone SelectionKind = iota
	SelectServer
	SelectGroup
	SelectContainer
	SelectPort
)

// Selection names an item of a list by the identity its actions need:
// a server (Name), a group header (Name, "" for the ungrouped servers), a
// container (ID, the engine's id, with Name for messages) or a port
// listener (PID and Port).
type Selection struct {
	Kind SelectionKind
	Name string
	ID   string
	PID  int
	Port int
}

// ServerItem names a server.
func ServerItem(name string) Selection { return Selection{Kind: SelectServer, Name: name} }

// GroupItem names a group's header; "" is the ungrouped servers'.
func GroupItem(group string) Selection { return Selection{Kind: SelectGroup, Name: group} }

// ContainerItem names a container by its engine id.
func ContainerItem(id, name string) Selection {
	return Selection{Kind: SelectContainer, ID: id, Name: name}
}

// PortItem names the process listening on a port.
func PortItem(pid, port int) Selection { return Selection{Kind: SelectPort, PID: pid, Port: port} }

// GroupLabel is how messages name a group.
func GroupLabel(group string) string {
	if group == "" {
		return "the ungrouped servers"
	}
	return "group " + group
}

// Pane is one region of the screen. Panes keep their own scroll and cursor
// state and read everything else from Env.
type Pane interface {
	// Scope is the key scope the pane adds while focused.
	Scope() keymap.Scope
	// Update handles a message that is not a key press already handled
	// as an action (mouse events inside the pane come with coordinates
	// relative to the pane's top left corner).
	Update(env *Env, msg tea.Msg) tea.Cmd
	// Do runs an action of the pane's scope, or a global one the pane
	// carries out (the main panel's scrolling and tabs).
	Do(env *Env, a keymap.Action) tea.Cmd
	// View renders the pane at exactly w x h cells, border included.
	View(env *Env, w, h int, focused bool) string
}

// StateMsg carries a new snapshot from the events connection.
type StateMsg struct{ State *server.State }

// ConnMsg reports the events connection: down with the error and when the
// next attempt is, or up again.
type ConnMsg struct {
	Connected bool
	Err       error
	Retry     time.Time
}

// Level is a notice's severity.
type Level int

const (
	LevelInfo Level = iota
	LevelSuccess
	LevelWarn
	LevelError
)

// NoticeMsg shows a message in the footer for a few seconds and keeps it in
// the message history (`:messages`).
type NoticeMsg struct {
	Text  string
	Level Level
}

// ToastMsg shows a toast at the top right (crashes, unhealthy containers,
// a lost connection). Item, when set, is what the toast is about: a click
// on the toast, or enter on its line in `:messages`, selects it.
type ToastMsg struct {
	Title, Body string
	Level       Level
	Item        Selection
}

// OpDoneMsg reports that the request of operation ID on Items returned;
// Errs holds a message per failed item, Err a failure of the request.
type OpDoneMsg struct {
	ID    int
	Label string // "restart web", "start group shop"
	Items []Selection
	Err   error
	Errs  []string
}

// StateFilterMsg asks the servers list to show only servers in one of
// States (View.Display()), Label naming them; no States clears it.
type StateFilterMsg struct {
	States []supervisor.Display
	Label  string
}

// FocusDetailMsg asks the root model to focus the right pane (enter or a
// double click on a server).
type FocusDetailMsg struct{}

// FocusListMsg asks the root model to focus the servers again (esc in the
// main panel).
type FocusListMsg struct{}

// SelectMsg asks the root model to select a server (a click in a list, a
// command naming a server); it becomes the target too.
type SelectMsg struct{ Item Selection }

// Mode is the input mode the footer shows (design/tui.md "Modes"). Outside
// ModeNormal the root model passes key presses to the focused pane's Update
// instead of the key engine.
type Mode int

const (
	ModeNormal Mode = iota
	ModeFilter
	ModeSearch
	ModeCommand
	ModeInsert
	ModeSelect
)

// String is the mode's footer label.
func (m Mode) String() string {
	return [...]string{"NORMAL", "FILTER", "SEARCH", "COMMAND", "INSERT", "SELECT"}[m]
}

// ModeMsg asks the root model to switch the input mode; a pane sends it to
// take raw keys (e.g. its search input) and to give them back. The pane
// focused when it arrives owns the mode; Env.EnterMode switches at once
// and is preferred.
type ModeMsg struct{ Mode Mode }

// ConfirmMsg asks the root model to show a confirmation dialog; Yes runs
// when the user confirms. The destructive choice is never the default.
type ConfirmMsg struct {
	Title, Body, Confirm string
	Yes                  tea.Cmd
}

// Fit truncates or pads s, which may hold styles, to exactly w cells.
func Fit(s string, w int) string {
	if w <= 0 {
		return ""
	}
	s = lipgloss.NewStyle().MaxWidth(w).Render(s)
	if n := lipgloss.Width(s); n < w {
		s += strings.Repeat(" ", w-n)
	}
	return s
}

// Box draws a pane frame of exactly w x h cells: the glyph set's border
// (heavy for the focused pane without colors, Glyphs.PaneBorder) with the
// title in the top border and right at its right end (a count), the
// focused pane's border in Accent and its title bold, and lines inside with
// one column of padding, cut or padded to fit, on the pane's surface.
func Box(env *Env, w, h int, focused bool, title, right string, lines []string) string {
	return BoxRows(env, w, h, focused, title, right, lines, nil)
}

// BoxRows is Box with the rows in rowFill (by index into lines) drawn on
// that style's background and attributes from border to border, padding
// included: the selected row passes env.Styles.Selected.
func BoxRows(env *Env, w, h int, focused bool, title, right string, lines []string, rowFill map[int]lipgloss.Style) string {
	if w <= 0 || h <= 0 {
		return ""
	}
	s := env.Styles
	if w < 4 || h < 2 {
		row := s.Row("", w)
		out := make([]string, h)
		for i := range out {
			out[i] = row
		}
		return strings.Join(out, "\n")
	}
	b := env.Glyphs.PaneBorder(focused, s.Mono)
	frame, titleStyle := s.Frame, s.Title
	if focused {
		frame, titleStyle = s.FrameFocused, s.TitleFocused
	}
	inner := w - 2
	head := ""
	switch {
	case strings.ContainsRune(title, '\x1b'):
		// A title that carries its own styles (a tab strip) keeps them:
		// styling it again would underline its escape sequences rune by
		// rune in the monochrome styles.
		head = s.Surface.Render(" ") + title + s.Surface.Render(" ")
	case title != "":
		head = s.Surface.Render(" ") + titleStyle.Render(title) + s.Surface.Render(" ")
	}
	tail := ""
	if right != "" {
		tail = s.Surface.Render(" ") + s.Muted.Render(right) + s.Surface.Render(" ")
	}
	if lipgloss.Width(head)+lipgloss.Width(tail) > inner {
		tail = ""
	}
	if lipgloss.Width(head) > inner {
		head = ansi.Truncate(head, inner, "")
	}
	fill := inner - lipgloss.Width(head) - lipgloss.Width(tail)
	out := make([]string, 0, h)
	out = append(out, frame.Render(b.TopLeft)+head+frame.Render(strings.Repeat(b.Top, max(0, fill)))+tail+frame.Render(b.TopRight))
	left, rightSide := frame.Render(b.Left), frame.Render(b.Right)
	for i := 0; i < h-2; i++ {
		line := ""
		if i < len(lines) {
			line = lines[i]
		}
		row := ""
		if style, ok := rowFill[i]; ok {
			row = theme.Fill(style, " "+Fit(line, inner-2)+" ", inner)
		} else {
			row = s.Row(" "+Fit(line, inner-2)+" ", inner)
		}
		out = append(out, left+row+rightSide)
	}
	out = append(out, frame.Render(b.BottomLeft+strings.Repeat(b.Bottom, inner)+b.BottomRight))
	return strings.Join(out, "\n")
}

// BottomBorder is the bottom border of a Box of w cells with status at
// its right end (a position such as "4/25", a filter, the log mode), in
// the same border as Box draws (Glyphs.PaneBorder); without a status, or
// too narrow for one, the plain border.
func BottomBorder(env *Env, w int, focused bool, status string) string {
	s := env.Styles
	if w < 2 {
		return s.Row("", max(0, w))
	}
	frame := s.Frame
	if focused {
		frame = s.FrameFocused
	}
	b := env.Glyphs.PaneBorder(focused, s.Mono)
	inner := w - 2
	text := ""
	if status != "" && w >= 8 {
		status = ansi.Truncate(status, inner-4, env.Glyphs.Ellipsis)
		text = s.Surface.Render(" ") + s.Muted.Render(status) + s.Surface.Render(" ")
	}
	fill := inner - lipgloss.Width(text) - 1
	if text == "" || fill < 1 {
		return frame.Render(b.BottomLeft + strings.Repeat(b.Bottom, inner) + b.BottomRight)
	}
	return frame.Render(b.BottomLeft+strings.Repeat(b.Bottom, fill)) + text + frame.Render(b.Bottom+b.BottomRight)
}

// WithBottom replaces the bottom border of box, a Box of w cells, with
// BottomBorder's for status.
func WithBottom(env *Env, box string, w int, focused bool, status string) string {
	if status == "" {
		return box
	}
	i := strings.LastIndexByte(box, '\n')
	if i < 0 {
		return box
	}
	return box[:i+1] + BottomBorder(env, w, focused, status)
}

// Titled is a pane that can name itself in one row: the root model draws a
// left-column pane that is not the active one as a compact header with its
// title and count (lazygit's collapsed side panels).
type Titled interface {
	Title(env *Env) (title, count string)
}

// JumpKey is the key that focuses the pane of scope (focus-<scope>), as
// its title and its collapsed header show it; "" when it has none.
func JumpKey(env *Env, scope keymap.Scope) string {
	if env.Keys == nil {
		return ""
	}
	if a, ok := env.Keys.ByID("focus-" + string(scope)); ok && len(a.Keys) > 0 {
		return a.Keys[0]
	}
	return ""
}

// PaneTitle is a left-column pane's title for Box: its jump key in Key and
// the title in the frame's title style, so the active pane names the key
// its collapsed header shows.
func PaneTitle(env *Env, scope keymap.Scope, title string, focused bool) string {
	s := env.Styles
	st := s.Title
	if focused {
		st = s.TitleFocused
	}
	key := JumpKey(env, scope)
	if key == "" {
		return st.Render(title)
	}
	return s.Key.Render(key) + s.Surface.Render(" ") + st.Render(title)
}

// HeaderRow is the compact header of a collapsed pane, exactly w cells:
// the pane's key and title, a rule and the count at the right end.
func HeaderRow(env *Env, w int, key, title, count string) string {
	s := env.Styles
	if w <= 0 {
		return ""
	}
	rule := env.Glyphs.Border.Top
	head := s.Frame.Render(rule) + s.Surface.Render(" ") + s.Key.Render(key) + s.Surface.Render(" ") + s.Title.Render(title) + s.Surface.Render(" ")
	tail := ""
	if count != "" {
		tail = s.Surface.Render(" ") + s.Muted.Render(count) + s.Surface.Render(" ")
	}
	gap := w - lipgloss.Width(head) - lipgloss.Width(tail) - 1
	if gap < 1 {
		return s.Row(head, w)
	}
	return s.Row(head+s.Frame.Render(strings.Repeat(rule, gap))+tail+s.Frame.Render(rule), w)
}

// LogsClearedMsg reports that a server's scrollback was cleared, or the
// error; the log pane showing that server empties itself.
type LogsClearedMsg struct {
	Server string
	Err    error
}

// RunMsg asks the root model to run a server action on an item, as its
// key would, with the same checks (a restart offered after an edit).
type RunMsg struct {
	Action string
	Item   Selection
}

// SelectProject is a Compose project's header in the containers list;
// Name is the project. It follows the other kinds so their values stay.
const SelectProject SelectionKind = SelectPort + 1

// ProjectItem names a Compose project's header.
func ProjectItem(project string) Selection { return Selection{Kind: SelectProject, Name: project} }

// ActMsg asks the root model to run the target action ID on Item, as a
// key, the menu or the command line would: a pane whose actions need the
// root's operation tracking (containers, ports) sends it with the item
// its cursor was on. Confirmed is set by the confirmation dialog of an
// action that asks first, which then runs without asking again.
type ActMsg struct {
	ID        string
	Item      Selection
	Confirmed bool
}

// HidePaneMsg asks the root model to close an on-demand left-column pane
// (the ports pane after esc).
type HidePaneMsg struct{ Scope keymap.Scope }

// GenMsg is a message of session Gen: the live stream's snapshots and the
// replies of commands started while that session was current. The root
// model drops the ones of an earlier session (after :on switched the
// computer) and handles the others as Msg.
type GenMsg struct {
	Gen int
	Msg tea.Msg
}
