package viewfmt

import (
	"strings"
	"testing"
)

// A format character split across two writes, as WebSocket messages split
// followed output, is still replaced, and escape sequences pass.
func TestFormatWriterJoinsSplitCharacters(t *testing.T) {
	var b strings.Builder
	w := NewFormatWriter(&b)
	raw := []byte("\x1b[31mred\x1b[0m a\u202eb c\xffd é\n")
	for i := range raw {
		if _, err := w.Write(raw[i : i+1]); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
	if want := "\x1b[31mred\x1b[0m a�b c\xffd é\n"; b.String() != want {
		t.Errorf("wrote %q, want %q", b.String(), want)
	}
}
