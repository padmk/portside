package share

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/padmk/portside/internal/signing"
)

func testDevice(t *testing.T) (Device, Credential) {
	t.Helper()
	d := newDevices(t.TempDir())
	c, err := d.NewCode()
	if err != nil {
		t.Fatal(err)
	}
	id, key, err := d.Pair(c.Code, mbp)
	if err != nil {
		t.Fatal(err)
	}
	dev, _ := d.Lookup(id)
	return dev, Credential{ID: id, Key: key}
}

func signedRequest(cred Credential, method, uri, body, epoch string) *http.Request {
	r := httptest.NewRequest(method, uri, strings.NewReader(body))
	cred.Sign(r.Header, method, uri, []byte(body), epoch)
	return r
}

func TestVerify(t *testing.T) {
	dev, cred := testDevice(t)
	other := Credential{ID: cred.ID, Key: strings.Repeat("ab", 32)}
	stale := func() *http.Request {
		r := signedRequest(cred, "GET", "/api/state", "", "e1")
		old := strconv.FormatInt(time.Now().Add(-10*time.Minute).Unix(), 10)
		// Re-sign with the old date, so only the date is wrong.
		r.Header.Set(signing.HeaderDate, old)
		r.Header.Set(signing.HeaderSignature, signing.MAC(cred.Key, "GET", "/api/state", old, r.Header.Get(signing.HeaderNonce), signing.BodyHash(nil), "e1"))
		return r
	}
	cases := []struct {
		name string
		req  *http.Request
		uri  string
		body string
		ok   bool
	}{
		{"valid", signedRequest(cred, "POST", "/api/servers/web/stop", "{}", "e1"), "/api/servers/web/stop", "{}", true},
		{"query and escaping", signedRequest(cred, "GET", "/api/servers/a%20b/logs?n=2", "", "e1"), "/api/servers/a%20b/logs?n=2", "", true},
		{"other body", signedRequest(cred, "POST", "/api/servers/web/stop", "{}", "e1"), "/api/servers/web/stop", `{"x":1}`, false},
		{"other path", signedRequest(cred, "POST", "/api/servers/web/stop", "{}", "e1"), "/api/servers/db/stop", "{}", false},
		{"other key", signedRequest(other, "GET", "/api/state", "", "e1"), "/api/state", "", false},
		{"old date", stale(), "/api/state", "", false},
	}
	n := &signing.Verifier{}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := verify(n, c.req, dev, c.uri, []byte(c.body))
			if (err == nil) != c.ok {
				t.Errorf("verify: %v, want ok=%v", err, c.ok)
			}
		})
	}
}

func TestVerifyRefusesReplay(t *testing.T) {
	dev, cred := testDevice(t)
	n := &signing.Verifier{}
	r := signedRequest(cred, "GET", "/api/state", "", "e1")
	if err := verify(n, r, dev, "/api/state", nil); err != nil {
		t.Fatal(err)
	}
	if err := verify(n, r, dev, "/api/state", nil); err == nil {
		t.Error("the same signed request verified twice")
	}
}

func TestSignatureCoversEpoch(t *testing.T) {
	dev, cred := testDevice(t)
	r := signedRequest(cred, "GET", "/api/state", "", "e1")
	r.Header.Set(HeaderEpoch, "e2")
	if err := verify(&signing.Verifier{}, r, dev, "/api/state", nil); err == nil {
		t.Error("a request signed for one epoch verified with another")
	}
}

func TestProof(t *testing.T) {
	dev, cred := testDevice(t)
	h := http.Header{}
	ch := Challenge(h)
	reply := http.Header{}
	e1 := signing.NewEpoch()
	signing.Prove(reply, h, dev.Key, e1)
	if epoch, ok := cred.Proven(ch, reply); !ok || epoch != e1 {
		t.Errorf("Proven = %q, %v", epoch, ok)
	}
	forged := reply.Clone()
	forged.Set(HeaderEpoch, signing.NewEpoch()) // a proof cannot be moved to another epoch
	if _, ok := cred.Proven(ch, forged); ok {
		t.Error("proof accepted for another epoch")
	}
	if _, ok := cred.Proven(Challenge(http.Header{}), reply); ok {
		t.Error("proof accepted for another challenge")
	}
	if _, ok := cred.Proven(ch, http.Header{}); ok {
		t.Error("a reply without proof was accepted")
	}
	if _, ok := (Credential{ID: cred.ID}).Proven(ch, reply); ok {
		t.Error("a credential without a key accepted a proof")
	}
}
