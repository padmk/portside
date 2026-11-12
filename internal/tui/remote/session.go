// Package remote is the terminal UI's view of the computers around the
// daemon and of the daemon itself: the Computers overlay (other computers
// over Tailscale, sharing, forwarded ports) and the switch of the session
// to another computer, the daemon's details with its upgrade and restart in
// place, and the coding-agent setup (design/tui.md "Other computers and
// the daemon").
package remote

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/padmk/portside/internal/client"
	"github.com/padmk/portside/internal/tui/live"
	"github.com/padmk/portside/internal/tui/ui"
)

// LocalName is the computer name that `:on` takes for this computer.
const LocalName = "local"

// switchTimeout bounds connecting to another computer.
const switchTimeout = 45 * time.Second

// cmdType is tea.Cmd's type, for the batch and sequence messages whose
// commands Tag tags one by one.
var cmdType = reflect.TypeFor[tea.Cmd]()

// Tag makes the message of cmd a ui.GenMsg of session gen, so the root
// model drops it once another session is current. Bubble Tea's own
// messages (quit, window title) pass unchanged, and the commands of a
// batch or a sequence are tagged one by one, so the program still runs
// them. A ui.ExecMsg becomes Bubble Tea's exec, whose callback result is
// tagged the same way when it arrives, after the UI comes back; a session
// that ended with ui.ErrEnding quits instead. Bubble Tea's own exec
// message passes untagged: suspend the UI through ui.Exec.
func Tag(gen int, cmd tea.Cmd) tea.Cmd {
	if cmd == nil {
		return nil
	}
	return func() tea.Msg {
		msg := cmd()
		if msg == nil {
			return nil
		}
		if e, ok := msg.(ui.ExecMsg); ok {
			return tea.Exec(e.Cmd, execDone(gen, e.Done))()
		}
		v := reflect.ValueOf(msg)
		t := v.Type()
		if t.Kind() == reflect.Slice && t.Elem() == cmdType {
			out := reflect.MakeSlice(t, v.Len(), v.Len())
			for i := range v.Len() {
				c, _ := v.Index(i).Interface().(tea.Cmd)
				out.Index(i).Set(reflect.ValueOf(Tag(gen, c)))
			}
			return out.Interface()
		}
		for t.Kind() == reflect.Pointer {
			t = t.Elem()
		}
		if strings.HasPrefix(t.PkgPath(), "charm.land/bubbletea/") {
			return msg
		}
		if _, ok := msg.(ui.GenMsg); ok {
			return msg
		}
		return ui.GenMsg{Gen: gen, Msg: msg}
	}
}

// execDone is the callback of a suspended session of session gen: done's
// message tagged with gen, or quit when the session ended because the
// program ends.
func execDone(gen int, done tea.ExecCallback) tea.ExecCallback {
	return func(err error) tea.Msg {
		if errors.Is(err, ui.ErrEnding) {
			return tea.QuitMsg{}
		}
		if p, ok := errors.AsType[ui.PanicMsg](err); ok {
			return p
		}
		if done == nil {
			return nil
		}
		out := done(err)
		return Tag(gen, func() tea.Msg { return out })()
	}
}

// Streams runs the live state stream of the current session: Start stops
// the previous session's stream and starts one for the new client, whose
// messages carry the session's generation.
type Streams struct {
	ctx    context.Context
	send   func(tea.Msg)
	wg     sync.WaitGroup
	mu     sync.Mutex
	cancel context.CancelFunc
}

// NewStreams returns the streams of a program: they end with ctx and
// deliver their messages with send.
func NewStreams(ctx context.Context, send func(tea.Msg)) *Streams {
	return &Streams{ctx: ctx, send: send}
}

// Start stops the running stream and runs s for client c as session gen.
func (st *Streams) Start(s *live.Stream, c *client.Client, gen int) {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.cancel != nil {
		st.cancel()
	}
	ctx, cancel := context.WithCancel(st.ctx)
	st.cancel = cancel
	st.wg.Go(func() {
		defer ui.Recover(st.send)
		s.Run(ctx, c, func(msg tea.Msg) { st.send(ui.GenMsg{Gen: gen, Msg: msg}) })
	})
}

// Wait returns when every stream has ended (the program's context ended).
func (st *Streams) Wait() { st.wg.Wait() }

// SwitchedMsg is the outcome of :on: the client and connection of the
// computer the session now targets (Computer "" for this one), or why the
// switch did not happen.
type SwitchedMsg struct {
	Computer string
	Client   *client.Client
	Conn     *client.Connection
	Err      error
	// Pair is set when the computer needs pairing first.
	Pair Peer
}

// Peer names another computer by what the API takes.
type Peer struct {
	Name, DNSName string
}

// IsLocal reports whether a name given to :on means this computer.
func IsLocal(name string) bool {
	return name == "" || strings.EqualFold(name, LocalName)
}

// Switch connects this daemon to the computer called name, unless it is
// connected to it already, and returns a client whose requests go there,
// as --on does; IsLocal names return local itself.
func Switch(local *client.Client, name string) tea.Cmd {
	return func() tea.Msg {
		if IsLocal(name) {
			return SwitchedMsg{Client: local}
		}
		ctx, cancel := context.WithTimeout(context.Background(), switchTimeout)
		defer cancel()
		ov, err := overview(ctx, local)
		if err != nil {
			return SwitchedMsg{Computer: name, Err: err}
		}
		if ov.Connection == nil || !connIs(ov.Connection.Peer.Name, ov.Connection.Peer.Label, ov.Connection.Peer.DNSName, name) {
			err := local.Do(ctx, http.MethodPost, "/api/remote/connect", map[string]string{"peer": name}, nil)
			if apiErr, ok := errors.AsType[*client.APIError](err); ok && apiErr.Code == "pairing_required" {
				return SwitchedMsg{Computer: name, Err: err, Pair: Peer{Name: name, DNSName: name}}
			}
			if err != nil {
				return SwitchedMsg{Computer: name, Err: err}
			}
		}
		on, cn, err := local.On(ctx, name)
		if err != nil {
			return SwitchedMsg{Computer: name, Err: err}
		}
		return SwitchedMsg{Computer: cn.Peer.Name, Client: on, Conn: cn}
	}
}

// connIs reports whether name is one of a computer's names, as
// client.Connection.Is compares them.
func connIs(peerName, label, dns, name string) bool {
	name = strings.TrimSuffix(strings.TrimSpace(name), ".")
	return strings.EqualFold(name, peerName) || strings.EqualFold(name, label) || strings.EqualFold(name, dns)
}
