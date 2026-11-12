package tui

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/padmk/portside/internal/tui/ui"
)

// A panic reported by any session's goroutine quits the program.
func TestPanicMsgQuits(t *testing.T) {
	m := testModel(t)
	_, cmd := m.Update(ui.GenMsg{Gen: m.gen + 1, Msg: ui.PanicMsg{Value: "boom"}})
	if cmd == nil {
		t.Fatal("no command after a panic")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatalf("the command is not quit")
	}
	if m.panicked == nil || !strings.Contains(m.panicked.Error(), "panic: boom") {
		t.Errorf("panicked = %v", m.panicked)
	}
}

// A goroutine of the UI that panics ends the program, which returns the
// panic as its error after it restored the terminal.
func TestRunReturnsAGoroutinesPanic(t *testing.T) {
	m := testModel(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	prog := tea.NewProgram(m, tea.WithContext(ctx), tea.WithInput(nil), tea.WithOutput(io.Discard))
	go func() {
		defer ui.Recover(prog.Send)
		panic("stream broke")
	}()
	done := make(chan error, 1)
	go func() { done <- m.runProgram(prog, cancel) }()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "panic: stream broke") {
			t.Fatalf("Run returned %v, want the panic", err)
		}
		if !strings.Contains(err.Error(), "TestRunReturnsAGoroutinesPanic") {
			t.Errorf("the error lacks the stack: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the program did not end")
	}
}
