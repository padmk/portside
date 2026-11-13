package logs

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/coder/websocket"

	"github.com/padmk/portside/internal/client"
	"github.com/padmk/portside/internal/tui/ui"
)

const (
	// retryDelay is the wait before a lost stream connects again.
	retryDelay = 2 * time.Second
	// timesWait bounds how long a new connection waits for the times of
	// its scrollback before it shows the lines without them.
	timesWait = 3 * time.Second
	// maxPending is how much output waiting for the UI is kept when it
	// falls behind: past twice that, the oldest output goes, from a line
	// start, and the pane shows a gap.
	maxPending = 1 << 20
)

// feedMsg is everything a stream received since the last one: with reset,
// a new connection's scrollback (raw, as the terminal stream replays it)
// replacing the pane's lines and the timed lines of the logs reply that
// date it, then live output; gap is set when output was dropped before
// data, err while the stream is lost. id is the feed's, unique across
// every pane of the program, so a message reaches only the pane whose
// feed sent it (Pane.Owns).
type feedMsg struct {
	id      uint64
	reset   bool
	backlog []byte
	times   []Line
	data    []byte
	gap     bool
	err     error
}

// feed collects a stream's output for the UI, coalescing what arrives
// between two reads into one message.
type feed struct {
	id     uint64
	ctx    context.Context
	cancel context.CancelFunc
	ready  chan struct{}

	mu      sync.Mutex
	reset   bool
	backlog []byte
	times   []Line
	data    []byte
	gap     bool
	err     error
	// panicked is a panic of one of the feed's goroutines; the next
	// message is it, and the root quits on it.
	panicked *ui.PanicMsg
}

// feedIDs numbers the feeds of every pane: two panes (a container's Logs
// and Job tabs, or a server's pane and a new container's) never share an
// id, so neither takes the other's output.
var feedIDs atomic.Uint64

// newFeed returns a feed with a new id, ending with ctx or close.
func newFeed(ctx context.Context) *feed {
	ctx, cancel := context.WithCancel(ctx)
	return &feed{id: feedIDs.Add(1), ctx: ctx, cancel: cancel, ready: make(chan struct{}, 1)}
}

// openFeed starts streaming a server's logs until close or the end of
// ctx: the terminal WebSocket replays the scrollback and continues with
// live output, so the two join without a gap or a repeat; on a lost
// connection it tries again after retryDelay.
func openFeed(ctx context.Context, c *client.Client, server string) *feed {
	f := newFeed(ctx)
	go func() { //nolint:contextcheck // the feed runs under f.ctx, derived from ctx
		defer ui.RecoverWith(f.publishPanic)
		f.run(c, server)
	}()
	return f
}

func (f *feed) close() { f.cancel() }

func (f *feed) signal() {
	select {
	case f.ready <- struct{}{}:
	default:
	}
}

func (f *feed) publishReset(backlog []byte, times []Line, data []byte, gap bool) {
	f.mu.Lock()
	f.reset, f.backlog, f.times, f.err = true, backlog, times, nil
	f.data, f.gap = nil, false
	f.addData(data, gap)
	f.mu.Unlock()
	f.signal()
}

func (f *feed) publishData(b []byte) {
	f.mu.Lock()
	f.addData(b, false)
	f.mu.Unlock()
	f.signal()
}

// addData queues output, under f.mu.
func (f *feed) addData(b []byte, gap bool) {
	f.data = append(f.data, b...)
	f.gap = f.gap || gap
	if len(f.data) > 2*maxPending {
		f.data, _ = dropOldest(f.data, maxPending)
		f.gap = true
	}
}

// dropOldest keeps at most keep bytes of the end of data, starting after
// a line end so that no line, character or escape sequence is cut; false
// when no line end is in that part and all of it goes.
func dropOldest(data []byte, keep int) ([]byte, bool) {
	tail := data[len(data)-keep:]
	i := bytes.IndexByte(tail, '\n')
	if i < 0 {
		return data[:0], false
	}
	return append(data[:0], tail[i+1:]...), true
}

// publishPanic ends the feed with a panic of one of its goroutines.
func (f *feed) publishPanic(p ui.PanicMsg) {
	f.mu.Lock()
	if f.panicked == nil {
		f.panicked = &p
	}
	f.mu.Unlock()
	f.cancel()
	f.signal()
}

