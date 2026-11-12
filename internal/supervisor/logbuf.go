package supervisor

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/padmk/portside/internal/paths"
)

const (
	logBufferSize = 1 << 20  // in-memory scrollback per server
	logFileLimit  = 10 << 20 // rotate the on-disk log after this many bytes
)

// restartMarker follows the history a starting daemon restored, once: a
// server that wrote nothing since the last one gets no second marker.
const restartMarker = "\x1b[2m== daemon restarted ==\x1b[0m\r\n"

// LogBuffer keeps a server's raw terminal output: a bounded in-memory tail for
// replay into terminals, an on-disk copy, and fan-out to live subscribers.
type LogBuffer struct {
	mu      sync.Mutex
	buf     []byte
	marks   []mark // when lines began, at most one per second
	file    *os.File
	path    string
	written int64
	subs    map[chan []byte]struct{}
	// watch sees every chunk as it is written, until it returns true.
	watch func(chunk []byte) bool
	// previousWrite is when the file was last written before this buffer
	// opened it, by the daemon before this one; zero without a file.
	previousWrite time.Time
}

// mark records that the line starting at off in buf began at at. A line
// without its own mark began within a second after the mark before it.
type mark struct {
	off int
	at  time.Time
}

func newLogBuffer(dir, name string) *LogBuffer {
	lb := &LogBuffer{subs: map[chan []byte]struct{}{}}
	if dir != "" {
		_ = os.MkdirAll(dir, paths.DirMode)
		lb.path = filepath.Join(dir, name+".log")
		if st, err := os.Stat(lb.path); err == nil {
			lb.previousWrite = st.ModTime()
		}
		lb.seedTail()
		lb.openFile()
		if len(lb.buf) > 0 && !bytes.HasSuffix(lb.buf, []byte(restartMarker)) {
			marker := restartMarker
			if !bytes.HasSuffix(lb.buf, []byte("\n")) {
				marker = "\r\n" + marker
			}
			_, _ = lb.Write([]byte(marker))
		}
	}
	return lb
}

// seedTail reads bounded history without replaying it to disk or assigning
// old output the current daemon's timestamps.
func (lb *LogBuffer) seedTail() {
	const limit = 256 << 10
	readTail := func(path string, n int) []byte {
		f, err := os.Open(path)
		if err != nil {
			return nil
		}
		defer f.Close()
		st, err := f.Stat()
		if err != nil {
			return nil
		}
		off := max(st.Size()-int64(n), 0)
		_, _ = f.Seek(off, io.SeekStart)
		data, _ := io.ReadAll(io.LimitReader(f, int64(n)))
		if off > 0 {
			if i := bytes.IndexByte(data, '\n'); i >= 0 {
				data = data[i+1:]
			}
		}
		return data
	}
	current := readTail(lb.path, limit)
	if len(current) < limit {
		lb.buf = readTail(lb.path+".1", limit-len(current))
	}
	lb.buf = append(lb.buf, current...)
}

// NewMemoryLog returns a buffer that is kept only in memory.
func NewMemoryLog() *LogBuffer { return newLogBuffer("", "") }

// Path is the on-disk log file, "" for a log kept only in memory.
func (lb *LogBuffer) Path() string {
	lb.mu.Lock()
	defer lb.mu.Unlock()
	return lb.path
}

func (lb *LogBuffer) retarget(dir, name string) {
	lb.mu.Lock()
	defer lb.mu.Unlock()
	if lb.file != nil {
		lb.file.Close()
		lb.file = nil
	}
	lb.path = filepath.Join(dir, name+".log")
	lb.openFile()
	if lb.file != nil {
		n, _ := lb.file.Write(lb.buf)
		lb.written += int64(n)
	}
}

