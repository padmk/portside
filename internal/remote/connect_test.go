package remote

import (
	"context"
	"errors"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/padmk/portside/internal/share"
	"github.com/padmk/portside/internal/tailnet"
)

// connectManager returns a manager paired with devbox whose Connect reaches
// devbox's daemon through reach, with a transport that never reaches the
// network.
func connectManager(t *testing.T, reach func(port int) (reached, error)) *Manager {
	t.Helper()
	self := tailnet.Node{Name: "mbp", DNSName: "mbp.t.ts.net", UserID: 1}
	m := New(7878, t.TempDir(), tailnet.Preloaded(&tailnet.Status{Self: self, Peers: []tailnet.Node{devbox}}))
	if err := m.tokens.Set(devbox.DNSName, testCred()); err != nil {
		t.Fatal(err)
	}
	ports := make(chan int, 4)
	ports <- 1111
	ports <- 2222
	m.reach = func(context.Context, tailnet.Node, share.Credential) (reached, error) {
		return reach(<-ports)
	}
	t.Cleanup(m.Disconnect)
	return m
}

func offlineTransport() *http.Transport {
	fail := func(context.Context, string, string) (net.Conn, error) { return nil, errors.New("offline") }
	return &http.Transport{DialContext: fail, DialTLSContext: fail}
}

// blockingReach answers port 1111 only once released, any other at once.
func blockingReach(arrived chan<- struct{}, release <-chan struct{}) func(int) (reached, error) {
	return func(port int) (reached, error) {
		if port == 1111 {
			arrived <- struct{}{}
			<-release
		}
		return reached{port: port, tr: offlineTransport(), data: []byte("{}")}, nil
	}
}

// A Disconnect while a Connect still reaches the other computer wins: the
// Connect installs nothing and saves nothing to resume.
func TestDisconnectDuringConnectWins(t *testing.T) {
	arrived, release := make(chan struct{}), make(chan struct{})
	m := connectManager(t, blockingReach(arrived, release))
	done := make(chan error, 1)
	go func() {
		_, err := m.Connect(context.Background(), "devbox")
		done <- err
	}()
	<-arrived
	m.Disconnect()
	close(release)
	select {
	case err := <-done:
		if err == nil {
			t.Error("Connect reported success after the Disconnect")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Connect did not return")
	}
	if info := m.Info(); info != nil {
		t.Errorf("connected after the Disconnect: %+v", info)
	}
	if s, ok := m.store.connection(); ok {
		t.Errorf("the connection is saved for resuming: %+v", s)
	}
}

// Of two overlapping Connects the later one wins, also when the earlier
// one's reply comes last.
func TestLaterConnectWins(t *testing.T) {
	arrived, release := make(chan struct{}), make(chan struct{})
	m := connectManager(t, blockingReach(arrived, release))
	first := make(chan error, 1)
	go func() {
		_, err := m.Connect(context.Background(), "devbox")
		first <- err
	}()
	<-arrived
	if _, err := m.Connect(context.Background(), "devbox"); err != nil {
		t.Fatal(err)
	}
	close(release)
	select {
	case err := <-first:
		if err == nil {
			t.Error("the earlier Connect reported success after the later one")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the earlier Connect did not return")
	}
	m.mu.Lock()
	port := 0
	if m.c != nil {
		port = m.c.port
	}
	m.mu.Unlock()
	if port != 2222 {
		t.Errorf("connected to port %d, want the later Connect's 2222", port)
	}
}
