package supervisor

import (
	"bytes"
	"regexp"
	"time"
)

// readyLogLine bounds the unfinished line kept while waiting for a newline.
const readyLogLine = 64 << 10

// watchReadyLog marks generation gen of p ready by its log once a line of
// its output matches re. It reads the log subscription ch, opened before the
// process started so no early line is missed, and ends at a match or when
// that generation has exited.
func (s *Supervisor) watchReadyLog(p *proc, gen int, re *regexp.Regexp, ch chan []byte) {
	defer p.logs.Unsubscribe(ch)
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	var line []byte
	for {
		select {
		case chunk := <-ch:
			line = append(line, chunk...)
			for {
				i := bytes.IndexByte(line, '\n')
				if i < 0 {
					break
				}
				if re.MatchString(PlainLine(string(line[:i]))) {
					s.markLogReady(p, gen)
					return
				}
				line = line[i+1:]
			}
			// A prompt or progress line may never end with a newline.
			if re.MatchString(PlainLine(string(line))) {
				s.markLogReady(p, gen)
				return
			}
			if len(line) > readyLogLine {
				line = line[len(line)-readyLogLine:]
			}
		case <-tick.C:
			s.mu.Lock()
			gone := p.gen != gen || p.pid == 0
			s.mu.Unlock()
			if gone {
				return
			}
		}
	}
}

func (s *Supervisor) markLogReady(p *proc, gen int) {
	s.mu.Lock()
	if p.gen == gen {
		p.logReady = true
	}
	s.mu.Unlock()
}
