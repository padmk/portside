package supervisor

import (
	"bytes"
	"regexp"
)

// readyLogLine bounds the unfinished line kept while waiting for a newline.
const readyLogLine = 64 << 10

// readyLogWatch returns a LogBuffer.WatchLines function that marks p ready
// by its log once a line of its output matches re. It keeps the unfinished
// line between chunks and matches it too, since a prompt may never end with
// a newline. It runs under the log buffer's lock, so it only sets an atomic.
func readyLogWatch(p *proc, re *regexp.Regexp) func([]byte) bool {
	var line []byte
	return func(chunk []byte) bool {
		line = append(line, chunk...)
		for {
			i := bytes.IndexByte(line, '\n')
			if i < 0 {
				break
			}
			if re.MatchString(PlainLine(string(line[:i]))) {
				p.logReady.Store(true)
				return true
			}
			line = line[i+1:]
		}
		if re.MatchString(PlainLine(string(line))) {
			p.logReady.Store(true)
			return true
		}
		if len(line) > readyLogLine {
			line = line[len(line)-readyLogLine:]
		}
		return false
	}
}
