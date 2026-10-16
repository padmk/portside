package supervisor

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

const (
	logBufferSize = 1 << 20  // in-memory scrollback per server
	logFileLimit  = 10 << 20 // rotate the on-disk log after this many bytes
)

// LogBuffer keeps a server's raw terminal output: a bounded in-memory tail for
// replay into terminals, an on-disk copy, and fan-out to live subscribers.
type LogBuffer struct {
	mu      sync.Mutex
	buf     []byte
	file    *os.File
	path    string
	written int64
	subs    map[chan []byte]struct{}
}

func newLogBuffer(dir, name string) *LogBuffer {
	lb := &LogBuffer{subs: map[chan []byte]struct{}{}}
	if dir != "" {
		_ = os.MkdirAll(dir, 0o755)
		lb.path = filepath.Join(dir, name+".log")
		lb.openFile()
	}
	return lb
}

// NewMemoryLog returns a buffer that is kept only in memory.
func NewMemoryLog() *LogBuffer { return newLogBuffer("", "") }

func (lb *LogBuffer) openFile() {
	f, err := os.OpenFile(lb.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	lb.file = f
	if st, err := f.Stat(); err == nil {
		lb.written = st.Size()
	}
}

// Write appends output and notifies subscribers. Slow subscribers drop chunks
// rather than stall the process being supervised.
func (lb *LogBuffer) Write(p []byte) (int, error) {
	chunk := append([]byte(nil), p...)
	lb.mu.Lock()
	lb.buf = append(lb.buf, chunk...)
	if len(lb.buf) > logBufferSize {
		cut := len(lb.buf) - logBufferSize*3/4
		if i := bytes.IndexByte(lb.buf[cut:], '\n'); i >= 0 {
			cut += i + 1
		}
		lb.buf = append([]byte(nil), lb.buf[cut:]...)
	}
	if lb.file != nil {
		n, _ := lb.file.Write(chunk)
		lb.written += int64(n)
		if lb.written > logFileLimit {
			lb.file.Close()
			_ = os.Rename(lb.path, lb.path+".1")
			lb.openFile()
		}
	}
	for ch := range lb.subs {
		select {
		case ch <- chunk:
		default:
		}
	}
	lb.mu.Unlock()
	return len(p), nil
}

// Subscribe returns the current scrollback and a channel of new output.
func (lb *LogBuffer) Subscribe() ([]byte, chan []byte) {
	ch := make(chan []byte, 256)
	lb.mu.Lock()
	defer lb.mu.Unlock()
	lb.subs[ch] = struct{}{}
	return append([]byte(nil), lb.buf...), ch
}

// Unsubscribe stops delivery to ch.
func (lb *LogBuffer) Unsubscribe(ch chan []byte) {
	lb.mu.Lock()
	delete(lb.subs, ch)
	lb.mu.Unlock()
}

// Raw returns a copy of the scrollback.
func (lb *LogBuffer) Raw() []byte {
	lb.mu.Lock()
	defer lb.mu.Unlock()
	return append([]byte(nil), lb.buf...)
}

// Clear empties the scrollback (the on-disk log is kept).
func (lb *LogBuffer) Clear() {
	lb.mu.Lock()
	lb.buf = nil
	lb.mu.Unlock()
}

// Close releases the log file.
func (lb *LogBuffer) Close() {
	lb.mu.Lock()
	if lb.file != nil {
		lb.file.Close()
		lb.file = nil
	}
	lb.mu.Unlock()
}

// Tail returns the last n non-empty plain lines, each cut to max runes. It
// only looks at the end of the scrollback, so it is cheap enough to call on
// every state broadcast.
func (lb *LogBuffer) Tail(n, max int) []string {
	lb.mu.Lock()
	start := len(lb.buf) - 16<<10
	if start < 0 {
		start = 0
	}
	raw := append([]byte(nil), lb.buf[start:]...)
	lb.mu.Unlock()
	lines := PlainLines(raw, 0)
	out := make([]string, 0, n)
	for i := len(lines) - 1; i >= 0 && len(out) < n; i-- {
		l := strings.TrimRight(lines[i], " \t")
		if strings.TrimSpace(l) == "" {
			continue
		}
		if r := []rune(l); len(r) > max {
			l = string(r[:max-1]) + "…"
		}
		out = append(out, l)
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

// Lines returns the last n lines as plain text, with escape sequences removed
// and carriage-return redraws (progress bars) collapsed to their final state.
func (lb *LogBuffer) Lines(n int) []string {
	return PlainLines(lb.Raw(), n)
}

var ansiRe = regexp.MustCompile(`\x1b(\[[0-9;?<=>!]*[ -/]*[@-~]|\][^\x07\x1b]*(\x07|\x1b\\)|[PX^_][^\x1b]*\x1b\\|[()][0-9A-Za-z]|[@-Z\\-_])`)

// StripANSI removes terminal escape sequences.
func StripANSI(s string) string { return ansiRe.ReplaceAllString(s, "") }

// PlainLines converts raw terminal output to readable lines.
func PlainLines(raw []byte, n int) []string {
	text := StripANSI(string(raw))
	text = strings.ReplaceAll(text, "\r\n", "\n")
	lines := strings.Split(text, "\n")
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		if i := strings.LastIndex(l, "\r"); i >= 0 {
			l = l[i+1:]
		}
		l = strings.Map(func(r rune) rune {
			if r < 0x20 && r != '\t' {
				return -1
			}
			return r
		}, l)
		out = append(out, l)
	}
	for len(out) > 0 && strings.TrimSpace(out[len(out)-1]) == "" {
		out = out[:len(out)-1]
	}
	if n > 0 && len(out) > n {
		out = out[len(out)-n:]
	}
	return out
}
