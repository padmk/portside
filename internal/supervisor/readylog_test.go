package supervisor

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"regexp"
	"syscall"
	"testing"
	"time"

	"github.com/creack/pty"

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

func TestReadyLogSeesALineInAFlood(t *testing.T) {
	p := &proc{logs: NewMemoryLog()}
	p.logs.WatchLines(newReadyMatcher(p, regexp.MustCompile(`^Worker ready$`), nil).watch)
	for i := 0; i < 20000; i++ {
		fmt.Fprintf(p.logs, "\x1b[2mline %d\x1b[0m\r\n", i)
		if i == 12345 {
			fmt.Fprint(p.logs, "Worker re")
			fmt.Fprint(p.logs, "ady\r\n")
		}
	}
	if !p.logReady.Load() {
		t.Fatal("the ready line was missed")
	}
}

// adopt hands script, running in a terminal, to s as server web, as an
// upgrade in place does with h's fields filled in.
func adopt(t *testing.T, s *Supervisor, script string, h Handoff) {
	t.Helper()
	cmd := exec.Command("sh", "-c", script)
	f, err := pty.Start(cmd)
	if err != nil {
		t.Fatal(err)
	}
	pid := cmd.Process.Pid
	t.Cleanup(func() { _ = syscall.Kill(-pid, syscall.SIGKILL) })
	fd, err := syscall.Dup(int(f.Fd()))
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	h.Name, h.PID, h.PIDStart, h.PTY, h.Want = "web", pid, processStart(pid), fd, true
	if h.StartedAt.IsZero() {
		h.StartedAt = time.Now().Add(-time.Minute)
	}
	if got := s.Adopt([]Handoff{h}); len(got) != 1 {
		t.Fatalf("adopted %v", got)
	}
}

func TestAdoptedServerKeepsWaitingForItsReadyLine(t *testing.T) {
	waiting := false
	for _, c := range []struct {
		name, pending, script string
		ready                 bool
	}{
		{"line still to come", "", "sleep 1; echo 'Worker ready'; sleep 60", true},
		{"line split by the upgrade", "Worker re", "sleep 1; echo 'ady'; sleep 60", true},
		{"pattern after other text", "Not ", "sleep 1; echo 'Worker ready'; sleep 60", false},
		{"a character split by the upgrade", "Za\xc5", `sleep 1; printf '\274\303\263\305\202\304\207\n'; sleep 60`, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			s := lifecycleSupervisor(t, "unused", config.RestartNo)
			cfg := s.procs["web"].cfg
			cfg.ReadyLog = `^(Worker ready|Zażółć)$`
			// Through JSON, as the handoff file carries it.
			var h Handoff
			data, _ := json.Marshal(Handoff{Config: cfg, Status: Starting, LogReady: &waiting, LogLine: []byte(c.pending)})
			if err := json.Unmarshal(data, &h); err != nil {
				t.Fatal(err)
			}
			adopt(t, s, c.script, h)
			if st := s.Snapshot(19000)[0].Status; st != Starting {
				t.Fatalf("adopted server before its ready line: %s", st)
			}
			time.Sleep(2500 * time.Millisecond)
			if st := s.Snapshot(19000)[0].Status; (st == Running) != c.ready {
				t.Fatalf("status %s, want ready %v", st, c.ready)
			}
		})
	}
}

func TestAdoptedServerKeepsItsLostReadiness(t *testing.T) {
	s := lifecycleSupervisor(t, "unused", config.RestartNo)
	cfg := s.procs["web"].cfg
	cfg.Port = 1 // nothing listens there
	adopt(t, s, "sleep 60", Handoff{Config: cfg, Status: Running, Note: "not listening on :1", MissingSince: time.Now().Add(-time.Minute)})
	if v := s.Snapshot(19000)[0]; !v.Unready || v.Health(false).Ready {
		t.Fatalf("lost readiness hidden by the upgrade: unready %v, health %+v", v.Unready, v.Health(false))
	}
}

func TestReadyLogWaitsForTheWholeLine(t *testing.T) {
	p := &proc{logs: NewMemoryLog()}
	p.logs.WatchLines(newReadyMatcher(p, regexp.MustCompile(`^Worker ready$`), nil).watch)
	fmt.Fprint(p.logs, "Worker ready")
	fmt.Fprint(p.logs, " not yet\r\n")
	if p.logReady.Load() {
		t.Fatal("a line that only began like the pattern matched")
	}
	fmt.Fprint(p.logs, "Worker ready\r\n")
	if !p.logReady.Load() {
		t.Fatal("the whole line did not match")
	}
}
