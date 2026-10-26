package share

import (
	"context"
	"log"
	"sync"
	"time"
)

// Presence: which paired devices are connected now and when each was last
// seen, for the Computers window, and one desktop notification per new
// connection. A connection is any signed request outside hello (which
// discovery sends too); a connected daemon keeps its event stream open, so
// a device counts as connected while it has such a request in flight.

// quietGap is how long a device must have been away for its next
// connection to be announced: reconnecting after a network change or a
// restart of either daemon is not news.
const quietGap = 5 * time.Minute

type liveDevice struct {
	ctx    context.Context // ends when the device is revoked
	cancel context.CancelFunc
	active int       // connection requests in flight
	seen   time.Time // last start or end of one
	since  time.Time // start of the announced connection; zero until one
}

// DeviceStatus is a paired device as the dashboard and the app show it.
type DeviceStatus struct {
	Device
	// Connected is true while the device has a connection open.
	Connected bool `json:"connected,omitempty"`
	// Since is when the device's announced connection began; a client that
	// notifies the user itself announces each new value once.
	Since time.Time `json:"since,omitzero"`
}

// SetNotifier sets the function that shows a desktop notification.
func (m *Manager) SetNotifier(fn func(title, msg string)) {
	m.mu.Lock()
	m.notifier = fn
	m.mu.Unlock()
}

// Subscribe returns a channel signalled when a device pairs, connects,
// disconnects or is removed; Unsubscribe stops it.
func (m *Manager) Subscribe() chan struct{}     { return m.changes.Subscribe() }
func (m *Manager) Unsubscribe(ch chan struct{}) { m.changes.Unsubscribe(ch) }

func (m *Manager) announce(title, msg string) {
	m.mu.Lock()
	fn := m.notifier
	m.mu.Unlock()
	if fn != nil {
		fn(title, msg)
	}
}

// liveLocked returns the live record of dev, made on its first request.
func (m *Manager) liveLocked(dev Device) *liveDevice {
	if m.live == nil {
		m.live = map[string]*liveDevice{}
	}
	d := m.live[dev.ID]
	if d == nil {
		ctx, cancel := context.WithCancel(context.Background())
		d = &liveDevice{ctx: ctx, cancel: cancel, seen: dev.LastSeen}
		m.live[dev.ID] = d
	}
	return d
}

// attach registers a request of dev and returns a context that ends when
// dev is revoked, and the function to call when the request ends. A request
// that counts as a connection marks dev connected until it ends; the first
// one after quietGap without any is announced.
func (m *Manager) attach(dev Device, counts bool) (context.Context, func()) {
	m.mu.Lock()
	d := m.liveLocked(dev)
	if !counts {
		m.mu.Unlock()
		return d.ctx, func() {}
	}
	now := time.Now()
	first := d.active == 0
	fresh := first && (d.seen.IsZero() || now.Sub(d.seen) > quietGap)
	d.active++
	d.seen = now
	if fresh {
		d.since = now
	}
	m.mu.Unlock()
	if first {
		m.seen(dev.ID, now)
	}
	if fresh {
		m.announce(dev.Name+" connected", "It is using this computer's servers.")
	}
	var once sync.Once
	return d.ctx, func() {
		once.Do(func() {
			now := time.Now()
			m.mu.Lock()
			d.active--
			d.seen = now
			last := d.active == 0
			m.mu.Unlock()
			if last {
				m.seen(dev.ID, now)
			}
		})
	}
}

// seen keeps when a device connected or disconnected and tells subscribers.
func (m *Manager) seen(id string, at time.Time) {
	if err := m.devs.touch(id, at); err != nil {
		log.Printf("share: %v", err)
	}
	m.changes.Notify()
}

// deviceStatus lists the paired devices with their connections.
func (m *Manager) deviceStatus() []DeviceStatus {
	devs := m.devs.List()
	out := make([]DeviceStatus, len(devs))
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, dev := range devs {
		out[i] = DeviceStatus{Device: dev}
		if d := m.live[dev.ID]; d != nil {
			out[i].Connected = d.active > 0
			out[i].Since = d.since
		}
	}
	return out
}

// endRevoked ends the requests in flight, WebSockets included, of devices
// that are no longer paired. The listener stays: moving to a new one would
// leave Tailscale Serve pointing at a freed port for a moment.
func (m *Manager) endRevoked() {
	paired := map[string]bool{}
	for _, d := range m.devs.List() {
		paired[d.ID] = true
	}
	m.mu.Lock()
	for id, d := range m.live {
		if !paired[id] {
			d.cancel()
			delete(m.live, id)
		}
	}
	m.mu.Unlock()
	m.changes.Notify()
}
