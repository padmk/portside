package ui

import (
	"fmt"
	"runtime/debug"

	tea "charm.land/bubbletea/v2"
)

// PanicMsg is a panic in a goroutine the terminal UI started itself
// (Bubble Tea recovers only its own: Update, View and commands). The root
// model quits on it and the program's Run returns it as its error once
// the terminal is restored. It is also the error a suspended session
// (attach, a shell) returns when one of its goroutines panicked.
type PanicMsg struct {
	Value any
	Stack []byte
}

func (p PanicMsg) Error() string {
	return fmt.Sprintf("panic: %v\n\n%s", p.Value, p.Stack)
}

// RecoverWith, deferred at the top of a goroutine, turns a panic into a
// PanicMsg for report: a goroutine that answers through a channel or an
// error reports it there.
func RecoverWith(report func(PanicMsg)) {
	if r := recover(); r != nil {
		report(PanicMsg{Value: r, Stack: debug.Stack()})
	}
}

// Recover, deferred at the top of a goroutine that sends to the program,
// sends a panic there as a PanicMsg.
func Recover(send func(tea.Msg)) {
	if r := recover(); r != nil {
		send(PanicMsg{Value: r, Stack: debug.Stack()})
	}
}
