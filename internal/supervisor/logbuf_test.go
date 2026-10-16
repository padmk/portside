package supervisor

import (
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

var t0 = time.Date(2024, 5, 6, 7, 8, 9, 0, time.UTC)

// put appends s to lb the way Write does, but stamped with an explicit time.
func put(lb *LogBuffer, at time.Time, s string) {
	chunk := []byte(s)
	lb.mu.Lock()
	lb.markLines(chunk, at)
	lb.buf = append(lb.buf, chunk...)
	lb.mu.Unlock()
}

func texts(lines []Line) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = l.Text
	}
	return out
}

func times(lines []Line) []time.Time {
	out := make([]time.Time, len(lines))
	for i, l := range lines {
		out[i] = l.At
	}
	return out
}

func TestPlainLines(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		n    int
		want []string
	}{
		{"plain", "a\nb\nc", 0, []string{"a", "b", "c"}},
		{"colors", "\x1b[31mred\x1b[0m ok\n\x1b[1;32mgreen\x1b[m", 0, []string{"red ok", "green"}},
		{"osc title", "\x1b]0;title\x07shell\n", 0, []string{"shell"}},
		{"osc st", "\x1b]8;;http://x\x1b\\link\x1b]8;;\x1b\\", 0, []string{"link"}},
		{"cursor and erase", "\x1b[2K\x1b[1Gdone", 0, []string{"done"}},
		{"cr redraw", "10%\r50%\r100%\nnext", 0, []string{"100%", "next"}},
		{"cr redraw with erase", "50%\r\x1b[K100%", 0, []string{"100%"}},
		{"crlf", "a\r\nb\r\n", 0, []string{"a", "b"}},
		{"trailing blank lines", "a\n\n  \n\t\n", 0, []string{"a"}},
		{"inner blank lines kept", "a\n\nb\n", 0, []string{"a", "", "b"}},
		{"control chars dropped", "a\x00b\x07c\x08d\te", 0, []string{"abcd\te"}},
		{"unicode", "zażółć 世界\n", 0, []string{"zażółć 世界"}},
		{"last n", "1\n2\n3\n4\n", 2, []string{"3", "4"}},
		{"n larger", "1\n2\n", 5, []string{"1", "2"}},
		{"n after trimming blanks", "1\n2\n3\n\n\n", 2, []string{"2", "3"}},
		{"empty", "", 0, []string{}},
		{"only blanks", "\n\n", 0, []string{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := PlainLines([]byte(c.raw), c.n)
			if !slices.Equal(got, c.want) {
				t.Errorf("PlainLines(%q, %d) = %q, want %q", c.raw, c.n, got, c.want)
			}
		})
	}
}

