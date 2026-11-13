package remote

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/coder/websocket"

	"github.com/padmk/portside/internal/app"
	"github.com/padmk/portside/internal/client"
	"github.com/padmk/portside/internal/server"
	"github.com/padmk/portside/internal/signing"
	"github.com/padmk/portside/internal/tui/live"
	"github.com/padmk/portside/internal/tui/ui"
)

// eventsDaemon serves /api/events as the daemon does: snapshots written
// with a write timeout, after which it drops the client. It counts the
// snapshots written and the writes that timed out.
func eventsDaemon(t *testing.T, snapshots int, size int) (c *client.Client, written, failed *atomic.Int32) {
	t.Helper()
	state := t.TempDir()
	t.Setenv(app.Env("STATE_DIR"), state)
	token := signing.RandomHex(32)
	if err := os.WriteFile(filepath.Join(state, "auth-token"), []byte(token+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	epoch := signing.NewEpoch()
	written, failed = new(atomic.Int32), new(atomic.Int32)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/health":
			body, _ := json.Marshal(client.Health{App: app.ID, Version: "test", PID: 1})
			signing.ProveReply(w.Header(), r.Header, token, epoch, http.StatusOK, body)
			_, _ = w.Write(body)
		case "/api/events":
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
			pad := strings.Repeat("x", size)
			for i := range snapshots {
				data, _ := json.Marshal(server.State{Groups: []string{strconv.Itoa(i), pad}})
				ctx, cancel := context.WithTimeout(r.Context(), 500*time.Millisecond)
				err := ws.Write(ctx, websocket.MessageText, data)
				cancel()
				if err != nil {
					failed.Add(1)
					return
				}
				written.Add(1)
			}
			<-r.Context().Done()
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	_, port, _ := net.SplitHostPort(srv.Listener.Addr().String())
	n, _ := strconv.Atoi(port)
	return client.New(n), written, failed
}

// While the program reads no messages (the UI suspended for an attached
// terminal), the stream keeps reading, so the daemon does not drop it,
// and the program gets the latest snapshot once it reads again.
func TestStreamKeepsReadingWhileTheProgramIsSuspended(t *testing.T) {
	const snapshots = 40
	c, written, failed := eventsDaemon(t, snapshots, 256<<10)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	resume := make(chan struct{})
	got := make(chan tea.Msg, 100)
	st := NewStreams(ctx, func(msg tea.Msg) {
		select {
		case <-resume:
		case <-ctx.Done():
			return
		}
		got <- msg
	})
	st.Start(live.New(), c, 3)
	deadline := time.Now().Add(20 * time.Second)
	for written.Load()+failed.Load() < snapshots && failed.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if failed.Load() != 0 || written.Load() != snapshots {
		t.Fatalf("the daemon wrote %d snapshots and timed out %d times", written.Load(), failed.Load())
	}
	close(resume)
	var last *server.State
	timeout := time.After(5 * time.Second)
	for last == nil || last.Groups[0] != strconv.Itoa(snapshots-1) {
		select {
		case msg := <-got:
			g, ok := msg.(ui.GenMsg)
			if !ok || g.Gen != 3 {
				t.Fatalf("message %#v", msg)
			}
			switch m := g.Msg.(type) {
			case ui.StateMsg:
				last = m.State
			case ui.ConnMsg:
				if m.Err != nil {
					t.Fatalf("connection lost: %v", m.Err)
				}
			}
		case <-timeout:
			t.Fatalf("no latest snapshot; last %v", last != nil)
		}
	}
	cancel()
	st.Wait()
}

func TestMailboxKeepsTheLatestOfEachKind(t *testing.T) {
	b := mailbox{ready: make(chan struct{}, 1)}
	s := func(gen int, group string) tea.Msg {
		return ui.GenMsg{Gen: gen, Msg: ui.StateMsg{State: &server.State{Groups: []string{group}}}}
	}
	b.put(s(1, "a"))
	b.put(ui.GenMsg{Gen: 1, Msg: ui.ConnMsg{Connected: true}})
	b.put(s(1, "b"))
	b.put(s(2, "c"))
	b.put(ui.PanicMsg{Value: "x"})
	b.put(s(1, "d"))
	var order []string
	for _, msg := range b.queue {
		switch m := msg.(type) {
		case ui.GenMsg:
			if st, ok := m.Msg.(ui.StateMsg); ok {
				order = append(order, strconv.Itoa(m.Gen)+st.State.Groups[0])
			} else {
				order = append(order, strconv.Itoa(m.Gen)+"conn")
			}
		case ui.PanicMsg:
			order = append(order, "panic")
		}
	}
	if got, want := strings.Join(order, " "), "1conn 2c panic 1d"; got != want {
		t.Errorf("queue = %s, want %s", got, want)
	}
}