func (f *feed) publishErr(err error) {
	f.mu.Lock()
	f.err = err
	f.mu.Unlock()
	f.signal()
}

// wait is the command that delivers the next message; the pane runs it
// again after each one.
func (f *feed) wait() tea.Cmd {
	return func() tea.Msg {
		select {
		case <-f.ready:
		case <-f.ctx.Done():
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.panicked != nil {
			return *f.panicked
		}
		if f.ctx.Err() != nil {
			return nil
		}
		m := feedMsg{id: f.id, reset: f.reset, backlog: f.backlog, times: f.times, data: f.data, gap: f.gap, err: f.err}
		f.reset, f.backlog, f.times, f.data, f.gap, f.err = false, nil, nil, nil, false, nil
		return m
	}
}

func (f *feed) run(c *client.Client, server string) {
	if ref, ok := strings.CutPrefix(server, containerKey); ok {
		f.runContainer(c, ref)
		return
	}
	path := "/api/servers/" + url.PathEscape(server)
	if ref, ok := strings.CutPrefix(server, jobKey); ok {
		ref, _, _ = strings.Cut(ref, "\x00")
		path = "/api/containers/" + url.PathEscape(ref) + "/job"
	}
	for f.ctx.Err() == nil {
		if err := f.connect(c, path); err != nil && f.ctx.Err() == nil {
			f.publishErr(err)
		}
		select {
		case <-f.ctx.Done():
			return
		case <-time.After(retryDelay):
		}
	}
}

type wsRead struct {
	data []byte
	err  error
}

// connect streams one connection until it fails. The first message is
// the scrollback (always=1 sends it even when empty); while the logs
// reply with its times loads, live output is held back so it follows the
// scrollback.
func (f *feed) connect(c *client.Client, path string) error {
	ctx, cancel := context.WithCancel(f.ctx)
	defer cancel()
	ws, err := c.DialWebSocket(ctx, path+"/terminal?readonly=1&always=1")
	if err != nil {
		return err
	}
	defer ws.CloseNow()
	ws.SetReadLimit(-1)
	_, backlog, err := ws.Read(ctx)
	if err != nil {
		return err
	}
	times := make(chan []Line, 1)
	go func() {
		defer ui.RecoverWith(f.publishPanic)
		var h []Line
		// A compose job's output has no logs reply to date it.
		if !strings.HasSuffix(path, "/job") {
			h, _ = fetchHistory(ctx, c, path)
		}
		times <- h
	}()
	reads := make(chan wsRead)
	go func() {
		defer ui.RecoverWith(f.publishPanic)
		for {
			_, data, err := ws.Read(ctx)
			select {
			case reads <- wsRead{data, err}:
			case <-ctx.Done():
				return
			}
			if err != nil {
				return
			}
		}
	}()
	var early []byte
	gap := false
	timer := time.NewTimer(timesWait)
	defer timer.Stop()
	for waiting := true; waiting; {
		select {
		case h := <-times:
			f.publishReset(backlog, h, early, gap)
			waiting = false
		case <-timer.C:
			f.publishReset(backlog, nil, early, gap)
			waiting = false
		case r := <-reads:
			if r.err != nil {
				return r.err
			}
			early = append(early, r.data...)
			if len(early) > 2*maxPending {
				early, _ = dropOldest(early, maxPending)
				gap = true
			}
		}
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case r := <-reads:
			if r.err != nil {
				return r.err
			}
			f.publishData(r.data)
		}
	}
}

// fetchHistory loads the latest lines with their times; only the times
// are used, to date the scrollback.
func fetchHistory(ctx context.Context, c *client.Client, path string) ([]Line, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var body bytes.Buffer
	q := url.Values{"lines": {strconv.Itoa(maxLines)}, "timestamps": {"1"}}
	if err := c.Do(ctx, http.MethodGet, path+"/logs?"+q.Encode(), nil, &body); err != nil {
		return nil, err
	}
	return parseHistory(body.String(), time.Now()), nil
}