func TestPlainLine(t *testing.T) {
	cases := map[string]string{
		"hello":                    "hello",
		"\x1b[31mhello\x1b[0m":     "hello",
		"abc\r":                    "abc",
		"loading\rdone":            "done",
		"a\x1b[2Kb":                "ab",
		"tab\tkept":                "tab\tkept",
		"bell\x07":                 "bell",
		"\x1b(Bplain":              "plain",
		"\x1b[?25lhidden\x1b[?25h": "hidden",
	}
	for in, want := range cases {
		if got := PlainLine(in); got != want {
			t.Errorf("PlainLine(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPlainLineDoubleCarriageReturnEnding(t *testing.T) {
	if got := PlainLine("abc\r\r"); got != "abc" {
		t.Errorf("PlainLine = %q, want %q", got, "abc")
	}
}

func TestStripANSI(t *testing.T) {
	if got := StripANSI("\x1b[38;5;196mx\x1b[0m\x1b[1Ay"); got != "xy" {
		t.Errorf("StripANSI = %q", got)
	}
}

func TestLogBufferLinesAndTail(t *testing.T) {
	lb := NewMemoryLog()
	lb.Write([]byte("one\r\n\x1b[32mtwo\x1b[0m\r\n\r\n   \r\nthree\r\n\r\n"))
	if got := lb.Lines(0); !slices.Equal(got, []string{"one", "two", "", "   ", "three"}) {
		t.Errorf("Lines = %q", got)
	}
	if got := lb.Lines(2); !slices.Equal(got, []string{"   ", "three"}) {
		t.Errorf("Lines(2) = %q", got)
	}
	if got := lb.Tail(2, 100); !slices.Equal(got, []string{"two", "three"}) {
		t.Errorf("Tail = %q", got)
	}
	if got := lb.Tail(5, 4); !slices.Equal(got, []string{"one", "two", "thr…"}) {
		t.Errorf("Tail with cut = %q", got)
	}
}

func TestTimedLinesOneChunkSharesATime(t *testing.T) {
	lb := NewMemoryLog()
	put(lb, t0, "a\nb\nc\n")
	got := lb.TimedLines()
	if !slices.Equal(texts(got), []string{"a", "b", "c"}) {
		t.Fatalf("lines = %q", texts(got))
	}
	for i, l := range got {
		if !l.At.Equal(t0) {
			t.Errorf("line %d at %v, want %v", i, l.At, t0)
		}
	}
	if len(lb.marks) != 1 {
		t.Errorf("marks = %d, want 1", len(lb.marks))
	}
}

func TestTimedLinesFollowMarks(t *testing.T) {
	lb := NewMemoryLog()
	put(lb, t0, "first\n")
	put(lb, t0.Add(500*time.Millisecond), "same second\n")
	put(lb, t0.Add(1500*time.Millisecond), "next\n")
	put(lb, t0.Add(5*time.Second), "later\nand more\n")
	got := lb.TimedLines()
	want := []time.Time{t0, t0, t0.Add(1500 * time.Millisecond), t0.Add(5 * time.Second), t0.Add(5 * time.Second)}
	if !slices.Equal(texts(got), []string{"first", "same second", "next", "later", "and more"}) {
		t.Fatalf("lines = %q", texts(got))
	}
	for i := range want {
		if !got[i].At.Equal(want[i]) {
			t.Errorf("line %d (%q) at %v, want %v", i, got[i].Text, got[i].At, want[i])
		}
	}
}

func TestMarksAtMostOnePerSecond(t *testing.T) {
	lb := NewMemoryLog()
	for i := 0; i < 50; i++ {
		put(lb, t0.Add(time.Duration(i)*100*time.Millisecond), fmt.Sprintf("line %d\n", i))
	}
	// 5 seconds of output, a mark each time a full second has passed.
	if len(lb.marks) != 5 {
		t.Errorf("marks = %d, want 5", len(lb.marks))
	}
	for i := 1; i < len(lb.marks); i++ {
		if d := lb.marks[i].at.Sub(lb.marks[i-1].at); d < time.Second {
			t.Errorf("marks %d and %d are %v apart", i-1, i, d)
		}
	}
}

func TestMarksOneChunkManyLines(t *testing.T) {
	lb := NewMemoryLog()
	put(lb, t0, strings.Repeat("x\n", 1000))
	if len(lb.marks) != 1 {
		t.Errorf("marks = %d, want 1", len(lb.marks))
	}
}

func TestPartialLineKeepsItsStartTime(t *testing.T) {
	lb := NewMemoryLog()
	put(lb, t0, "ab")
	put(lb, t0.Add(10*time.Second), "cd\nef")
	put(lb, t0.Add(20*time.Second), "gh\n")
	got := lb.TimedLines()
	if !slices.Equal(texts(got), []string{"abcd", "efgh"}) {
		t.Fatalf("lines = %q", texts(got))
	}
	if !got[0].At.Equal(t0) || !got[1].At.Equal(t0.Add(10*time.Second)) {
		t.Errorf("times = %v", times(got))
	}
}

func TestTimedLinesTrailingBlanksAndPlainText(t *testing.T) {
	lb := NewMemoryLog()
	put(lb, t0, "\x1b[31mred\x1b[0m\r\nprogress 1\rprogress 2\n\n  \n")
	got := lb.TimedLines()
	if !slices.Equal(texts(got), []string{"red", "progress 2"}) {
		t.Errorf("lines = %q", texts(got))
	}
}

func TestTimedLinesEmpty(t *testing.T) {
	if got := NewMemoryLog().TimedLines(); len(got) != 0 {
		t.Errorf("lines = %v", got)
	}
}

func TestTimedLinesMatchPlainLines(t *testing.T) {
	lb := NewMemoryLog()
	raw := "a\n\x1b[1mb\x1b[0m\r\n\nc\rd\ne"
	put(lb, t0, raw)
	if got, want := texts(lb.TimedLines()), lb.Lines(0); !slices.Equal(got, want) {
		t.Errorf("TimedLines = %q, Lines = %q", got, want)
	}
}

func TestClearResetsMarks(t *testing.T) {
	lb := NewMemoryLog()
	put(lb, t0, "old\nolder\n")
	lb.Clear()
	if got := lb.TimedLines(); len(got) != 0 {
		t.Errorf("lines after Clear = %v", got)
	}
	if lb.marks != nil || len(lb.Raw()) != 0 {
		t.Errorf("Clear left marks %v and %d bytes", lb.marks, len(lb.Raw()))
	}
	// A line written right after Clear gets its own time, however recent the
	// cleared output was.
	put(lb, t0.Add(100*time.Millisecond), "new\n")
	got := lb.TimedLines()
	if len(got) != 1 || got[0].Text != "new" || !got[0].At.Equal(t0.Add(100*time.Millisecond)) {
		t.Errorf("lines = %+v", got)
	}
}

func TestShiftMarks(t *testing.T) {
	mk := func(offs ...int) *LogBuffer {
		lb := NewMemoryLog()
		for i, off := range offs {
			lb.marks = append(lb.marks, mark{off, t0.Add(time.Duration(i) * time.Minute)})
		}
		return lb
	}
	offs := func(lb *LogBuffer) []int {
		var out []int
		for _, m := range lb.marks {
			out = append(out, m.off)
		}
		return out
	}

	// The last mark before the cut moves to the start of what remains.
	lb := mk(0, 10, 20)
	lb.shiftMarks(15)
	if got := offs(lb); !slices.Equal(got, []int{0, 5}) || !lb.marks[0].at.Equal(t0.Add(time.Minute)) {
		t.Errorf("cut inside a line: offsets %v first %v", got, lb.marks[0].at)
	}

	// A mark exactly at the cut stays and becomes the start.
	lb = mk(0, 15, 30)
	lb.shiftMarks(15)
	if got := offs(lb); !slices.Equal(got, []int{0, 15}) || !lb.marks[0].at.Equal(t0.Add(time.Minute)) {
		t.Errorf("cut on a mark: offsets %v first %v", got, lb.marks[0].at)
	}

	// Marks all after the cut keep their order and move back by n.
	lb = mk(20, 30)
	lb.shiftMarks(10)
	if got := offs(lb); !slices.Equal(got, []int{10, 20}) {
		t.Errorf("cut before all marks: offsets %v", got)
	}

	lb = mk()
	lb.shiftMarks(10)
	if len(lb.marks) != 0 {
		t.Errorf("marks = %v", lb.marks)
	}
}

// fill writes numbered lines of 100 bytes with explicit times, group lines
// per second, up to just under the buffer limit.
func fill(lb *LogBuffer, group int) int {
	n := logBufferSize / 100
	for i := 0; i < n; i++ {
		put(lb, t0.Add(time.Duration(i/group)*time.Second), fmt.Sprintf("%07d%s\n", i, strings.Repeat(".", 92)))
	}
	return n
}

func TestMarksSurviveBufferTrim(t *testing.T) {
	for _, group := range []int{1, 10} {
		t.Run(fmt.Sprintf("%d per second", group), func(t *testing.T) {
			lb := NewMemoryLog()
			n := fill(lb, group)
			if len(lb.buf) > logBufferSize {
				t.Fatal("fixture already trimmed")
			}
			// This write crosses the limit and trims the head of the buffer.
			before := time.Now()
			lb.Write([]byte(fmt.Sprintf("%07d%s\n", n, strings.Repeat(".", 92))))
			if len(lb.buf) > logBufferSize*3/4+200 || len(lb.buf) < logBufferSize/2 {
				t.Fatalf("buffer holds %d bytes after the trim", len(lb.buf))
			}
			got := lb.TimedLines()
			if len(got) == 0 || len(got) >= n {
				t.Fatalf("lines after trim = %d", len(got))
			}
			first := -1
			for i, l := range got[:len(got)-1] {
				var idx int
				if _, err := fmt.Sscanf(l.Text, "%07d", &idx); err != nil {
					t.Fatalf("line %d = %q", i, l.Text)
				}
				if first < 0 {
					first = idx
				}
				if idx != first+i {
					t.Fatalf("line %d is numbered %d, want %d", i, idx, first+i)
				}
				// A line dated by a mark that was cut away is dated by the
				// last mark before it, as it was before the trim.
				if want := t0.Add(time.Duration(idx/group) * time.Second); !l.At.Equal(want) {
					t.Fatalf("line %d at %v, want %v", idx, l.At, want)
				}
			}
			if first <= 0 {
				t.Fatalf("nothing was trimmed (first line %d)", first)
			}
			if last := got[len(got)-1]; last.At.Before(before) || last.At.After(time.Now()) {
				t.Errorf("new line at %v, want about now", last.At)
			}
			if lb.marks[0].off != 0 {
				t.Errorf("first mark at offset %d, want 0", lb.marks[0].off)
			}
			for _, m := range lb.marks {
				if m.off < 0 || m.off >= len(lb.buf) {
					t.Errorf("mark offset %d outside the buffer (%d bytes)", m.off, len(lb.buf))
				}
			}
		})
	}
}

func TestLogBufferConcurrent(t *testing.T) {
	lb := NewMemoryLog()
	var wg sync.WaitGroup
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				lb.Write([]byte(fmt.Sprintf("line %d\n", i)))
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			lb.TimedLines()
			lb.Tail(3, 20)
			if i%50 == 0 {
				lb.Clear()
			}
		}
	}()
	wg.Wait()
}

func TestSubscribeReceivesWrites(t *testing.T) {
	lb := NewMemoryLog()
	lb.Write([]byte("before\n"))
	snap, ch := lb.Subscribe()
	if string(snap) != "before\n" {
		t.Errorf("snapshot = %q", snap)
	}
	lb.Write([]byte("after\n"))
	if got := string(<-ch); got != "after\n" {
		t.Errorf("chunk = %q", got)
	}
	lb.Unsubscribe(ch)
	lb.Write([]byte("unseen\n"))
	select {
	case got := <-ch:
		t.Errorf("received %q after Unsubscribe", got)
	default:
	}
}
