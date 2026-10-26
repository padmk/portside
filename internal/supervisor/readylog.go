package supervisor

import (
	"bytes"
	"regexp"
)

// readyLogLine bounds the unfinished line kept while waiting for a newline.
const readyLogLine = 64 << 10

// readyMatcher marks p ready by its log once a whole line of its output
// (ended by a newline) matches re. Its watch method is a LogBuffer.WatchLines
// function: it runs under the log buffer's lock, so it only sets an atomic,
// and line, the unfinished line between chunks, is read under that lock too.
type readyMatcher struct {
	p    *proc
	re   *regexp.Regexp
	line []byte
}

// newReadyMatcher starts matching with pending, an unfinished line carried
// over from the daemon before an upgrade.
func newReadyMatcher(p *proc, re *regexp.Regexp, pending string) *readyMatcher {
	return &readyMatcher{p: p, re: re, line: []byte(pending)}
}

func (m *readyMatcher) watch(chunk []byte) bool {
	m.line = append(m.line, chunk...)
	for {
		i := bytes.IndexByte(m.line, '\n')
		if i < 0 {
			break
		}
		if m.re.MatchString(PlainLine(string(m.line[:i]))) {
			m.p.logReady.Store(true)
			return true
		}
		m.line = m.line[i+1:]
	}
	if len(m.line) > readyLogLine {
		m.line = m.line[len(m.line)-readyLogLine:]
	}
	return false
}

// installReadyLog makes p wait for a line matching pattern (checked when the
// config loaded), continuing the unfinished line pending; "" stops waiting.
func installReadyLog(p *proc, pattern, pending string) {
	p.readyMatch = nil
	if pattern != "" {
		if re, err := regexp.Compile(pattern); err == nil {
			p.readyMatch = newReadyMatcher(p, re, pending)
			p.logs.WatchLines(p.readyMatch.watch)
			return
		}
		p.logReady.Store(true)
	}
	p.logs.WatchLines(nil)
}
