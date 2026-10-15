package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/url"
	"os"
	"os/signal"
	"syscall"

	"github.com/coder/websocket"
	"golang.org/x/term"
)

const (
	red    = "31"
	green  = "32"
	yellow = "33"
	blue   = "34"
	dim    = "2"
	bold   = "1"
)

func colorEnabled() bool {
	if os.Getenv("NO_COLOR") != "" || os.Getenv("TERM") == "dumb" {
		return false
	}
	return isTerminal(os.Stdout)
}

func isTerminal(f *os.File) bool { return term.IsTerminal(int(f.Fd())) }

func (e *env) paint(code, s string) string {
	if !e.color || s == "" {
		return s
	}
	return "\x1b[" + code + "m" + s + "\x1b[0m"
}

// cmdAttach connects the local terminal to a server's pseudo-terminal.
func cmdAttach(e *env, args []string) error {
	fs := flag.NewFlagSet("attach", flag.ContinueOnError)
	names, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(names) != 1 {
		return usageError{"attach takes exactly one server name"}
	}
	if !isTerminal(os.Stdin) {
		return fmt.Errorf("attach needs an interactive terminal; use `logs -f` instead")
	}
	c, err := e.client()
	if err != nil {
		return err
	}
	name := names[0]
	ctx, cancel := context.WithCancel(e.ctx)
	defer cancel()
	ws, _, err := websocket.Dial(ctx, fmt.Sprintf("ws://127.0.0.1:%d/api/servers/%s/terminal", c.Port, url.PathEscape(name)),
		&websocket.DialOptions{HTTPHeader: c.Header()})
	if err != nil {
		return err
	}
	defer ws.CloseNow()
	ws.SetReadLimit(-1)

	fmt.Fprintf(os.Stderr, "\x1b[2mattached to %s (Ctrl-] detaches)\x1b[0m\r\n", name)
	old, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		return err
	}
	defer func() {
		_ = term.Restore(int(os.Stdin.Fd()), old)
		fmt.Fprintf(os.Stderr, "\r\n\x1b[2mdetached from %s\x1b[0m\n", name)
	}()

	send := func(m termMsg) {
		data, _ := json.Marshal(m)
		_ = ws.Write(ctx, websocket.MessageText, data)
	}
	resize := func() {
		if w, h, err := term.GetSize(int(os.Stdout.Fd())); err == nil {
			send(termMsg{T: "r", Cols: uint16(w), Rows: uint16(h)})
		}
	}
	resize()
	winch := make(chan os.Signal, 1)
	signal.Notify(winch, syscall.SIGWINCH)
	defer signal.Stop(winch)
	go func() {
		for range winch {
			resize()
		}
	}()

	go func() {
		defer cancel()
		buf := make([]byte, 4096)
		for {
			n, err := os.Stdin.Read(buf)
			if err != nil {
				return
			}
			for i := 0; i < n; i++ {
				if buf[i] == 0x1d { // Ctrl-]
					if i > 0 {
						send(termMsg{T: "i", D: string(buf[:i])})
					}
					return
				}
			}
			send(termMsg{T: "i", D: string(buf[:n])})
		}
	}()
	for {
		_, data, err := ws.Read(ctx)
		if err != nil {
			return nil
		}
		_, _ = os.Stdout.Write(data)
	}
}

type termMsg struct {
	T    string `json:"t"`
	D    string `json:"d,omitempty"`
	Cols uint16 `json:"c,omitempty"`
	Rows uint16 `json:"r,omitempty"`
}
