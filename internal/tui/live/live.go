// Package live streams daemon snapshots to the terminal UI.
package live

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/coder/websocket"
	"github.com/padmk/portside/internal/client"
	"github.com/padmk/portside/internal/server"
	"github.com/padmk/portside/internal/tui/ui"
)

// snapshotLimit is the largest state snapshot read.
const snapshotLimit = 64 << 20

// Stream owns a reconnect signal for a live state stream.
type Stream struct{ reconnect chan struct{} }

func New() *Stream { return &Stream{reconnect: make(chan struct{}, 1)} }

// Reconnect interrupts a connection or its retry delay.
func (s *Stream) Reconnect() {
	select {
	case s.reconnect <- struct{}{}:
	default:
	}
}

// Run starts a stream without an external reconnect trigger.
func Run(ctx context.Context, c *client.Client, send func(tea.Msg)) {
	New().Run(ctx, c, send)
}

// Run reads snapshots until the context is cancelled and returns only
// when every goroutine it started has ended.
func (s *Stream) Run(ctx context.Context, c *client.Client, send func(tea.Msg)) {
	delay := time.Second
	for ctx.Err() == nil {
		attempt, cancel := context.WithCancel(ctx)
		finished := make(chan struct{})
		watched := make(chan struct{})
		go func() {
			defer close(watched)
			// A panic here ends the attempt; the root quits on it.
			defer ui.Recover(send)
			defer cancel()
			select {
			case <-s.reconnect:
				cancel()
			case <-finished:
			case <-ctx.Done():
			}
		}()
		conn, err := c.DialWebSocket(attempt, "/api/events")
		if err == nil {
			// A snapshot holds every server with its usage history and log
			// tail, far over the library's 32 KiB default.
			conn.SetReadLimit(snapshotLimit)
			send(ui.ConnMsg{Connected: true})
			for {
				var kind websocket.MessageType
				var data []byte
				kind, data, err = conn.Read(attempt)
				if err != nil {
					break
				}
				if kind != websocket.MessageText {
					continue
				}
				var state server.State
				if err = json.Unmarshal(data, &state); err != nil {
					break
				}
				delay = time.Second
				send(ui.StateMsg{State: &state})
			}
			conn.CloseNow()
		}
		close(finished)
		cancel()
		<-watched
		if ctx.Err() != nil {
			return
		}
		if errors.Is(err, context.Canceled) {
			continue
		}
		send(ui.ConnMsg{Err: err, Retry: time.Now().Add(delay)})
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-s.reconnect:
			timer.Stop()
		case <-timer.C:
			delay *= 2
			if delay > 10*time.Second {
				delay = 10 * time.Second
			}
		}
	}
}