// parseHistory reads a logs reply with timestamps: bracketed notes first
// (left out: they say which lines the reply holds, and only the times of
// the lines matter here), then lines that start with "15:04:05" (today), "01-02 15:04:05" or eight
// spaces when the time is unknown, and one space.
func parseHistory(text string, now time.Time) []Line {
	text = strings.TrimSuffix(text, "\n")
	if text == "" {
		return nil
	}
	rows := strings.Split(text, "\n")
	i := 0
	for i < len(rows) && strings.HasPrefix(rows[i], "[") {
		i++
	}
	out := make([]Line, 0, len(rows)-i)
	for _, row := range rows[i:] {
		at, rest := parseStamp(row, now)
		out = append(out, Clean(rest, at))
	}
	return out
}

func parseStamp(row string, now time.Time) (time.Time, string) {
	loc := now.Location()
	if len(row) >= 15 && row[14] == ' ' {
		if t, err := time.ParseInLocation("01-02 15:04:05", row[:14], loc); err == nil {
			year := now.Year()
			if t.Month() > now.Month() {
				year-- // a date later in the year than today is from last year
			}
			return time.Date(year, t.Month(), t.Day(), t.Hour(), t.Minute(), t.Second(), 0, loc), row[15:]
		}
	}
	if len(row) >= 9 && row[8] == ' ' {
		if row[:8] == "        " {
			return time.Time{}, row[9:]
		}
		if t, err := time.ParseInLocation("15:04:05", row[:8], loc); err == nil {
			return time.Date(now.Year(), now.Month(), now.Day(), t.Hour(), t.Minute(), t.Second(), 0, loc), row[9:]
		}
	}
	return time.Time{}, row
}

// Keys of the pane's other sources: no server name starts with a NUL, so
// these never meet a server's read state or its LogsClearedMsg.
const (
	containerKey = "\x00container:"
	jobKey       = "\x00job:"
)

// ShowContainer switches the pane to a container's output (ref is its
// name or id): the engine's stream, which follows the container by name
// across stops, starts and recreation and marks each. Each container
// keeps its read state as a server does. The pane's server actions
// (attach, clear-logs) do not apply to it; the caller handles them.
func (p *Pane) ShowContainer(env *ui.Env, ref string) tea.Cmd {
	if ref == "" {
		return p.Show(env, "")
	}
	cmd := p.Show(env, containerKey+ref)
	// The engine sends no times, so a time column would date the whole
	// tail with the moment it arrived; t shows it for live output.
	if p.restore == nil {
		p.gutter = gutterHidden
	}
	return cmd
}

// ShowJob switches the pane to the output of the compose job of the
// container ref; job tells one job from the next (its start time), so a
// new job opens a new stream.
func (p *Pane) ShowJob(env *ui.Env, ref, job string) tea.Cmd {
	if ref == "" {
		return p.Show(env, "")
	}
	return p.Show(env, jobKey+ref+"\x00"+job)
}

// runContainer streams a container's output until close, connecting
// again after retryDelay when the connection fails.
func (f *feed) runContainer(c *client.Client, ref string) {
	for f.ctx.Err() == nil {
		if err := f.connectContainer(c, ref); err != nil && f.ctx.Err() == nil {
			f.publishErr(err)
		}
		select {
		case <-f.ctx.Done():
			return
		case <-time.After(retryDelay):
		}
	}
}

// connectContainer streams one connection of a container's logs: binary
// messages are output (the latest lines first), text messages the
// stream's status, of which an engine error becomes a red line. Each
// connection replaces the lines with the tail it sends.
func (f *feed) connectContainer(c *client.Client, ref string) error {
	ctx, cancel := context.WithCancel(f.ctx)
	defer cancel()
	ws, err := c.DialWebSocket(ctx, "/api/containers/"+url.PathEscape(ref)+"/logs")
	if err != nil {
		return err
	}
	defer ws.CloseNow()
	ws.SetReadLimit(-1)
	f.publishReset(nil, nil, nil, false)
	for {
		typ, data, err := ws.Read(ctx)
		if err != nil {
			return err
		}
		if typ == websocket.MessageText {
			var st struct {
				T string `json:"t"`
				D string `json:"d"`
			}
			if json.Unmarshal(data, &st) == nil && st.T == "error" && st.D != "" {
				f.publishData([]byte("\x1b[31m" + st.D + "\x1b[0m\r\n"))
			}
			continue
		}
		f.publishData(data)
	}
}
