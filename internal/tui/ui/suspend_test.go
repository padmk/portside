package ui

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// modeSetter stands for a program in the suspended UI that sets a
// scrolling region and the DEC line-drawing character set and then ends
// with err.
type modeSetter struct {
	out io.Writer
	err error
}

func (m *modeSetter) SetStdin(io.Reader)    {}
func (m *modeSetter) SetStdout(w io.Writer) { m.out = w }
func (m *modeSetter) SetStderr(io.Writer)   {}
func (m *modeSetter) Run() error {
	_, _ = io.WriteString(m.out, "\x1b[5;10r\x1b(0\x1b[>1u")
	return m.err
}

// Bubble Tea runs an exec command with the program's output, then draws
// again: the modes the command set are reset before that, also when it
// failed.
func TestExecResetsTerminalModes(t *testing.T) {
	for _, fail := range []error{nil, errors.New("lost")} {
		msg := Exec(&modeSetter{err: fail}, nil)()
		e, ok := msg.(ExecMsg)
		if !ok {
			t.Fatalf("Exec gave %T", msg)
		}
		var out bytes.Buffer
		var c tea.ExecCommand = e.Cmd
		c.SetStdin(strings.NewReader(""))
		c.SetStdout(&out)
		c.SetStderr(io.Discard)
		if err := c.Run(); !errors.Is(err, fail) {
			t.Errorf("Run = %v, want %v", err, fail)
		}
		if got, want := out.String(), "\x1b[5;10r\x1b(0\x1b[>1u"+TerminalReset; got != want {
			t.Errorf("output %q, want %q", got, want)
		}
		if !strings.HasPrefix(TerminalReset, "\x1b[!p\x1b[r") {
			t.Errorf("TerminalReset = %q", TerminalReset)
		}
	}
}
