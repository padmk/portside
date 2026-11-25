package server

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/padmk/portside/internal/config"
	"github.com/padmk/portside/internal/share"
	"github.com/padmk/portside/internal/signing"
	"github.com/padmk/portside/internal/supervisor"
	"github.com/padmk/portside/internal/tailnet"
)

// A Stop given while a bulk restart waits for a server to exit wins: the
// server stays stopped.
func TestStopDuringBulkRestartWins(t *testing.T) {
	state := t.TempDir()
	cfg := &config.Config{Shell: "sh", Port: 7999, Servers: []config.Server{{
		Name: "web", Dir: t.TempDir(), Enabled: true, Restart: config.RestartNo, Stop: 5 * time.Second,
		Cmd: `trap 'sleep 1; exit 0' INT; echo up; while :; do sleep 0.1; done`,
	}}}
	sup := supervisor.New(cfg, state)
	defer sup.Shutdown()
	token := signing.RandomHex(32)
	s := New(Options{Supervisor: sup, Share: share.New(7999, state, tailnet.Preloaded(&tailnet.Status{})), StateDir: state, Token: token, Port: 7999})
	post := func(path, body string) int {
		r := httptest.NewRequest(http.MethodPost, path, bytes.NewReader([]byte(body)))
		r.Host = "127.0.0.1:7999"
		signing.Sign(r.Header, token, r.Method, r.RequestURI, []byte(body), s.epoch)
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		return w.Code
	}
	view := func() supervisor.View { return sup.Snapshot(7999)[0] }
	waitFor := func(what string, ok func(supervisor.View) bool) {
		t.Helper()
		for deadline := time.Now().Add(5 * time.Second); !ok(view()); time.Sleep(20 * time.Millisecond) {
			if time.Now().After(deadline) {
				t.Fatalf("%s: %+v", what, view())
			}
		}
	}
	if err := sup.Start("web", supervisor.StartOptions{}); err != nil {
		t.Fatal(err)
	}
	waitFor("running", func(v supervisor.View) bool { return v.PID != 0 })
	time.Sleep(300 * time.Millisecond) // the trap is set
	first := view().PID
	done := make(chan int, 1)
	go func() { done <- post("/api/bulk", `{"action":"restart","names":["web"]}`) }()
	waitFor("stopping", func(v supervisor.View) bool { return v.Status == supervisor.Stopping })
	if code := post("/api/servers/web/stop", "{}"); code != http.StatusOK {
		t.Fatalf("stop: %d", code)
	}
	if code := <-done; code != http.StatusOK {
		t.Fatalf("bulk restart: %d", code)
	}
	time.Sleep(200 * time.Millisecond)
	if v := view(); v.Status != supervisor.Stopped || v.Wanted || (v.PID != 0 && v.PID != first) {
		t.Fatalf("after the restart and the stop: %s, wanted %v, pid %d", v.Status, v.Wanted, v.PID)
	}
}
