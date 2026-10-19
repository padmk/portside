// Package signing signs HTTP requests with a shared key and verifies them.
//
// A request signature is an HMAC-SHA256, keyed by a 32-byte hex key, over
// the method, the request URI as sent on the wire (escaped path and query),
// the Unix time, a random nonce, the body hash and an epoch. The verifier
// checks the signature, a time window, that the nonce is new and that the
// epoch is its own.
//
// An epoch is random per verifier instance (a daemon start, a sharing
// listener), so a request captured by a program posing as the verifier is
// refused by the real one, whose epoch differs. A signer learns the epoch
// from a proof: it sends a fresh random challenge, and the verifier answers
// with its epoch and HMAC(key, "server", challenge, epoch). A signer sends
// signed requests only after checking such a proof, and checks the proof on
// replies too, so a listener without the key gets no signature at all. A
// whole reply is proved with HMAC(key, "server", challenge, epoch, status,
// body hash), so a relay cannot alter it either; WebSocket upgrades carry
// the short proof.
//
// Every message is newline-joined parts that never contain a newline
// themselves, and each use has its own number of parts: a session value 2,
// a proof 3, a reply proof 5, a request signature 6. One kind of MAC can
// therefore never be taken for another.
//
// Paired devices sign with their device key (package share); the CLI, MCP
// server and menu bar app sign requests to their own daemon with its API
// token (package server).
package signing

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/padmk/portside/internal/app"
)

// Header returns the name of a product header: "X-<product>-<name>".
func Header(name string) string { return "X-" + app.Name + "-" + name }

// Header names of a signed request.
var (
	HeaderDate      = Header("Date")
	HeaderNonce     = Header("Nonce")
	HeaderSignature = Header("Signature")
	// HeaderEpoch names the verifier instance a request is meant for;
	// signatures cover it. Replies carry it next to the proof.
	HeaderEpoch = Header("Epoch")
	// HeaderChallenge carries a signer's fresh challenge; HeaderProof the
	// verifier's answer to it.
	HeaderChallenge = Header("Challenge")
	HeaderProof     = Header("Proof")
)

const (
	// Window is the allowed difference between the signing and the
	// verifying clock.
	Window   = 2 * time.Minute
	nonceTTL = 2*Window + time.Minute
	// MaxBody bounds a signed request body, which is read whole to be hashed.
	MaxBody = 8 << 20
)

// ErrSignature means a signature is missing, malformed or wrong.
var ErrSignature = errors.New("bad signature")

// ErrClock means a request was signed outside the time window.
var ErrClock = errors.New("the request time differs from this computer's clock by more than two minutes")

// ErrReplay means a nonce was used before.
var ErrReplay = errors.New("replayed request")

// ErrEpoch means a request was signed for another instance of the verifier,
// such as a daemon that has restarted since.
var ErrEpoch = errors.New("signed for another instance; ask for a new proof")

// ValidKey reports whether k is a full 32-byte hex key; nothing else may
// sign or verify.
func ValidKey(k string) bool {
	b, err := hex.DecodeString(k)
	return err == nil && len(b) == 32
}

