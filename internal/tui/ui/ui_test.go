package ui

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/padmk/portside/internal/tui/theme"
)

// TestProcessEndsWithTheProgram: a child run while the UI is suspended
// is ended when the program's context ends, and Run says why.
func TestProcessEndsWithTheProgram(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	p := &Process{Ctx: ctx, Cmd: exec.Command("sleep", "30")}
	errc := make(chan error, 1)
	go func() { errc <- p.Run() }()
	time.Sleep(100 * time.Millisecond)
	cancel()
	select {
	case err := <-errc:
		if !errors.Is(err, ErrEnding) {
			t.Fatalf("Run = %v, want ErrEnding", err)
		}
	case <-time.After(endWait + 2*time.Second):
		t.Fatal("the child outlived the program")
	}
	if p.Cmd.ProcessState == nil {
		t.Error("the child was not waited for")
	}
}

// TestBottomBorderMatchesTheBox: a status in the bottom border keeps the
// box's border, the heavy one of a focused pane without colors.
func TestBottomBorderMatchesTheBox(t *testing.T) {
	env := &Env{Styles: theme.New(theme.TerminalFamily.Variant(true)), Glyphs: theme.Unicode}
	env.Styles.Mono = true
	rows := strings.Split(WithBottom(env, Box(env, 20, 4, true, "x", "", nil), 20, true, "3/9"), "\n")
	last := ansi.Strip(rows[len(rows)-1])
	if !strings.HasPrefix(last, theme.Unicode.Heavy.BottomLeft) || !strings.Contains(last, "3/9") || ansi.StringWidth(last) != 20 {
		t.Errorf("bottom border = %q", last)
	}
}
