package remote

import (
	"context"
	"encoding/json"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/padmk/portside/internal/client"
	"github.com/padmk/portside/internal/remote"
	"github.com/padmk/portside/internal/server"
	"github.com/padmk/portside/internal/tui/ui"
)

// connRefresh is how often a session on another computer reads this
// daemon's connection to it again: its forwarded ports change with
// forwarding turned on or off here or in another client, and with ports
// taken on this computer.
const connRefresh = 5 * time.Second

type (
	connTickMsg struct{ seq int }
	connMsg     struct {
		seq int
		ov  server.RemoteOverview
		err error
	}
)

// WatchConnection keeps the session's connection (ui.Session.Conn, which
// resolves the other computer's addresses here) in line with this
// daemon's while the session targets another computer: it reads it now
// and every connRefresh. A later call replaces the loop; the root calls
// it whenever the session changes.
func (h *Host) WatchConnection(env *ui.Env) tea.Cmd {
	h.connSeq++
	if !env.Session.Remote() {
		return nil
	}
	return fetchConn(h.Local, h.connSeq)
}

func fetchConn(c *client.Client, seq int) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
		defer cancel()
		ov, err := overview(ctx, c)
		return connMsg{seq: seq, ov: ov, err: err}
	}
}

// updateConn takes the loop's messages; ok is false for the others.
func (h *Host) updateConn(env *ui.Env, msg tea.Msg) (tea.Cmd, bool) {
	switch msg := msg.(type) {
	case connTickMsg:
		if msg.seq != h.connSeq || !env.Session.Remote() {
			return nil, true
		}
		return fetchConn(h.Local, msg.seq), true
	case connMsg:
		if msg.seq != h.connSeq || !env.Session.Remote() {
			return nil, true
		}
		// A failed read keeps the connection known last.
		if msg.err == nil {
			SyncConn(env, msg.ov.Connection)
		}
		seq := msg.seq
		return tea.Tick(connRefresh, func(time.Time) tea.Msg { return connTickMsg{seq} }), true
	}
	return nil, false
}

// SyncConn makes the session's connection this daemon's connection info
// while the session targets the computer it is to; another computer's or
// none leaves the session without one, so its addresses do not resolve
// (ui.Session.URL and PortURL are empty) instead of reaching whatever now
// holds a port that was forwarded. A session on this computer has none.
func SyncConn(env *ui.Env, info *remote.Info) {
	if !env.Session.Remote() {
		return
	}
	cn := clientConn(info)
	if cn == nil || !cn.Is(env.Session.Computer) {
		env.Session.Conn = nil
		return
	}
	env.Session.Conn = cn
}

// clientConn is the client's view of a connection, decoded from the same
// JSON the API sends (client.Client.On reads it so), nil for none.
func clientConn(info *remote.Info) *client.Connection {
	if info == nil {
		return nil
	}
	b, err := json.Marshal(info)
	if err != nil {
		return nil
	}
	var cn client.Connection
	if json.Unmarshal(b, &cn) != nil {
		return nil
	}
	return &cn
}
