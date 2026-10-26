package remote

import (
	"net/http"
	"testing"

	"github.com/padmk/portside/internal/csp"
	"github.com/padmk/portside/internal/share"
)

// A reply from another computer is served on the dashboard's origin, so it
// must not run script there, set cookies or site data, or redirect, whatever
// headers that computer sends.
func TestSanitizeRelayed(t *testing.T) {
	h := http.Header{}
	h.Set("Content-Type", "text/html")
	h.Set("Content-Security-Policy", "script-src *")
	for _, k := range []string{share.HeaderProof, share.HeaderEpoch, "Set-Cookie", "Clear-Site-Data", "Refresh", "Link", "Content-Security-Policy-Report-Only"} {
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
	for _, k := range []string{share.HeaderProof, share.HeaderEpoch, "Set-Cookie", "Clear-Site-Data", "Refresh", "Link", "Content-Security-Policy-Report-Only"} {
		if v := h.Get(k); v != "" {
			t.Errorf("%s passed through: %q", k, v)
		}
	}
	if h.Get("Content-Type") != "text/html" {
		t.Error("Content-Type was changed")
	}
}
