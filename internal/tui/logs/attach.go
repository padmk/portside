package logs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/coder/websocket"
	"github.com/muesli/cancelreader"
	"golang.org/x/term"

	"github.com/padmk/portside/internal/client"
	"github.com/padmk/portside/internal/server"
	"github.com/padmk/portside/internal/tui/ui"
)

const (
	// detachKey is ctrl-], which ends an attached session.
	detachKey = 0x1d
	// exitPoll is how often an attached session checks that the process
	// it attached to still runs: the terminal stream outlives it.
	exitPoll = time.Second
)

// Attach suspends the terminal UI and connects the terminal to a server's
// pseudo-terminal until ctrl-], the end of the process attached to, a
// lost connection, the end of input or a SIGTERM or SIGHUP; the UI then
// comes back as it was. The session also ends with the program
// (env.ProgramContext).
func Attach(env *ui.Env, server string) tea.Cmd {
	return AttachContext(env.ProgramContext(), env, server)
}

// AttachContext is Attach for a session that also ends with ctx, the
// program's lifetime. The client and the process attached to are taken
// from env now; the session never reads env.
func AttachContext(ctx context.Context, env *ui.Env, server string) tea.Cmd {
	s := &session{ctx: ctx, client: env.Client, server: server, pid: serverPID(env, server),
		stdin: os.Stdin, stdout: os.Stdout}
	return ui.Exec(s, func(err error) tea.Msg {
		n := endNotice(server, err)
		// The session wrote over the screen, so the whole frame is drawn again.
		return tea.BatchMsg{tea.ClearScreen, func() tea.Msg { return n }}
	})
}

func serverPID(env *ui.Env, name string) int {
	if env.State == nil {
		return 0
	}
	for _, v := range env.State.Servers {
		if v.Name == name {
			return v.PID
		}
	}
	return 0
}

// Why a session ended, other than an error of its own.
var (
	errDetached  = errors.New("detached")
	errInputDone = errors.New("input ended")
)

// exitedError ends a session whose process ended or was replaced.
type exitedError struct{ restarted bool }

func (e exitedError) Error() string {
	if e.restarted {
		return "the process restarted"
	}
	return "the process exited"
}

// lostError ends a session whose connection failed.
type lostError struct{ err error }

func (e lostError) Error() string { return "connection lost: " + e.err.Error() }
func (e lostError) Unwrap() error { return e.err }

// endNotice says how a session ended.
func endNotice(server string, err error) ui.NoticeMsg {
	var exited exitedError
	switch {
	case err == nil, errors.Is(err, errDetached), errors.Is(err, errInputDone):
		return ui.NoticeMsg{Text: "detached from " + server, Level: ui.LevelInfo}
	case errors.As(err, &exited):
		return ui.NoticeMsg{Text: "detached from " + server + ": " + exited.Error(), Level: ui.LevelWarn}
	case errors.Is(err, context.Canceled):
		return ui.NoticeMsg{Text: "detached from " + server + ": the terminal UI is ending", Level: ui.LevelInfo}
	}
	return ui.NoticeMsg{Text: "attach " + server + ": " + err.Error(), Level: ui.LevelError}
}

// session is an attached terminal, run by tea.Exec while the UI is
// suspended.
type session struct {
	ctx    context.Context
	client *client.Client
	server string
	// pid is the process attached to, 0 when none ran.
	pid int
	// path is the terminal WebSocket, the server's terminal when empty;
	// banner is the line printed before it connects.
	path, banner string
	stdin        io.Reader
	stdout       io.Writer
}

func (s *session) SetStdin(r io.Reader)  { s.stdin = r }
func (s *session) SetStdout(w io.Writer) { s.stdout = w }
func (s *session) SetStderr(io.Writer)   {}

// termMsg is a client message of the terminal WebSocket.
type termMsg struct {
	T    string `json:"t"` // "i" input, "r" resize
	D    string `json:"d,omitempty"`
	Cols uint16 `json:"c,omitempty"`
	Rows uint16 `json:"r,omitempty"`
}

