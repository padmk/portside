package supervisor

import (
	"context"
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
	p.logs.WatchLines(readyLogWatch(p, regexp.MustCompile(`^Worker ready$`)))
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

func TestAdoptedServerKeepsWaitingForItsReadyLine(t *testing.T) {
	s := lifecycleSupervisor(t, "unused", config.RestartNo)
	cmd := exec.Command("sh", "-c", "sleep 2; echo 'Worker ready'; sleep 60")
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
	cfg := s.procs["web"].cfg
	cfg.ReadyLog = "Worker ready"
	waiting := false
	h := Handoff{Name: "web", PID: pid, PIDStart: processStart(pid), PTY: fd, Config: cfg,
		Status: Starting, Want: true, StartedAt: time.Now().Add(-time.Minute), LogReady: &waiting}
	if got := s.Adopt([]Handoff{h}); len(got) != 1 {
		t.Fatalf("adopted %v", got)
	}
	time.Sleep(time.Second)
	if st := s.Snapshot(19000)[0].Status; st != Starting {
		t.Fatalf("adopted server before its ready line: %s", st)
	}
	if err := s.WaitReady(context.Background(), "web", 5*time.Second); err != nil {
		t.Fatal(err)
	}
}
