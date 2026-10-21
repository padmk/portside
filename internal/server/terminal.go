package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/coder/websocket"
	"github.com/padmk/portside/internal/supervisor"
)

// ── logs and terminal ────────────────────────────────────────────────────

func (s *Server) logs(w http.ResponseWriter, r *http.Request) {
	lb, ok := s.sup.Logs(r.PathValue("name"))
	if !ok {
		fail(w, http.StatusNotFound, errors.New("unknown server"))
		return
	}
	if r.URL.Query().Get("raw") == "1" {
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write(lb.Raw())
		return
	}
	q, err := parseLogQuery(r)
	if err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	writeLines(w, q, lb.TimedLines())
}

func (s *Server) clearLogs(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.sup.Logs(r.PathValue("name")); !ok {
		fail(w, http.StatusNotFound, errors.New("unknown server"))
		return
	}
	s.sup.ClearLogs(r.PathValue("name"))
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

type termMsg struct {
	T    string `json:"t"` // "i" input, "r" resize
	D    string `json:"d,omitempty"`
	Cols uint16 `json:"c,omitempty"`
	Rows uint16 `json:"r,omitempty"`
}

// terminal streams a server's raw output as binary messages (scrollback
// first) and accepts keystrokes and resize requests as JSON text messages.
func (s *Server) terminal(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	lb, ok := s.sup.Logs(name)
	if !ok {
		fail(w, http.StatusNotFound, errors.New("unknown server"))
		return
	}
	readOnly := r.URL.Query().Get("readonly") == "1"
	streamBuffer(w, r, lb, func(m termMsg) {
		switch m.T {
		case "i":
			if !readOnly {
				_ = s.sup.Input(name, []byte(m.D))
			}
		case "r":
			s.sup.Resize(name, m.Cols, m.Rows)
		}
	})
}

// streamBuffer sends a log buffer over a WebSocket: the scrollback, then
// live output, coalescing bursts. Text messages from the client go to input.
func streamBuffer(w http.ResponseWriter, r *http.Request, lb *supervisor.LogBuffer, input func(termMsg)) {
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return
	}
	defer c.CloseNow()
	c.SetReadLimit(1 << 16)
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	backlog, ch := lb.Subscribe()
	defer lb.Unsubscribe(ch)
	// With always=1 the scrollback is sent even when empty, so a client can
	// tell it apart from live output.
	if r.URL.Query().Get("tail") != "0" && (len(backlog) > 0 || r.URL.Query().Get("always") == "1") {
		if err := c.Write(ctx, websocket.MessageBinary, backlog); err != nil {
			return
		}
	}
	go readTermMsgs(ctx, cancel, c, input)
	for {
		select {
		case <-ctx.Done():
			return
		case chunk := <-ch:
			buf := chunk
		drain:
			for len(buf) < 256<<10 {
				select {
				case more := <-ch:
					buf = append(buf, more...)
				default:
					break drain
				}
			}
			wctx, wcancel := context.WithTimeout(ctx, 10*time.Second)
			err := c.Write(wctx, websocket.MessageBinary, buf)
			wcancel()
			if err != nil {
				return
			}
		}
	}
}

// readTermMsgs decodes client messages until the socket closes, then cancels.
func readTermMsgs(ctx context.Context, cancel context.CancelFunc, c *websocket.Conn, input func(termMsg)) {
	defer cancel()
	for {
		_, data, err := c.Read(ctx)
		if err != nil {
			return
		}
		var m termMsg
		if json.Unmarshal(data, &m) == nil && input != nil {
			input(m)
		}
	}
}
