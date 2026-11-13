package logs

import (
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
)

// Log lines are untrusted program output: cleaning and drawing one takes
// time linear in its length, and drawing only looks at what is shown.

// grows reports whether doing f on an input four times larger took more
// than about four times as long, as quadratic work does.
func grows(t *testing.T, f func(n int)) {
	t.Helper()
	measure := func(n int) time.Duration {
		best := time.Duration(1<<63 - 1)
		for range 3 {
			start := time.Now()
			f(n)
			best = min(best, time.Since(start))
		}
		return best
	}
	const n = 2500
	small, large := measure(n), measure(4*n)
	if large > 8*small+20*time.Millisecond {
		t.Errorf("4x the input took %v against %v", large, small)
	}
}

func TestCleanTabsIsLinear(t *testing.T) {
	grows(t, func(n int) { Clean(strings.Repeat("ab\t", n), time.Time{}) })
	if got := Clean("a\tbc\t世界\td", time.Time{}).Plain; got != "a       bc      世界    d" {
		t.Errorf("Clean = %q", got)
	}
}

func TestDrawingLongLinesIsBounded(t *testing.T) {
	for _, wrap := range []bool{false, true} {
		grows(t, func(n int) {
			p, env := testPane(t, 80, 24, strings.Repeat(strings.Repeat("INFO ", n)+"\n", 32), nil)
			p.wrap = wrap
			p.View(env, 80, 24, true)
		})
	}
}

func TestLongCompleteLineIsSplit(t *testing.T) {
	p, _ := testPane(t, 80, 24, strings.Repeat("INFO ", 50000)+"\nnext\n", nil)
	got := plains(p)
	if len(got) < 4 || got[len(got)-1] != "next" {
		t.Fatalf("lines = %d, last %q", len(got), got[len(got)-1])
	}
	for _, l := range got {
		if len(l) > maxPartial {
			t.Errorf("a line of %d bytes", len(l))
		}
	}
}

// A cut line styles only its shown cells, and shows what styling the
// whole line and cutting it would.
func TestCutLineMatchesWholeLine(t *testing.T) {
	env := testEnv()
	raws := []string{
		strings.Repeat("12:00:01 INFO GET /api/x 200 zażółć 世界 http://a.localhost:3000/x ERROR ", 8),
		strings.Repeat("\x1b[31mred INFO\x1b[0m 世界 \x1b[32mgreen\x1b[1mbold\x1b[0m tail ", 10),
	}
	for _, raw := range raws {
		l := Clean(raw, time.Time{})
		for _, re := range []*regexp.Regexp{nil, regexp.MustCompile(`a|界`)} {
			for _, w := range []int{1, 5, 20, 80} {
				for left := 0; left < ansi.StringWidth(l.Plain)+5; left += 7 {
					whole := Rows(highlight(env.Styles, l, re, false, 0, len(l.Plain)), "", w, false, left, "…")
					p := New()
					p.width, p.left = w, left
					p.buf.Append(l)
					p.search.re, p.search.current = re, -1
					got := p.lineRows(env, 0)
					if ansi.Strip(whole[0]) != ansi.Strip(got[0]) {
						t.Fatalf("width %d from %d: %q, want %q", w, left, ansi.Strip(got[0]), ansi.Strip(whole[0]))
					}
				}
			}
		}
	}
	p := New()
	p.width, p.left = 20, 395
	p.buf.Append(Clean(strings.Repeat("x", 400)+" ERROR "+strings.Repeat("x", 400), time.Time{}))
	if row := p.lineRows(env, 0)[0]; !strings.Contains(row, env.Styles.Error.Bold(true).Render("ERROR")) {
		t.Errorf("row %q does not style ERROR", row)
	}
}

// A long line is split outside its escape sequences, also when a color
// or a link starts right at the cut, and its line end adds no row.
func TestLongLineSplitsOutsideEscapes(t *testing.T) {
	for _, seq := range []string{"\x1b[38;5;196m", "\x1b]8;;http://example.com\x1b\\"} {
		raw := strings.Repeat("a", maxPartial-3) + seq + "red" + "\x1b[0m\r\n"
		p, _ := testPane(t, 80, 24, raw+"next\n", nil)
		got := plains(p)
		if len(got) != 3 || strings.Contains(got[1], "38;5") || strings.Contains(got[1], "example.com") || got[2] != "next" {
			t.Errorf("%q: lines %d, the second %.40q", seq, len(got), got[min(1, len(got)-1)])
		}
	}
}