// MAC returns the hex HMAC-SHA256 of parts joined by newlines, keyed by the
// hex key.
func MAC(key string, parts ...string) string {
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

// RandomHex returns n random bytes, hex encoded.
func RandomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// NewEpoch returns a random epoch for a verifier instance.
func NewEpoch() string { return RandomHex(16) }

// validToken reports whether s is 16 to 32 random bytes, hex encoded: the
// form of challenges and epochs. Nothing else enters a proof, so a proof can
// never double as a request signature.
func validToken(s string) bool {
	if len(s) < 32 || len(s) > 64 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

// Challenge adds a fresh challenge to h and returns it.
func Challenge(h http.Header) string {
	ch := RandomHex(16)
	h.Set(HeaderChallenge, ch)
	return ch
}

// Prove answers the challenge of request header req in reply header h: the
// epoch and a proof that the replying side holds key. A malformed challenge
// gets no answer.
func Prove(h, req http.Header, key, epoch string) {
	ch := req.Get(HeaderChallenge)
	if !ValidKey(key) || !validToken(ch) || !validToken(epoch) {
		return
	}
	h.Set(HeaderEpoch, epoch)
	h.Set(HeaderProof, proof(key, ch, epoch))
}

// Proven reports whether reply header h proves that the replying side holds
// key, for challenge, and returns the epoch the proof vouches for.
func Proven(key, challenge string, h http.Header) (string, bool) {
	epoch := h.Get(HeaderEpoch)
	if !ValidKey(key) || !validToken(challenge) || !validToken(epoch) {
		return "", false
	}
	return epoch, hmac.Equal([]byte(proof(key, challenge, epoch)), []byte(h.Get(HeaderProof)))
}

func proof(key, challenge, epoch string) string { return MAC(key, "server", challenge, epoch) }

// ProveReply answers the challenge of request header req in reply header h
// for a whole reply: the epoch and a proof over the challenge, the epoch,
// the status and the body. A malformed challenge gets no answer.
func ProveReply(h, req http.Header, key, epoch string, status int, body []byte) {
	ch := req.Get(HeaderChallenge)
	if !ValidKey(key) || !validToken(ch) || !validToken(epoch) {
		return
	}
	h.Set(HeaderEpoch, epoch)
	h.Set(HeaderProof, replyProof(key, ch, epoch, status, body))
}

// ProvenReply reports whether reply header h proves, for challenge, that the
// replying side holds key and sent this status and body, and returns the
// epoch the proof vouches for.
func ProvenReply(key, challenge string, h http.Header, status int, body []byte) (string, bool) {
	epoch := h.Get(HeaderEpoch)
	if !ValidKey(key) || !validToken(challenge) || !validToken(epoch) {
		return "", false
	}
	return epoch, hmac.Equal([]byte(replyProof(key, challenge, epoch, status, body)), []byte(h.Get(HeaderProof)))
}

func replyProof(key, challenge, epoch string, status int, body []byte) string {
	return MAC(key, "server", challenge, epoch, strconv.Itoa(status), BodyHash(body))
}

// CheckEpoch reports ErrEpoch unless request header h names epoch.
func CheckEpoch(h http.Header, epoch string) error {
	if epoch == "" || subtle.ConstantTimeCompare([]byte(h.Get(HeaderEpoch)), []byte(epoch)) != 1 {
		return ErrEpoch
	}
	return nil
}

// Signed reports whether a request carries a signature.
func Signed(h http.Header) bool { return h.Get(HeaderSignature) != "" }

// Sign adds the signature headers for a request to h. uri is the request
// URI as sent on the wire; epoch is the one the verifier last proved.
func Sign(h http.Header, key, method, uri string, body []byte, epoch string) {
	date := strconv.FormatInt(time.Now().Unix(), 10)
	nonce := RandomHex(16)
	h.Set(HeaderDate, date)
	h.Set(HeaderNonce, nonce)
	if epoch != "" {
		h.Set(HeaderEpoch, epoch)
	} else {
		h.Del(HeaderEpoch)
	}
	h.Set(HeaderSignature, MAC(key, method, uri, date, nonce, BodyHash(body), epoch))
}

// Verifier checks signed requests and remembers recent nonces to refuse
// replays. The zero value is ready to use.
type Verifier struct {
	mu   sync.Mutex
	seen map[string]time.Time
}

func (v *Verifier) fresh(nonce string) bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	now := time.Now()
	if v.seen == nil {
		v.seen = map[string]time.Time{}
	}
	if len(v.seen) > 4096 {
		for k, exp := range v.seen {
			if now.After(exp) {
				delete(v.seen, k)
			}
		}
	}
	if exp, ok := v.seen[nonce]; ok && now.Before(exp) {
		return false
	}
	v.seen[nonce] = now.Add(nonceTTL)
	return true
}

// Verify checks a signed request against key. uri is the request URI as the
// signer sent it and body the whole request body. The epoch the signature
// covers is the request's epoch header; callers check it with CheckEpoch.
func (v *Verifier) Verify(r *http.Request, key, uri string, body []byte) error {
	if !ValidKey(key) {
		return ErrSignature
	}
	date, err := strconv.ParseInt(r.Header.Get(HeaderDate), 10, 64)
	if err != nil {
		return ErrSignature
	}
	if d := time.Since(time.Unix(date, 0)); d > Window || d < -Window {
		return ErrClock
	}
	nonce := r.Header.Get(HeaderNonce)
	if len(nonce) < 16 || len(nonce) > 64 {
		return ErrSignature
	}
	want := MAC(key, r.Method, uri, r.Header.Get(HeaderDate), nonce, BodyHash(body), r.Header.Get(HeaderEpoch))
	if !hmac.Equal([]byte(want), []byte(r.Header.Get(HeaderSignature))) {
		return ErrSignature
	}
	// Only checked once the signature holds, so others cannot fill the cache.
	if !v.fresh(nonce) {
		return ErrReplay
	}
	return nil
}
