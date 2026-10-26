package remote

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

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
