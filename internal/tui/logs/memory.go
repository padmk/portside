package logs

// memorySize is how many servers' read states are kept.
const memorySize = 8

// readState is where a reader was in a server's logs: restored when the
// server is shown again or its stream reconnects, since both load the
// lines again.
type readState struct {
	follow bool
	// at is the bottom line's index from the first line kept and text its
	// plain text, which finds it again when lines came or went; rowOff is
	// its rows below the screen.
	at     int
	text   string
	rowOff int
	query  string
	only   bool
	gutter gutterMode
	stamps stampDecision
	left   int
}

// memory keeps the read states of the latest servers shown, newest last.
type memory struct {
	servers []string
	states  []readState
}

func (m *memory) save(server string, st readState) {
	m.drop(server)
	m.servers, m.states = append(m.servers, server), append(m.states, st)
	if n := len(m.servers) - memorySize; n > 0 {
		m.servers, m.states = m.servers[n:], m.states[n:]
	}
}

// take returns and forgets a server's read state.
func (m *memory) take(server string) (readState, bool) {
	for i, s := range m.servers {
		if s == server {
			st := m.states[i]
			m.drop(server)
			return st, true
		}
	}
	return readState{}, false
}

func (m *memory) drop(server string) {
	for i, s := range m.servers {
		if s == server {
			m.servers = append(m.servers[:i], m.servers[i+1:]...)
			m.states = append(m.states[:i], m.states[i+1:]...)
			return
		}
	}
}

func (p *Pane) readState() readState {
	st := readState{follow: p.follow, query: p.search.query, only: p.search.only,
		gutter: p.gutter, stamps: p.stamps, left: p.left}
	if !p.follow && p.seqLen() > 0 {
		q := p.bottomPos()
		abs := p.seqAt(q.k)
		st.at, st.text, st.rowOff = abs-p.base, p.line(abs).Plain, q.off
	}
	return st
}

// restoreState applies a read state to lines loaded again: the bottom
// line is the one with the same text nearest to where it was.
func (p *Pane) restoreState(st readState) {
	p.gutter, p.left = st.gutter, st.left
	if st.stamps.decided {
		p.stamps = st.stamps
	}
	p.setQuery(st.query)
	p.search.only = st.only && p.search.re != nil
	p.follow = st.follow
	if st.follow || p.buf.Len() == 0 {
		p.follow = true
		return
	}
	abs := p.findLine(st.at, st.text)
	p.anchor, p.rowOff = abs, 0
	if p.seqAt(p.seqIndex(abs)) == abs {
		p.rowOff = st.rowOff
	}
	p.clampAnchor()
}

// findLine is the absolute index of the line with text nearest to index
// at from the first line, else the line at that index.
func (p *Pane) findLine(at int, text string) int {
	n := p.buf.Len()
	at = max(0, min(at, n-1))
	for d := 0; d < n; d++ {
		if i := at - d; i >= 0 && p.buf.At(i).Plain == text {
			return p.base + i
		}
		if i := at + d; d > 0 && i < n && p.buf.At(i).Plain == text {
			return p.base + i
		}
		if at-d < 0 && at+d >= n {
			break
		}
	}
	return p.base + at
}
