package share

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/padmk/portside/internal/app"
)

// Request signing between paired daemons. The connecting device signs the
// method, request URI, time, a nonce and the body hash with its key; this
// daemon checks the signature, the time window and that the nonce is new.
// A listener posing as this daemon therefore receives nothing reusable
// beyond a single replay of a request the user made anyway. The challenge
// and proof headers let the connecting device check that the daemon it
// talks to holds the key too, before it trusts what that daemon says.

func header(name string) string { return "X-" + app.Name + "-" + name }

// Header names; each is "X-<product>-<name>".
var (
	HeaderDevice    = header("Device")
	HeaderDate      = header("Date")
	HeaderNonce     = header("Nonce")
	HeaderSignature = header("Signature")
	HeaderChallenge = header("Challenge")
	HeaderProof     = header("Proof")
	// HeaderEpoch names the listener a request is meant for. Each listener
	// has a random epoch, sent with the proof, and signatures cover it, so a
	// request sent to a listener that no longer exists is useless elsewhere.
	HeaderEpoch = header("Epoch")
)

const (
	signWindow = 2 * time.Minute // allowed clock difference
	nonceTTL   = 2*signWindow + time.Minute
)

// Credential is what a connecting device keeps after pairing.
type Credential struct {
	ID  string `json:"id"`
	Key string `json:"key"`
}

func mac(key string, parts ...string) string {
	k, _ := hex.DecodeString(key)
	h := hmac.New(sha256.New, k)
	h.Write([]byte(strings.Join(parts, "\n")))
	return hex.EncodeToString(h.Sum(nil))
}

// BodyHash is the hex SHA-256 of a request body.
func BodyHash(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// Sign adds the signature headers for a request to h. uri is the request
// URI as sent on the wire (escaped path and query); epoch is the one the
// daemon last proved, empty before the first proof.
func (c Credential) Sign(h http.Header, method, uri string, body []byte, epoch string) {
	date := strconv.FormatInt(time.Now().Unix(), 10)
	nonce := randomHex(16)
	h.Set(HeaderDevice, c.ID)
	h.Set(HeaderDate, date)
	h.Set(HeaderNonce, nonce)
	h.Set(HeaderEpoch, epoch)
	h.Set(HeaderSignature, mac(c.Key, method, uri, date, nonce, BodyHash(body), epoch))
}

// Challenge adds a fresh challenge to h and returns it.
func Challenge(h http.Header) string {
	ch := randomHex(16)
	h.Set(HeaderChallenge, ch)
	return ch
}

// Proven reports whether a reply proves that the daemon holds c's key, and
// returns the listener epoch the proof vouches for.
func (c Credential) Proven(challenge string, reply http.Header) (string, bool) {
	if !validKey(c.Key) || challenge == "" {
		return "", false
	}
	epoch := reply.Get(HeaderEpoch)
	want := mac(c.Key, "server", challenge, epoch)
	return epoch, hmac.Equal([]byte(want), []byte(reply.Get(HeaderProof)))
}

func proof(key, challenge, epoch string) string { return mac(key, "server", challenge, epoch) }

// nonces remembers recently used nonces to refuse replays.
type nonces struct {
	mu   sync.Mutex
	seen map[string]time.Time
}

func (n *nonces) fresh(nonce string) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	now := time.Now()
	if n.seen == nil {
		n.seen = map[string]time.Time{}
	}
	if len(n.seen) > 4096 {
		for k, exp := range n.seen {
			if now.After(exp) {
				delete(n.seen, k)
			}
		}
	}
	if exp, ok := n.seen[nonce]; ok && now.Before(exp) {
		return false
	}
	n.seen[nonce] = now.Add(nonceTTL)
	return true
}

var errSignature = errors.New("bad signature")

// verify checks a signed request against dev's key. uri is the request URI
// as the device sent it.
func (n *nonces) verify(r *http.Request, dev Device, uri string, body []byte) error {
	date, err := strconv.ParseInt(r.Header.Get(HeaderDate), 10, 64)
	if err != nil {
		return errSignature
	}
	if d := time.Since(time.Unix(date, 0)); d > signWindow || d < -signWindow {
		return errors.New("the clocks of the two computers differ by more than two minutes")
	}
	nonce := r.Header.Get(HeaderNonce)
	if len(nonce) < 16 || len(nonce) > 64 {
		return errSignature
	}
	want := mac(dev.Key, r.Method, uri, r.Header.Get(HeaderDate), nonce, BodyHash(body), r.Header.Get(HeaderEpoch))
	if !validKey(dev.Key) || !hmac.Equal([]byte(want), []byte(r.Header.Get(HeaderSignature))) {
		return errSignature
	}
	if !n.fresh(nonce) {
		return errors.New("replayed request")
	}
	return nil
}
