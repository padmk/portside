package ui

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// A goroutine that answers through a channel answers with its panic, as
// an error carrying the value and the goroutine's stack.
func TestRecoverWithReportsThroughTheChannel(t *testing.T) {
	result := make(chan error, 1)
	go func() {
		defer RecoverWith(func(p PanicMsg) { result <- p })
		var m map[string]int
		m["x"] = 1 //nolint:staticcheck // SA5000: the test needs this panic
		result <- nil
	}()
	select {
	case err := <-result:
		p, ok := errors.AsType[PanicMsg](err)
		if !ok {
			t.Fatalf("got %v, want a PanicMsg", err)
		}
		if !strings.Contains(err.Error(), "panic: assignment to entry in nil map") {
			t.Errorf("error %q lacks the value", err.Error())
		}
		if !strings.Contains(string(p.Stack), "TestRecoverWithReportsThroughTheChannel") {
			t.Errorf("stack lacks the goroutine's frames:\n%s", p.Stack)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the goroutine never answered")
	}
}

func TestRecoverWithStaysQuietWithoutAPanic(t *testing.T) {
	called := false
	func() {
		defer RecoverWith(func(PanicMsg) { called = true })
	}()
	if called {
		t.Error("reported a panic that did not happen")
	}
}
