package cli

import (
	"strings"
	"testing"
)

// Text printed in tables cannot move the cursor or clear the terminal.
func TestPlainReplacesControlCharacters(t *testing.T) {
	if got := plain("web\x1b[2Jfake\u202e\nx"); strings.ContainsAny(got, "\x1b\n\u202e") || !strings.HasPrefix(got, "web") {
		t.Errorf("plain = %q", got)
	}
}

// Without arguments, portside prints the help until a config exists, then
// opens the terminal UI in a terminal and prints the status anywhere else.
func TestBareCommand(t *testing.T) {
	for _, c := range []struct {
		configExists, interactive bool
		want                      string
	}{
		{false, false, ""},
		{false, true, ""},
		{true, true, "tui"},
		{true, false, "status"},
	} {
		if got := bareCommand(c.configExists, c.interactive); got != c.want {
			t.Errorf("bareCommand(config %v, terminal %v) = %q, want %q", c.configExists, c.interactive, got, c.want)
		}
	}
}
