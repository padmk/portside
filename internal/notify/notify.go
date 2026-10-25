// Package notify signals subscribers that something changed.
package notify

import "sync"

// Signals is a set of change channels. Each channel holds at most one
// pending signal, so a slow subscriber sees one wake-up for any number of
// changes and a notifier never blocks. The zero value is ready to use.
type Signals struct {
	mu   sync.Mutex
	subs map[chan struct{}]struct{}
}

// Subscribe returns a channel signalled on every Notify.
func (s *Signals) Subscribe() chan struct{} {
	ch := make(chan struct{}, 1)
	s.mu.Lock()
	if s.subs == nil {
		s.subs = map[chan struct{}]struct{}{}
	}
	s.subs[ch] = struct{}{}
	s.mu.Unlock()
	return ch
}

// Unsubscribe stops signals to ch; a channel that is not subscribed is ignored.
func (s *Signals) Unsubscribe(ch chan struct{}) {
	s.mu.Lock()
	delete(s.subs, ch)
	s.mu.Unlock()
}

// Notify signals every subscriber that has no signal pending.
func (s *Signals) Notify() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for ch := range s.subs {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}
