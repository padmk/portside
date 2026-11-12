package remote

import (
	"context"
	"net/http"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/padmk/portside/internal/agentsetup"
	"github.com/padmk/portside/internal/client"
	"github.com/padmk/portside/internal/remote"
	"github.com/padmk/portside/internal/server"
	"github.com/padmk/portside/internal/share"
)

// requestTimeout bounds the requests of the overlays; a scan of the
// tailnet takes up to 8 seconds on the daemon's side.
const requestTimeout = 20 * time.Second

// Discovery is GET /api/remote/peers: the tailnet and the computers on it
// that share their daemon.
type Discovery struct {
	remote.Discovery
	Prompt string `json:"prompt"`
}

// Replies of the requests, each with the overlay's sequence number so a
// reply to a closed or reopened overlay is dropped.
type (
	overviewMsg struct {
		seq int
		ov  server.RemoteOverview
		err error
	}
	peersMsg struct {
		seq  int
		disc Discovery
		err  error
	}
	// doneMsg is a computers action's reply: what it was ("connect
	// studio") and the overview after it, when the reply carries one.
	doneMsg struct {
		seq  int
		what string
		err  error
		// info is the connection after connect, pair and forwarding.
		info *remote.Info
		// ov is the overview after a change of sharing.
		ov *server.RemoteOverview
		// code is a new pairing code of this computer.
		code *share.Code
		// switchTo asks for the session to follow the connection.
		switchTo string
	}
	agentsMsg struct {
		seq   int
		items []agentsetup.Item
		err   error
	}
	appliedMsg struct {
		seq     int
		results []agentsetup.Result
		err     error
	}
)

func overview(ctx context.Context, c *client.Client) (server.RemoteOverview, error) {
	var ov server.RemoteOverview
	err := c.Do(ctx, http.MethodGet, "/api/remote", nil, &ov)
	return ov, err
}

func fetchOverview(c *client.Client, seq int) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
		defer cancel()
		ov, err := overview(ctx, c)
		return overviewMsg{seq: seq, ov: ov, err: err}
	}
}

func fetchPeers(c *client.Client, seq int) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
		defer cancel()
		var d Discovery
		err := c.Do(ctx, http.MethodGet, "/api/remote/peers", nil, &d)
		return peersMsg{seq: seq, disc: d, err: err}
	}
}

// call sends a computers action and reports it as a doneMsg; into, when
// set, decodes the reply (*remote.Info, *server.RemoteOverview or
// *share.Code).
func call(c *client.Client, seq int, what, method, path string, body any, into any, timeout time.Duration) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		err := c.Do(ctx, method, path, body, into)
		d := doneMsg{seq: seq, what: what, err: err}
		if err == nil {
			switch v := into.(type) {
			case *remote.Info:
				d.info = v
			case *server.RemoteOverview:
				d.ov = v
			case *share.Code:
				d.code = v
			}
		}
		return d
	}
}

func fetchAgents(c *client.Client, seq int) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
		defer cancel()
		var v struct {
			Items []agentsetup.Item `json:"items"`
		}
		err := c.Do(ctx, http.MethodGet, "/api/agents", nil, &v)
		return agentsMsg{seq: seq, items: v.Items, err: err}
	}
}

func applyAgents(c *client.Client, seq int, ids []string) tea.Cmd {
	return func() tea.Msg {
		// The daemon bounds the agents' CLIs at two minutes.
		ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
		defer cancel()
		var results []agentsetup.Result
		err := c.Do(ctx, http.MethodPost, "/api/agents/apply", map[string][]string{"ids": ids}, &results)
		return appliedMsg{seq: seq, results: results, err: err}
	}
}
