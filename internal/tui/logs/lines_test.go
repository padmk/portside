package logs

import (
	"testing"
	"time"
)

func TestCleanCursorRedraws(t *testing.T) {
	cases := []struct{ name, raw, text, plain string }{
		{
			"compose key bar",
			"\x1b[0G\x1b[2A\x1b[0G\x1b[s\x1b7\x1b[48;0H\x1b[0K\x1b[1;38;2;0;0;0;48;2;255;255;255mw\x1b[0m\x1b[2m Enable Watch\x1b[0m" +
				"\x1b[0G\x1b[u\x1b8\x1b[0G\x1b[s\x1b7\x1b[1B\x1b[0K\x1b[0G\x1b[u\x1b8app  | \x1b[32mready\x1b[0m\r\r",
			"app  | \x1b[32mready\x1b[0m",
			"app  | ready",
		},
		{"two-byte escapes", "\x1b=\x1b7a\x1b8b\x1b>", "b", "b"},
		{"redraw keeps color", "\x1b[31m10%\r100%", "\x1b[31m100%", "100%"},
		{"tab after redraw", "x\ra\tb", "a       b", "a       b"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Clean(c.raw, time.Time{})
			if got.Text != c.text || got.Plain != c.plain {
				t.Errorf("Clean(%q) = %q / %q, want %q / %q", c.raw, got.Text, got.Plain, c.text, c.plain)
			}
		})
	}
}