// Run connects until one of the session's ends; every goroutine it starts
// has returned and the terminal is restored when it returns. The error
// is why it ended: errDetached, errInputDone, exitedError, lostError,
// ui.ErrEnding, a ui.PanicMsg or a failure to start.
func (s *session) Run() (err error) {
	// A panic, here or in a goroutine (which cancels with it), is the
	// session's error once the terminal is restored; the UI quits on it.
	defer ui.RecoverWith(func(p ui.PanicMsg) { err = p })
	in, ok := s.stdin.(*os.File)
	if !ok || !term.IsTerminal(int(in.Fd())) {
		return errors.New("attach needs an interactive terminal")
	}
	// The end of the program or a SIGTERM or SIGHUP ends the session with
	// ui.ErrEnding, and the UI quits after it.
	sigCtx, stopSignals := ui.EndingContext(s.ctx)
	defer stopSignals()
	ctx, cancel := context.WithCancelCause(sigCtx)
	defer cancel(nil)
	panicked := func(p ui.PanicMsg) { cancel(p) }
	path, banner := s.path, s.banner
	if path == "" {
		path = "/api/servers/" + url.PathEscape(s.server) + "/terminal"
		banner = "Attached to " + s.server + ". Press ctrl-] to detach."
	}
	ws, err := s.client.DialWebSocket(ctx, path)
	if err != nil {
		return err
	}
	defer ws.CloseNow()
	ws.SetReadLimit(-1)

	if _, err := fmt.Fprintf(s.stdout, "%s\r\n", banner); err != nil {
		return err
	}
	old, err := term.MakeRaw(int(in.Fd()))
	if err != nil {
		return err
	}
	defer func() { _ = term.Restore(int(in.Fd()), old) }()

	send := func(m termMsg) {
		data, _ := json.Marshal(m)
		if err := ws.Write(ctx, websocket.MessageText, data); err != nil && ctx.Err() == nil {
			cancel(lostError{err})
		}
	}
	var wg sync.WaitGroup
	defer wg.Wait()
	defer cancel(nil)

	out, _ := s.stdout.(*os.File)
	resize := func() {
		fd := int(in.Fd())
		if out != nil {
			fd = int(out.Fd())
		}
		if w, h, err := term.GetSize(fd); err == nil {
			send(termMsg{T: "r", Cols: uint16(w), Rows: uint16(h)})
		}
	}
	resize()
	winch := make(chan os.Signal, 1)
	signal.Notify(winch, syscall.SIGWINCH)
	defer signal.Stop(winch)
	wg.Go(func() {
		defer ui.RecoverWith(panicked)
		for {
			select {
			case <-ctx.Done():
				return
			case <-winch:
				resize()
			}
		}
	})
	if s.pid > 0 {
		wg.Go(func() {
			defer ui.RecoverWith(panicked)
			s.watchExit(ctx, cancel)
		})
	}

	// The input reader can be cancelled, so it never takes a key meant for
	// the UI after the session ends.
	reader, err := cancelreader.NewReader(in)
	if err != nil {
		return err
	}
	wg.Go(func() {
		defer ui.RecoverWith(panicked)
		defer reader.Close()
		buf := make([]byte, 4096)
		for {
			n, err := reader.Read(buf)
			for i := range n {
				if buf[i] == detachKey {
					if i > 0 {
						send(termMsg{T: "i", D: string(buf[:i])})
					}
					cancel(errDetached)
					return
				}
			}
			if n > 0 {
				send(termMsg{T: "i", D: string(buf[:n])})
			}
			if err != nil {
				if !errors.Is(err, cancelreader.ErrCanceled) {
					cancel(errInputDone)
				}
				return
			}
		}
	})
	// Deferred calls run last first: the reader is cancelled, then the
	// context, then the goroutines are waited for and the terminal is
	// restored.
	defer reader.Cancel()

	for {
		_, data, err := ws.Read(ctx)
		if err != nil {
			if cause := context.Cause(ctx); cause != nil {
				return cause
			}
			return lostError{err}
		}
		if _, err := s.stdout.Write(data); err != nil {
			return err
		}
	}
}

// watchExit ends the session when the process it attached to is gone:
// the server shows no process or another one.
func (s *session) watchExit(ctx context.Context, cancel context.CancelCauseFunc) {
	t := time.NewTicker(exitPoll)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		var st server.State
		rctx, rcancel := context.WithTimeout(ctx, 5*time.Second)
		err := s.client.Do(rctx, http.MethodGet, "/api/state", nil, &st)
		rcancel()
		if err != nil {
			continue // a lost connection ends the stream too
		}
		pid := 0
		for _, v := range st.Servers {
			if v.Name == s.server {
				pid = v.PID
			}
		}
		if pid != s.pid {
			cancel(exitedError{restarted: pid != 0})
			return
		}
	}
}

// ContainerShell suspends the terminal UI for a shell in a running
// container (GET /api/containers/{ref}/shell, the same terminal protocol
// as attach) until ctrl-], the shell's end, a lost connection or the end
// of ctx; the daemon ends the shell's processes when the session goes.
func ContainerShell(ctx context.Context, env *ui.Env, name string) tea.Cmd {
	s := &session{ctx: ctx, client: env.Client, server: name, stdin: os.Stdin, stdout: os.Stdout,
		path:   "/api/containers/" + url.PathEscape(name) + "/shell",
		banner: "Shell in container " + name + ". Press ctrl-] to detach."}
	return ui.Exec(s, func(err error) tea.Msg {
		n := ui.NoticeMsg{Text: "back from the shell in " + name, Level: ui.LevelInfo}
		var lost lostError
		switch {
		case errors.As(err, &lost) && websocket.CloseStatus(err) == websocket.StatusNormalClosure:
			n.Text = "the shell in " + name + " ended"
		case err != nil && !errors.Is(err, errDetached) && !errors.Is(err, errInputDone) && !errors.Is(err, context.Canceled):
			n = ui.NoticeMsg{Text: "shell in " + name + ": " + err.Error(), Level: ui.LevelError}
		}
		return tea.BatchMsg{tea.ClearScreen, func() tea.Msg { return n }}
	})
}
