package supervisor

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/padmk/portside/internal/config"
)

func TestHealthNeedsSuccessOrRedirect(t *testing.T) {
	for code, ready := range map[int]bool{200: true, 204: true, 302: true, 401: false, 404: false, 503: false} {
		h := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if code == 302 {
				w.Header().Set("Location", "/login")
			}
			w.WriteHeader(code)
		}))
		port := h.Listener.Addr().(*net.TCPAddr).Port
		_, reason := probeReady(port, "/health")
		h.Close()
		if (reason == "") != ready {
			t.Errorf("health answering %d: reason %q, want ready %v", code, reason, ready)
		}
	}
}

// startedFor starts web and reports its status after d.
func startedFor(t *testing.T, s *Supervisor, d time.Duration) Status {
	t.Helper()
	if err := s.Start("web", StartOptions{}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(d)
	return s.Snapshot(19000)[0].Status
}

func TestReadyLogHoldsStartingUntilTheLine(t *testing.T) {
	s := lifecycleSupervisor(t, "echo booting; sleep 3; echo 'Worker ready'; sleep 60", config.RestartNo)
	s.procs["web"].cfg.ReadyLog = `Worker ready$`
	if st := startedFor(t, s, 2*time.Second); st != Starting {
		t.Fatalf("status before the line: %s", st)
	}
	if err := s.WaitReady(context.Background(), "web", 5*time.Second); err != nil {
		t.Fatal(err)
	}
	if st := s.Snapshot(19000)[0].Status; st != Running {
		t.Fatalf("status after the line: %s", st)
	}
}

func TestReadyAfterDelaysRunning(t *testing.T) {
	s := lifecycleSupervisor(t, "sleep 60", config.RestartNo)
	s.procs["web"].cfg.ReadyAfter = 3 * time.Second
	if st := startedFor(t, s, 2*time.Second); st != Starting {
		t.Fatalf("status before ready_after: %s", st)
	}
	if err := s.WaitReady(context.Background(), "web", 5*time.Second); err != nil {
		t.Fatal(err)
	}
}
