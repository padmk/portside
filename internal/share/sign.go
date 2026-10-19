package share

import (
	"crypto/hmac"
	"errors"
	"net/http"

	"github.com/padmk/portside/internal/signing"
)

// Request signing between paired daemons. The connecting device signs its
// requests with its key (package signing: method, request URI, time, nonce,
// body hash and the listener epoch); this daemon checks the signature, the
// time window and that the nonce is new. The challenge and proof headers let
// the connecting device check that the daemon it talks to holds the key too,
// before it trusts what that daemon says.

// Header names; each is "X-<product>-<name>".
var (
	HeaderDevice    = signing.Header("Device")
	HeaderChallenge = signing.Header("Challenge")
	HeaderProof     = signing.Header("Proof")
	// HeaderEpoch names the listener a request is meant for. Each listener
	// has a random epoch, sent with the proof, and signatures cover it, so a
	// request sent to a listener that no longer exists is useless elsewhere.
	HeaderEpoch = signing.HeaderEpoch
)

// Credential is what a connecting device keeps after pairing.
type Credential struct {
	ID  string `json:"id"`
	Key string `json:"key"`
}

// Sign adds the signature headers for a request to h. uri is the request
// URI as sent on the wire (escaped path and query); epoch is the one the
// daemon last proved, empty before the first proof.
func (c Credential) Sign(h http.Header, method, uri string, body []byte, epoch string) {
	h.Set(HeaderDevice, c.ID)
	signing.Sign(h, c.Key, method, uri, body, epoch)
}

// Challenge adds a fresh challenge to h and returns it.
func Challenge(h http.Header) string {
	ch := signing.RandomHex(16)
	h.Set(HeaderChallenge, ch)
	return ch
}

// Proven reports whether a reply proves that the daemon holds c's key, and
// returns the listener epoch the proof vouches for.
func (c Credential) Proven(challenge string, reply http.Header) (string, bool) {
	if !signing.ValidKey(c.Key) || challenge == "" {
		return "", false
	}
	epoch := reply.Get(HeaderEpoch)
	return epoch, hmac.Equal([]byte(proof(c.Key, challenge, epoch)), []byte(reply.Get(HeaderProof)))
}

func proof(key, challenge, epoch string) string { return signing.MAC(key, "server", challenge, epoch) }

// verify checks a signed request against dev's key. uri is the request URI
// as the device sent it.
func verify(v *signing.Verifier, r *http.Request, dev Device, uri string, body []byte) error {
	err := v.Verify(r, dev.Key, uri, body)
	if errors.Is(err, signing.ErrClock) {
		return errors.New("the clocks of the two computers differ by more than two minutes")
	}
	return err
}
