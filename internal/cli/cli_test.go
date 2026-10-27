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
