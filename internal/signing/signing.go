// Package signing signs HTTP requests with a shared key and verifies them.
//
// A request signature is an HMAC-SHA256, keyed by a 32-byte hex key, over
// the method, the request URI as sent on the wire (escaped path and query),
// the Unix time, a random nonce, the body hash and an optional epoch. The
// verifier checks the signature, a time window and that the nonce is new, so
// a listener posing as the verifying side receives nothing reusable beyond a
// single replay of a request the user made anyway, within the window.
//
// Paired devices sign with their device key (package share); the CLI, MCP
// server and menu bar app sign requests to their own daemon with its API
// token (package server).
package signing

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

// Header returns the name of a product header: "X-<product>-<name>".
func Header(name string) string { return "X-" + app.Name + "-" + name }

// Header names of a signed request.
var (
	HeaderDate      = Header("Date")
	HeaderNonce     = Header("Nonce")
	HeaderSignature = Header("Signature")
	// HeaderEpoch names what a request is meant for when the verifier has
	// several lives (a sharing listener); signatures cover it.
	HeaderEpoch = Header("Epoch")
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

// Signed reports whether a request carries a signature.
func Signed(h http.Header) bool { return h.Get(HeaderSignature) != "" }

// Sign adds the signature headers for a request to h. uri is the request
// URI as sent on the wire; epoch may be empty.
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
// covers is the request's epoch header; callers that need a particular
// epoch check that header themselves.
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
