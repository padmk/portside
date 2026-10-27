package remote

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/padmk/portside/internal/csp"
	"github.com/padmk/portside/internal/share"
	"github.com/padmk/portside/internal/signing"
)

// A reply from another computer is served on the dashboard's origin, so it
// must not run script there, set cookies or site data, or redirect, whatever
// headers that computer sends.
func TestSanitizeRelayed(t *testing.T) {
	h := http.Header{}
	h.Set("Content-Type", "text/html")
	h.Set("Content-Security-Policy", "script-src *")
	for _, k := range []string{share.HeaderProof, share.HeaderEpoch, "Set-Cookie", "Clear-Site-Data", "Refresh", "Location", "Link", "Content-Security-Policy-Report-Only"} {
		h.Set(k, "x")
	}
	sanitizeRelayed(h)
	if got := h.Get("Content-Security-Policy"); got != csp.Untrusted {
		t.Errorf("Content-Security-Policy = %q, want %q", got, csp.Untrusted)
	}
	if got := h.Values("Content-Security-Policy"); len(got) != 1 {
		t.Errorf("Content-Security-Policy has %d values, want the sandbox alone", len(got))
	}
	if h.Get("X-Content-Type-Options") != "nosniff" {
		t.Error("X-Content-Type-Options is not nosniff")
	}
	for _, k := range []string{share.HeaderProof, share.HeaderEpoch, "Set-Cookie", "Clear-Site-Data", "Refresh", "Location", "Link", "Content-Security-Policy-Report-Only"} {
		if v := h.Get(k); v != "" {
			t.Errorf("%s passed through: %q", k, v)
		}
	}
	if h.Get("Content-Type") != "text/html" {
		t.Error("Content-Type was changed")
	}
}

// A relayed reply carries X-Content-Type-Options once, although both the
// daemon's handler and the relay set it.
func TestRelayedReplyHasOneNosniff(t *testing.T) {
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		signing.Prove(w.Header(), r.Header, testCred().Key, signing.NewEpoch())
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("{}"))
	}))
	defer peer.Close()
	m, _ := connected(t)
	// The peer is reached over HTTPS; this one answers plain HTTP.
	tr := &http.Transport{DialTLSContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "tcp", peer.Listener.Addr().String())
	}}
	c := newConn(m, devbox, 7878, testCred(), tr)
	m.mu.Lock()
	m.c = c
	m.mu.Unlock()

	w := httptest.NewRecorder()
	w.Header().Set("X-Content-Type-Options", "nosniff") // as the daemon's handler does
	m.ServeAPI(w, httptest.NewRequest(http.MethodGet, PathPrefix+"/devbox.t.ts.net/state", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	if got := w.Header().Values("X-Content-Type-Options"); len(got) != 1 || got[0] != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q, want nosniff once", got)
	}
}

// TestRelayedReplyIsBounded checks that a reply from the other computer
// larger than maxRelayReply is never relayed whole: one that says its size
// is refused, and a stream is cut off, so this daemon never holds more.
func TestRelayedReplyIsBounded(t *testing.T) {
	const size = maxRelayReply + 1<<20
	for _, declared := range []bool{true, false} {
		t.Run(fmt.Sprintf("declared=%v", declared), func(t *testing.T) {
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				signing.Prove(w.Header(), r.Header, testCred().Key, signing.NewEpoch())
				w.Header().Set("Content-Type", "application/json")
				if declared {
					w.Header().Set("Content-Length", strconv.Itoa(size))
				}
				block := make([]byte, 32<<10)
				for n := 0; n < size; n += len(block) {
					if _, err := w.Write(block); err != nil {
						return
					}
				}
			}))
			defer peer.Close()
			m := relayTo(t, peer)
			local := httptest.NewServer(http.HandlerFunc(m.ServeAPI))
			defer local.Close()

			res, err := http.Get(local.URL + PathPrefix + "/devbox.t.ts.net/state")
			if err != nil {
				return // cut off before the headers
			}
			defer res.Body.Close()
			n, _ := io.Copy(io.Discard, res.Body)
			if declared && res.StatusCode != http.StatusBadGateway {
				t.Errorf("status %d for a declared %d-byte reply, want 502", res.StatusCode, size)
			}
			if n > maxRelayReply+1<<20 || n >= size {
				t.Errorf("relayed %d bytes of a %d-byte reply", n, size)
			}
		})
	}
}

// TestRelayedReplyIdleIsAborted checks that a reply body that stops arriving
// is aborted after relayIdle instead of holding the request open.
func TestRelayedReplyIdleIsAborted(t *testing.T) {
	defer func(d time.Duration) { relayIdle = d }(relayIdle)
	relayIdle = 200 * time.Millisecond
	stall := make(chan struct{})
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		signing.Prove(w.Header(), r.Header, testCred().Key, signing.NewEpoch())
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"partial":`))
		w.(http.Flusher).Flush()
		select {
		case <-stall:
		case <-r.Context().Done():
		}
	}))
	defer peer.Close()
	defer close(stall) // before peer.Close, which waits for the handler
	m := relayTo(t, peer)
	local := httptest.NewServer(http.HandlerFunc(m.ServeAPI))
	defer local.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		if res, err := http.Get(local.URL + PathPrefix + "/devbox.t.ts.net/state"); err == nil {
			_, _ = io.Copy(io.Discard, res.Body)
			res.Body.Close()
		}
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("a stalled reply body kept the relayed request open")
	}
}

// TestPeerCallDoesNotFollowRedirect checks that a redirect from the other
// computer is not followed with the signed request.
func TestPeerCallDoesNotFollowRedirect(t *testing.T) {
	calls := 0
	tr := roundTrip(func(r *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: http.StatusTemporaryRedirect, Header: http.Header{"Location": {"https://other.example/next"}},
			Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
	})
	cred := testCred()
	_, _, _ = call(context.Background(), tr, devbox, 7878, &cred, "epoch", http.MethodPost, "/api/pair", map[string]string{"code": "x"}, nil)
	if calls != 1 {
		t.Fatalf("made %d requests, want one", calls)
	}
}

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// TestNullStateIsIgnored checks that a remote state that is not an object
// neither replaces the last state nor breaks the stream to the dashboard.
func TestNullStateIsIgnored(t *testing.T) {
	m, c := connected(t)
	var rs remoteState
	_ = json.Unmarshal([]byte("null"), &rs)
	c.apply([]byte("null"), &rs)
	defer func() {
		if p := recover(); p != nil {
			t.Fatalf("a null state panics: %v", p)
		}
	}()
	if data := m.wrapped(c); !strings.Contains(string(data), `"remote"`) {
		t.Errorf("wrapped = %s, want the connection", data)
	}
}

// relayTo connects a manager to peer as the other computer.
func relayTo(t *testing.T, peer *httptest.Server) *Manager {
	t.Helper()
	m, _ := connected(t)
	tr := &http.Transport{DialTLSContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "tcp", peer.Listener.Addr().String())
	}}
	c := newConn(m, devbox, 7878, testCred(), tr)
	m.mu.Lock()
	m.c = c
	m.mu.Unlock()
	return m
}