func (lb *LogBuffer) openFile() {
	f, err := os.OpenFile(lb.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, paths.FileMode)
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
	lb.markLines(chunk, time.Now())
	lb.buf = append(lb.buf, chunk...)
	if len(lb.buf) > logBufferSize {
		cut := len(lb.buf) - logBufferSize*3/4
		if i := bytes.IndexByte(lb.buf[cut:], '\n'); i >= 0 {
			cut += i + 1
		}
		lb.buf = append([]byte(nil), lb.buf[cut:]...)
		lb.shiftMarks(cut)
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
	if lb.watch != nil && lb.watch(chunk) {
		lb.watch = nil
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

// Locked runs f under the buffer's lock, as WatchLines functions run.
func (lb *LogBuffer) Locked(f func()) {
	lb.mu.Lock()
	defer lb.mu.Unlock()
	f()
}

// WatchLines calls fn with every chunk written from now on, under the
// buffer's lock so that, unlike a subscription, it misses no output; it
// stops once fn returns true or another WatchLines (nil for none) replaces it.
func (lb *LogBuffer) WatchLines(fn func(chunk []byte) bool) {
	lb.mu.Lock()
	lb.watch = fn
	lb.mu.Unlock()
}

// markLines records the time of the lines chunk starts, before it is
// appended to buf.
func (lb *LogBuffer) markLines(chunk []byte, now time.Time) {
	add := func(off int) {
		if n := len(lb.marks); n > 0 && now.Sub(lb.marks[n-1].at) < time.Second {
			return
		}
		lb.marks = append(lb.marks, mark{off, now})
	}
	base := len(lb.buf)
	if base == 0 || lb.buf[base-1] == '\n' {
		add(base)
	}
	for i, b := range chunk {
		if b == '\n' && i+1 < len(chunk) {
			add(base + i + 1)
		}
	}
}

// shiftMarks follows a cut of the first n bytes of buf. The last mark
// before the cut moves to the new start, which it still dates.
func (lb *LogBuffer) shiftMarks(n int) {
	keep := 0
	for keep < len(lb.marks) && lb.marks[keep].off <= n {
		keep++
	}
	if keep > 0 {
		keep--
	}
	lb.marks = append([]mark(nil), lb.marks[keep:]...)
	for i := range lb.marks {
		lb.marks[i].off = max(lb.marks[i].off-n, 0)
	}
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
	lb.buf, lb.marks = nil, nil
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

// Line is one line of plain output and when it began; At is zero when
// unknown.
type Line struct {
	At   time.Time
	Text string
}

// TimedLines returns the scrollback as plain lines with their times.
func (lb *LogBuffer) TimedLines() []Line {
	lb.mu.Lock()
	raw := append([]byte(nil), lb.buf...)
	marks := append([]mark(nil), lb.marks...)
	lb.mu.Unlock()
	return plainRows(raw, marks)
}

// ansiRe matches one terminal escape sequence: CSI, OSC (ended by BEL or
// ST), a DCS, SOS, PM or APC string, an nF sequence (intermediates such as
// the charset designators "(" and ")" or "#", then a final byte), or any
// other two-byte escape ESC 0x30..0x7E, which covers DECSC "ESC 7", DECRC
// "ESC 8", keypad modes "ESC =" and "ESC >", "ESC c", "ESC D" and "ESC M".
var ansiRe = regexp.MustCompile(`\x1b(\[[0-?]*[ -/]*[@-~]|\][^\x07\x1b]*(\x07|\x1b\\)|[PX^_][^\x1b]*\x1b\\|[ -/]+[0-~]|[0-~])`)

// ANSIPattern is the pattern of one terminal escape sequence, for clients
// that clean raw terminal output themselves.
var ANSIPattern = ansiRe

// StripANSI removes terminal escape sequences.
func StripANSI(s string) string { return ansiRe.ReplaceAllString(s, "") }

// PlainLines converts raw terminal output to readable lines.
func PlainLines(raw []byte, n int) []string {
	lines := plainRows(raw, nil)
	if n > 0 && len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = l.Text
	}
	return out
}

// maxCursorUp bounds how many earlier rows one line can move back over.
const maxCursorUp = 100

// plainRows converts raw terminal output to plain lines stamped from
// marks, without trailing blank lines. Each line goes through PlainLine;
// a line that moves the cursor up (CSI n A or CSI n F, outside a cursor
// save and restore) is written over the row it moved to instead of being
// appended, as docker compose redraws the rows it keeps free for its key
// bar. An overwrite that leaves no text keeps the row's earlier text.
func plainRows(raw []byte, marks []mark) []Line {
	var out []Line
	row, m := 0, 0
	var at time.Time
	for off := 0; off < len(raw); {
		end := bytes.IndexByte(raw[off:], '\n')
		if end < 0 {
			end = len(raw)
		} else {
			end += off
		}
		for m < len(marks) && marks[m].off <= off {
			at = marks[m].at
			m++
		}
		l := string(raw[off:end])
		row = max(row-CursorUp(l), 0)
		text := PlainLine(l)
		switch {
		case row == len(out):
			out = append(out, Line{At: at, Text: text})
		case text != "":
			out[row] = Line{At: at, Text: text}
		}
		row++
		off = end + 1
	}
	for len(out) > 0 && strings.TrimSpace(out[len(out)-1].Text) == "" {
		out = out[:len(out)-1]
	}
	return out
}

// CursorUp returns how many rows a raw line moves the cursor up outside
// a cursor save and restore, at most maxCursorUp.
func CursorUp(l string) int {
	if !strings.Contains(l, "\x1b[") || !strings.ContainsAny(l, "AF") {
		return 0
	}
	up := 0
	saved := false
	for _, loc := range ansiRe.FindAllStringIndex(l, -1) {
		seq := l[loc[0]:loc[1]]
		switch seq {
		case "\x1b7", "\x1b[s":
			saved = true
			continue
		case "\x1b8", "\x1b[u":
			saved = false
			continue
		}
		if saved || len(seq) < 3 || seq[1] != '[' {
			continue
		}
		if final := seq[len(seq)-1]; final != 'A' && final != 'F' {
			continue
		}
		n := 1
		if p := seq[2 : len(seq)-1]; p != "" {
			v, err := strconv.Atoi(p)
			if err != nil {
				continue
			}
			n = max(v, 1)
		}
		up += n
	}
	return min(up, maxCursorUp)
}

// PlainLine turns one raw line into readable text: escape sequences
// removed, the cursor rules of RedrawLine applied, and other control
// characters dropped.
func PlainLine(l string) string { return RedrawLine(l, nil) }

// RedrawLine returns the text one raw line leaves on its row of the screen,
// with the escape sequences keep accepts left in place and every other
// sequence and control character except tab removed. It follows bounded,
// line-local rules, not a terminal emulation:
//
//   - A carriage return, or a move to the first column (CSI G, CSI 0 G,
//     CSI 1 G), starts the line over: a progress bar's last redraw wins and
//     a shorter redraw leaves no tail. Trailing carriage returns are
//     ignored, since a program's CRLF reaches the log as CR CR LF.
//   - Backspace and CSI n D move the cursor left, CSI n C right and CSI n G
//     to column n; text written there overwrites what is under the cursor,
//     as spinners redraw.
//   - CSI K and CSI 0 K erase to the end of the line, CSI 1 K to the cursor
//     (with spaces) and CSI 2 K the whole line.
//   - Text between a cursor save (CSI s or ESC 7) and the following restore
//     (CSI u or ESC 8) is dropped, together with the sequences inside: it
//     was drawn elsewhere on the screen, as docker compose draws its key
//     bar. A save without a restore on the same line changes nothing.
//   - Other cursor movement (up, down, absolute position) is ignored.
//
// Columns count runes, so wide characters can misplace an overwrite.
func RedrawLine(l string, keep func(seq string) bool) string {
	l = strings.TrimRight(l, "\r")
	if !strings.ContainsAny(l, "\x1b\r\b") {
		return strings.Map(dropControl, l)
	}
	if s, ok := stripLine(l, keep); ok {
		return s
	}
	var ln termLine
	for len(l) > 0 {
		loc := ansiRe.FindStringIndex(l)
		part := l
		if loc != nil {
			part = l[:loc[0]]
		}
		for _, r := range part {
			ln.char(r)
		}
		if loc == nil {
			break
		}
		ln.escape(l[loc[0]:loc[1]], keep)
		l = l[loc[1]:]
	}
	return ln.String()
}

// stripLine is RedrawLine for a line that only writes forward: it reports
// false when the line moves the cursor, so it needs a replay.
func stripLine(l string, keep func(string) bool) (string, bool) {
	var b strings.Builder
	for len(l) > 0 {
		loc := ansiRe.FindStringIndex(l)
		part := l
		if loc != nil {
			part = l[:loc[0]]
		}
		if strings.ContainsAny(part, "\r\b") {
			return "", false
		}
		for _, r := range part {
			if dropControl(r) >= 0 {
				b.WriteRune(r)
			}
		}
		if loc == nil {
			break
		}
		seq := l[loc[0]:loc[1]]
		switch seq {
		case "\x1b[K", "\x1b[0K": // erasing after the last character changes nothing
		default:
			if moves(seq) {
				return "", false
			}
			if keep != nil && keep(seq) {
				b.WriteString(seq)
			}
		}
		l = l[loc[1]:]
	}
	return b.String(), true
}

// moves reports whether RedrawLine interprets an escape sequence.
func moves(seq string) bool {
	switch seq {
	case "\x1b7", "\x1b8", "\x1b[s", "\x1b[u":
		return true
	}
	return len(seq) > 2 && seq[1] == '[' && strings.IndexByte("GDCK", seq[len(seq)-1]) >= 0
}

func dropControl(r rune) rune {
	if r < 0x20 && r != '\t' || r == 0x7f {
		return -1
	}
	return r
}

// cell is one rune on a line and the zero-width sequences kept before it.
type cell struct {
	pre string
	r   rune
}

// termLine is the state of one row while RedrawLine replays it.
type termLine struct {
	cells   []cell
	cursor  int
	pending string // kept sequences not yet followed by a rune
	saved   *termLine
}

func (t *termLine) char(r rune) {
	switch {
	case r == '\r':
		t.erase(0)
		t.cursor = 0
	case r == '\b':
		t.cursor = max(t.cursor-1, 0)
	case dropControl(r) < 0:
	default:
		for len(t.cells) < t.cursor {
			t.cells = append(t.cells, cell{r: ' '})
		}
		if t.cursor == len(t.cells) {
			t.cells = append(t.cells, cell{pre: t.pending, r: r})
		} else {
			t.cells[t.cursor].r = r
			t.cells[t.cursor].pre += t.pending
		}
		t.pending = ""
		t.cursor++
	}
}

// erase removes the cells from column from on, keeping the sequences
// they carried so the style in effect stays as the stream set it.
func (t *termLine) erase(from int) {
	if from >= len(t.cells) {
		return
	}
	var pre strings.Builder
	for _, c := range t.cells[from:] {
		pre.WriteString(c.pre)
	}
	t.pending = pre.String() + t.pending
	t.cells = t.cells[:from]
}

func (t *termLine) escape(seq string, keep func(string) bool) {
	switch seq {
	case "\x1b7", "\x1b[s":
		s := *t
		s.cells = slices.Clone(t.cells)
		s.saved = nil
		t.saved = &s
		return
	case "\x1b8", "\x1b[u":
		if t.saved != nil {
			*t = *t.saved
		}
		return
	}
	if len(seq) > 2 && seq[1] == '[' {
		final := seq[len(seq)-1]
		params := seq[2 : len(seq)-1]
		n := 1
		if params != "" {
			v, err := strconv.Atoi(params)
			if err != nil {
				n = -1 // private, intermediate or several parameters
			} else {
				n = v
			}
		}
		switch {
		case final == 'G' && n >= 0:
			if n <= 1 {
				t.char('\r')
			} else {
				t.cursor = min(n-1, maxRedrawColumn)
			}
			return
		case final == 'D' && n >= 0:
			t.cursor = max(t.cursor-max(n, 1), 0)
			return
		case final == 'C' && n >= 0:
			t.cursor = min(t.cursor+max(n, 1), maxRedrawColumn)
			return
		case final == 'K':
			switch params {
			case "", "0":
				t.erase(t.cursor)
			case "1":
				for i := 0; i < len(t.cells) && i <= t.cursor; i++ {
					t.cells[i].r = ' '
				}
			case "2":
				t.erase(0)
			}
			return
		}
	}
	if keep != nil && keep(seq) {
		t.pending += seq
	}
}

// maxRedrawColumn bounds the padding a cursor move can add to a line.
const maxRedrawColumn = 1000

func (t *termLine) String() string {
	var b strings.Builder
	for _, c := range t.cells {
		b.WriteString(c.pre)
		b.WriteRune(c.r)
	}
	b.WriteString(t.pending)
	return b.String()
}
