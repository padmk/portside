package supervisor

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// Program output is untrusted input: numbers in escape sequences may be
// any size and a line may repeat a sequence as often as it likes.

func TestRedrawLineSaturatesHugeParameters(t *testing.T) {
	huge := "9223372036854775807"
	cases := map[string]string{
		"x\x1b[" + huge + "Cy":                   "x" + strings.Repeat(" ", maxRedrawColumn-1) + "y",
		"x\x1b[" + huge + "0000Cy":               "x" + strings.Repeat(" ", maxRedrawColumn-1) + "y",
		"abc\x1b[" + huge + "Dz":                 "zbc",
		"abc\x1b[" + huge + "Gz":                 "abc" + strings.Repeat(" ", maxRedrawColumn-3) + "z",
		"x\x1b[" + huge + "C\x1b[" + huge + "Cy": "x" + strings.Repeat(" ", maxRedrawColumn-1) + "y",
	}
	for in, want := range cases {
		if got := PlainLine(in); got != want {
			t.Errorf("PlainLine(%.40q) = %.40q (%d bytes), want %d bytes", in, got, len(got), len(want))
		}
	}
}

func TestCursorUpSaturatesHugeParameters(t *testing.T) {
	huge := "\x1b[9223372036854775807A"
	for _, l := range []string{huge, huge + huge, huge + "\x1b[5F" + huge, "\x1b[99999999999999999999999A"} {
		if got := CursorUp(l); got != maxCursorUp {
			t.Errorf("CursorUp(%q) = %d, want %d", l, got, maxCursorUp)
		}
	}
}

// The daemon reads the tail of every log on each state broadcast, so a
// line that panicked there would stop the daemon.
func TestTailSurvivesHugeCursorMoves(t *testing.T) {
	lb := NewMemoryLog()
	_, _ = lb.Write([]byte("x\x1b[9223372036854775807Cy\n\x1b[9223372036854775807A\x1b[9223372036854775807Aup\nlast\n"))
	if got := lb.Tail(1, 80); len(got) != 1 || got[0] != "last" {
		t.Errorf("Tail = %q", got)
	}
	if got := lb.Lines(0); len(got) == 0 {
		t.Errorf("Lines = %q", got)
	}
}

// Each pattern repeats a short sequence that a quadratic replay would
// walk the whole line for. Making the line four times longer may make the
// replay at most about four times slower, not sixteen.
func TestRedrawLineIsLinear(t *testing.T) {
	color := "\x1b[38;2;255;255;255m\x1b[48;2;0;0;0m"
	keep := func(seq string) bool { return strings.HasSuffix(seq, "m") || strings.HasPrefix(seq, "\x1b]8;") }
	a := func(n int) string { return strings.Repeat("a", n) }
	cases := map[string]func(n int) string{
		"colored carriage-return redraws": func(n int) string { return strings.Repeat(color+"50%\x1b[0m\r", n) },
		"colored redraws without reset":   func(n int) string { return strings.Repeat(color+"x\r", n) },
		"colored overwrites in one cell":  func(n int) string { return strings.Repeat(color+"x\b", n) },
		"cursor saves":                    func(n int) string { return a(4*n) + strings.Repeat("\x1b7a", n/4) },
		"saves and restores":              func(n int) string { return a(4*n) + strings.Repeat("\x1b7\r\x1b8", n/4) },
		"erase to the cursor":             func(n int) string { return a(8*n) + strings.Repeat("\x1b[1K", n) },
		"erase after moving left":         func(n int) string { return a(8*n) + strings.Repeat("\x1b[Dx\x1b[1K", n) },
		"long hyperlink then colors": func(n int) string {
			return "\x1b]8;;http://" + a(n) + "\x1b\\" + strings.Repeat("\x1b[31m\r", n)
		},
	}
	replay := func(line string) time.Duration {
		best := time.Duration(1<<63 - 1)
		for range 3 {
			start := time.Now()
			RedrawLine(line, keep)
			CursorUp(line)
			best = min(best, time.Since(start))
		}
		return best
	}
	const n = 4000
	for name, gen := range cases {
		t.Run(name, func(t *testing.T) {
			small, large := replay(gen(n)), replay(gen(4*n))
			if large > 8*small+20*time.Millisecond {
				t.Errorf("4x the input took %v against %v", large, small)
			}
		})
	}
}

func TestRedrawLineKeepsStyleAcrossManyRedraws(t *testing.T) {
	sgr := func(s string) bool { return strings.HasSuffix(s, "m") }
	line := strings.Repeat("\x1b[0m\x1b[32mold\r", 1000) + "\x1b[1mnew"
	got := RedrawLine(line, sgr)
	if !strings.HasSuffix(got, "\x1b[0m\x1b[32m\x1b[1mnew") || strings.Contains(got, "old") || len(got) > maxKept+len("\x1b[1mnew") {
		t.Errorf("RedrawLine = %q (%d bytes)", got, len(got))
	}
}

func FuzzRedrawLine(f *testing.F) {
	for _, s := range []string{
		composeKeyBar, "x\x1b[9223372036854775807Cy", "\x1b[9223372036854775807A\x1b[9223372036854775807A",
		"a\x1b7b\x1b8c", "abc\x1b[1K\x1b[2D\x1b[Kz", "\x1b[31m10%\r100%", "wait |\b/\b-",
	} {
		f.Add(s)
	}
	sgr := func(s string) bool { return strings.HasSuffix(s, "m") }
	f.Fuzz(func(t *testing.T, line string) {
		if up := CursorUp(line); up < 0 || up > maxCursorUp {
			t.Fatalf("CursorUp = %d", up)
		}
		plain := PlainLine(line)
		if strings.ContainsFunc(plain, func(r rune) bool { return dropControl(r) < 0 }) {
			t.Fatalf("PlainLine(%q) = %q holds a control character", line, plain)
		}
		if n := utf8.RuneCountInString(plain); n > utf8.RuneCountInString(line)+maxRedrawColumn {
			t.Fatalf("PlainLine(%q) has %d runes", line, n)
		}
		RedrawLine(line, sgr)
	})
}
