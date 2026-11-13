package ui

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"

	tea "charm.land/bubbletea/v2"
)

// ErrEnding ends a session that suspended the UI (attach, a shell, a CLI
// command) because the program ends: its context ended, or a SIGTERM or
// SIGHUP arrived. Bubble Tea ignores signals while the UI is suspended,
// so the session takes them; after it the terminal is restored and the
// UI quits.
var ErrEnding = errors.New("the terminal UI is ending")

// EndingContext is ctx, also ended by SIGTERM and SIGHUP, with the cause
// ErrEnding either way; stop releases the signals.
func EndingContext(ctx context.Context) (context.Context, func()) {
	out, cancel := context.WithCancelCause(context.Background())
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGHUP)
	done := make(chan struct{})
	go func() {
		defer RecoverWith(func(pm PanicMsg) { cancel(pm) })
		select {
		case <-ctx.Done():
			cancel(ErrEnding)
		case <-sig:
			cancel(ErrEnding)
		case <-done:
		}
	}()
	return out, func() {
		signal.Stop(sig)
		close(done)
		cancel(nil)
	}
}

// ExecMsg asks to suspend the UI for Cmd, then deliver Done's message.
// remote.Tag turns it into Bubble Tea's exec with Done's message tagged
// with the session that asked, so a result that arrives after :on
// switched computers is dropped, and a session that ended with
// ErrEnding quits the UI instead.
type ExecMsg struct {
	Cmd  tea.ExecCommand
	Done tea.ExecCallback
}

// Exec suspends the UI for c (ExecMsg). When c ends, however it ends, the
// terminal modes it may have left set are reset (TerminalReset) before
// the UI draws again.
func Exec(c tea.ExecCommand, done tea.ExecCallback) tea.Cmd {
	return func() tea.Msg { return ExecMsg{Cmd: &resetAfter{ExecCommand: c}, Done: done} }
}

// TerminalReset undoes the terminal modes a program in the suspended UI
// (an attached server, a shell, an editor) may leave set, which the UI
// does not set itself and which would otherwise garble it until the
// window is resized: a soft reset (DECSTR: scrolling region, origin and
// insert modes, character sets, cursor keys, colors), the scrolling region
// again for terminals without DECSTR, mouse and focus reporting, the
// keyboard protocol's pushed flags and xterm's modifyOtherKeys. The UI
// turns the modes it uses back on when it resumes.
const TerminalReset = "\x1b[!p\x1b[r" +
	"\x1b[?1000l\x1b[?1002l\x1b[?1003l\x1b[?1005l\x1b[?1006l\x1b[?1015l\x1b[?1004l" +
	"\x1b[<99u\x1b[>4;0m"

// resetAfter runs a command in the suspended UI and then writes
// TerminalReset to its output.
type resetAfter struct {
	tea.ExecCommand
	out io.Writer
}

func (r *resetAfter) SetStdout(w io.Writer) {
	r.out = w
	r.ExecCommand.SetStdout(w)
}

func (r *resetAfter) Run() error {
	defer func() {
		if r.out != nil {
			_, _ = io.WriteString(r.out, TerminalReset) // nothing to do if the terminal is gone
		}
	}()
	return r.ExecCommand.Run()
}

// endWait is how long a child that was told to end gets before SIGKILL.
const endWait = 2 * time.Second

// Process is a child process run in the suspended terminal until it
// exits or the program ends (EndingContext of Ctx): then the child gets
// SIGHUP, what a closing terminal sends and an interactive shell ends on
// (it passes it to its jobs), and SIGKILL after endWait, and Run returns
// ErrEnding once it is gone. The child shares the terminal's foreground
// process group, so it is signalled itself, not a group.
type Process struct {
	Ctx context.Context
	Cmd *exec.Cmd
}

func (p *Process) SetStdin(r io.Reader) {
	if p.Cmd.Stdin == nil {
		p.Cmd.Stdin = r
	}
}

func (p *Process) SetStdout(w io.Writer) {
	if p.Cmd.Stdout == nil {
		p.Cmd.Stdout = w
	}
}

func (p *Process) SetStderr(w io.Writer) {
	if p.Cmd.Stderr == nil {
		p.Cmd.Stderr = w
	}
}

func (p *Process) Run() error {
	parent := p.Ctx
	if parent == nil {
		parent = context.Background()
	}
	ctx, stop := EndingContext(parent)
	defer stop()
	if err := p.Cmd.Start(); err != nil {
		return err
	}
	waited := make(chan error, 1)
	go func() {
		defer RecoverWith(func(pm PanicMsg) { waited <- pm })
		waited <- p.Cmd.Wait()
	}()
	select {
	case err := <-waited:
		return err
	case <-ctx.Done():
	}
	_ = p.Cmd.Process.Signal(syscall.SIGHUP)
	// The program's exit status after the hang-up is not the session's
	// result; only a panic of the waiting goroutine is.
	var ended error
	select {
	case ended = <-waited:
	case <-time.After(endWait):
		_ = p.Cmd.Process.Kill()
		ended = <-waited
	}
	if pm, ok := errors.AsType[PanicMsg](context.Cause(ctx)); ok {
		return pm
	}
	if pm, ok := errors.AsType[PanicMsg](ended); ok {
		return pm
	}
	return ErrEnding
}
