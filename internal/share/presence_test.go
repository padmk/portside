package share

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// recorder collects the notification titles a manager shows.
type recorder struct {
	mu     sync.Mutex
	titles []string
}

func (r *recorder) notify(title, _ string) {
	r.mu.Lock()
	r.titles = append(r.titles, title)
	r.mu.Unlock()
}

func (r *recorder) count(suffix string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, t := range r.titles {
		if strings.HasSuffix(t, suffix) {
			n++
		}
	}
	return n
}

func TestPairingIsAnnounced(t *testing.T) {
	f := newGuard(t)
	var rec recorder
	f.m.SetNotifier(rec.notify)
	c, err := f.m.devs.NewCode()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.m.Pair(Peer{Node: testStatus().Peers[0]}, c.Code, "1"); err != nil {
		t.Fatal(err)
	}
	if n := rec.count(" paired"); n != 1 {
		t.Errorf("%d pairing notifications, want 1: %v", n, rec.titles)
	}
	if _, err := f.m.Pair(Peer{Node: testStatus().Peers[0]}, "WRONGCODE", "2"); err == nil {
		t.Fatal("a wrong code paired")
	}
	if n := rec.count(" paired"); n != 1 {
		t.Errorf("a failed pairing was announced: %v", rec.titles)
	}
}

func TestConnectionAnnouncedOncePerNewConnection(t *testing.T) {
	f := newGuard(t)
	var rec recorder
	f.m.SetNotifier(rec.notify)
	release := make(chan struct{})
	started := make(chan struct{}, 4)
	f.m.SetHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/events" {
			started <- struct{}{}
			<-release
		}
	}))
	serve := func(path string) int {
		w := httptest.NewRecorder()
		f.m.guard().ServeHTTP(w, request(path, signed(f.cred, epoch)))
		return w.Code
	}
	away := func() {
		f.m.mu.Lock()
		f.m.live[f.cred.ID].seen = time.Now().Add(-2 * quietGap)
		f.m.mu.Unlock()
	}
	connected := func() DeviceStatus {
		t.Helper()
		for _, d := range f.m.Status().Devices {
			if d.ID == f.cred.ID {
				return d
			}
		}
		t.Fatal("paired device not listed")
		return DeviceStatus{}
	}

	// Right after pairing, which was announced itself, the first
	// connection is not announced again.
	if serve("/api/state") != 200 {
		t.Fatal("request refused")
	}
	if n := rec.count(" connected"); n != 0 {
		t.Errorf("connection right after pairing announced: %v", rec.titles)
	}

	// Away for longer than quietGap: the event stream opening is announced,
	// and requests while it stays open are not, even once it has been open
	// for longer than quietGap.
	away()
	go serve("/api/events")
	<-started
	away()
	for range 5 {
		serve("/api/state")
		serve(HelloPath)
	}
	if n := rec.count(" connected"); n != 1 {
		t.Errorf("%d connection notifications, want 1: %v", n, rec.titles)
	}
	d := connected()
	if !d.Connected || d.Since.IsZero() {
		t.Errorf("while the stream is open: %+v", d)
	}
	release <- struct{}{}
	deadline := time.Now().Add(2 * time.Second)
	for connected().Connected && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if d := connected(); d.Connected || time.Since(d.LastSeen) > time.Minute {
		t.Errorf("after the stream ended: %+v", d)
	}

	// Reconnecting soon after is the same connection.
	close(release)
	serve("/api/events")
	if n := rec.count(" connected"); n != 1 {
		t.Errorf("a quick reconnection was announced: %v", rec.titles)
	}

	// Hello alone (discovery) neither connects nor is announced.
	away()
	serve(HelloPath)
	if n := rec.count(" connected"); n != 1 || connected().Connected {
		t.Errorf("hello counted as a connection: %v", rec.titles)
	}
	serve("/api/state")
	if n := rec.count(" connected"); n != 2 {
		t.Errorf("%d connection notifications after being away again, want 2: %v", n, rec.titles)
	}

	// Last seen is kept in the state file for the next daemon.
	d = connected()
	again := newDevices(filepath.Dir(f.m.devs.path))
	stored, ok := again.Lookup(f.cred.ID)
	if !ok || !stored.LastSeen.Equal(d.LastSeen) || time.Since(stored.LastSeen) > time.Minute {
		t.Errorf("stored last seen %v, listed %v", stored.LastSeen, d.LastSeen)
	}
}
