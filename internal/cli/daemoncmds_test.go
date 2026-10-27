package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/padmk/portside/internal/app"
	"github.com/padmk/portside/internal/client"
	"github.com/padmk/portside/internal/paths"
	"github.com/padmk/portside/internal/ports"
)

// isolateDaemon points the config, state directory and daemon port at
// temporary values, so nothing reaches the account's real daemon.
func isolateDaemon(t *testing.T) (cfg, state string, port int) {
	t.Helper()
	dir := t.TempDir()
	cfg = filepath.Join(dir, app.ID+".toml")
	state = filepath.Join(dir, "state")
	if err := os.MkdirAll(state, 0o700); err != nil {
		t.Fatal(err)
	}
	port = freePort(t)
	t.Setenv("HOME", dir)
	t.Setenv(app.Env("CONFIG"), cfg)
	t.Setenv(app.Env("STATE_DIR"), state)
	t.Setenv(app.Env("PORT"), strconv.Itoa(port))
	return cfg, state, port
}

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

func testEnv(cfg string) (*env, *bytes.Buffer, *bytes.Buffer) {
	var out, errOut bytes.Buffer
	return &env{ctx: context.Background(), cfgPath: cfg, out: &out, errOut: &errOut}, &out, &errOut
}

func exitCode(err error) int {
	var se silentError
	switch {
	case err == nil:
		return 0
	case errors.As(err, &se):
		return se.code
	}
	return 1
}

func TestDaemonInfoNoStart(t *testing.T) {
	tests := []struct {
		name       string
		config     *string // nil: no config file
		wantCode   int
		wantCfgErr bool
	}{
		{"no config file", nil, infoNotRunning, false},
		{"valid config", new("port = 7999\n"), infoNotRunning, false},
		{"config does not load", new("port = \"not a number\n[servers.x\n"), infoConfigError, true},
		{"invalid server", new("[servers.web]\nport = 3000\n"), infoConfigError, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, state, port := isolateDaemon(t)
			if tt.config != nil {
				if err := os.WriteFile(cfg, []byte(*tt.config), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			e, out, _ := testEnv(cfg)
			err := cmdDaemon(e, []string{"info", "--no-start"})
			if got := exitCode(err); got != tt.wantCode {
				t.Fatalf("exit %d (%v), want %d", got, err, tt.wantCode)
			}
			var info map[string]any
			if err := json.Unmarshal(out.Bytes(), &info); err != nil {
				t.Fatalf("stdout is not JSON: %v\n%s", err, out)
			}
			if info["running"] != false || info["config"] != cfg || info["stateDir"] != state || info["cli"] != app.Version {
				t.Errorf("info = %v", info)
			}
			if _, ok := info["error"].(string); !ok {
				t.Errorf("info lacks error: %v", info)
			}
			if msg, ok := info["configError"].(string); ok != tt.wantCfgErr || (ok && msg == "") {
				t.Errorf("configError = %v, want present %v", info["configError"], tt.wantCfgErr)
			}
			// Nothing was started.
			if conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), 200*time.Millisecond); err == nil {
				conn.Close()
				t.Error("a daemon listens after info --no-start")
			}
			if _, err := os.Stat(filepath.Join(state, paths.LockName)); err == nil {
				t.Error("a daemon took the state lock after info --no-start")
			}
		})
	}
}

// fakeHealth answers /api/health like a daemon from before verified
// requests: with its identity and no proof. It records every other request.
type fakeHealth struct {
	pid  int
	mu   sync.Mutex
	reqs []string
}

func (f *fakeHealth) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/api/health" {
		_ = json.NewEncoder(w).Encode(client.Health{App: app.ID, Version: "0.0.1-old", PID: f.pid})
		return
	}
	f.mu.Lock()
	f.reqs = append(f.reqs, r.Method+" "+r.URL.Path)
	f.mu.Unlock()
	w.WriteHeader(http.StatusUnauthorized)
}

func (f *fakeHealth) requests() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.reqs...)
}

// serveHealth serves f on 127.0.0.1:port in this process.
func serveHealth(t *testing.T, port int, f *fakeHealth) {
	t.Helper()
	ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: f}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
}

func TestDaemonInfoUnverified(t *testing.T) {
	cfg, _, port := isolateDaemon(t)
	f := &fakeHealth{pid: 4242}
	serveHealth(t, port, f)
	e, out, _ := testEnv(cfg)
	if err := cmdDaemon(e, []string{"info", "--no-start"}); err != nil {
		t.Fatalf("info: %v", err)
	}
	var info map[string]any
	if err := json.Unmarshal(out.Bytes(), &info); err != nil {
		t.Fatal(err)
	}
	if info["running"] != true || info["verified"] != false || info["version"] != "0.0.1-old" || info["port"] != float64(port) {
		t.Errorf("info = %v, want running, unverified, the claimed version", info)
	}
	if got := f.requests(); len(got) != 0 {
		t.Errorf("requests beyond the handshake: %v", got)
	}
}

// With a config that does not load, an unproven reply on the guessed port
// is not this daemon: the app gets the config error, not a restart offer.
func TestDaemonInfoUnverifiedWithConfigError(t *testing.T) {
	cfg, _, port := isolateDaemon(t)
	if err := os.WriteFile(cfg, []byte("root = \"a\"\nroot = \"b\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	serveHealth(t, port, &fakeHealth{pid: 4242})
	e, out, _ := testEnv(cfg)
	err := cmdDaemon(e, []string{"info", "--no-start"})
	if got := exitCode(err); got != infoConfigError {
		t.Fatalf("exit %d (%v), want %d", got, err, infoConfigError)
	}
	var info map[string]any
	if err := json.Unmarshal(out.Bytes(), &info); err != nil {
		t.Fatal(err)
	}
	if info["running"] != false || info["configError"] == nil {
		t.Errorf("info = %v, want not running with configError", info)
	}
}

func needListenerLookup(t *testing.T) {
	t.Helper()
	_, lsofErr := exec.LookPath("lsof")
	_, ssErr := exec.LookPath("ss")
	if lsofErr != nil && ssErr != nil {
		t.Skip("neither lsof nor ss to name listeners")
	}
}

// TestListenerHelper is not a test: run as a child process with
// CLI_TEST_LISTEN set, it listens there, prints "ready" and waits for
// stdin to close.
func TestListenerHelper(t *testing.T) {
	addr := os.Getenv("CLI_TEST_LISTEN")
	if addr == "" {
		t.Skip("helper process only")
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		fmt.Println("error:", err)
		os.Exit(1)
	}
	defer ln.Close()
	fmt.Println("ready")
	_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
	os.Exit(0)
}

// listenInChild listens on addr in another process and returns its pid.
func listenInChild(t *testing.T, addr string) int {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestListenerHelper$")
	cmd.Env = append(os.Environ(), "CLI_TEST_LISTEN="+addr)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		stdin.Close()
		_ = cmd.Wait()
	})
	line, _ := bufio.NewReader(stdout).ReadString('\n')
	if line != "ready\n" {
		t.Fatalf("helper on %s: %q", addr, line)
	}
	return cmd.Process.Pid
}

func listenAt(t *testing.T, host string, port int) bool {
	t.Helper()
	ln, err := net.Listen("tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		return false
	}
	t.Cleanup(func() { ln.Close() })
	return true
}

func TestDaemonListens(t *testing.T) {
	needListenerLookup(t)
	self := os.Getpid()
	tests := []struct {
		name string
		// setup listens on port and returns the pid to ask about.
		setup func(t *testing.T, port int) int
		want  bool
	}{
		{"this process on 127.0.0.1, ::1 free", func(t *testing.T, port int) int {
			listenAt(t, "127.0.0.1", port)
			return self
		}, true},
		{"this process on both", func(t *testing.T, port int) int {
			listenAt(t, "127.0.0.1", port)
			if !ports.IPv6Loopback() || !listenAt(t, "::1", port) {
				t.Skip("no IPv6 loopback")
			}
			return self
		}, true},
		{"another pid claimed", func(t *testing.T, port int) int {
			listenAt(t, "127.0.0.1", port)
			return listenInChild(t, net.JoinHostPort("127.0.0.1", strconv.Itoa(freePort(t))))
		}, false},
		{"another process on 127.0.0.1", func(t *testing.T, port int) int {
			listenInChild(t, net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
			return self
		}, false},
		{"another process on ::1", func(t *testing.T, port int) int {
			if !ports.IPv6Loopback() {
				t.Skip("no IPv6 loopback")
			}
			listenAt(t, "127.0.0.1", port)
			listenInChild(t, net.JoinHostPort("::1", strconv.Itoa(port)))
			return self
		}, false},
		{"nothing listens", func(t *testing.T, port int) int { return self }, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			port := freePort(t)
			pid := tt.setup(t, port)
			if got := daemonListens(context.Background(), port, pid); got != tt.want {
				t.Errorf("daemonListens(%d, %d) = %v, want %v", port, pid, got, tt.want)
			}
		})
	}
}

// holdLock takes the daemon lock in the state directory, as a running
// daemon does.
func holdLock(t *testing.T, state string) {
	t.Helper()
	f, err := os.Create(filepath.Join(state, paths.LockName))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
}

// TestShutdownSignalsOnlyProvenDaemon covers the SIGTERM fallback for a
// daemon that cannot prove itself: the claimed pid must listen on the port
// and this state directory's lock must be held. This process stands in for
// the old daemon and catches SIGTERM, so a wrong signal fails the test
// instead of ending it.
func TestShutdownSignalsOnlyProvenDaemon(t *testing.T) {
	needListenerLookup(t)
	tests := []struct {
		name       string
		claimChild bool // the reply claims a pid that does not listen
		lock       bool
		wantSignal bool
	}{
		{"listener and lock", false, true, true},
		{"no lock held", false, false, false},
		{"claimed pid does not listen", true, true, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, state, port := isolateDaemon(t)
			sigs := make(chan os.Signal, 1)
			signal.Notify(sigs, syscall.SIGTERM)
			defer signal.Stop(sigs)

			pid := os.Getpid()
			var child int
			if tt.claimChild {
				child = listenInChild(t, net.JoinHostPort("127.0.0.1", strconv.Itoa(freePort(t))))
				pid = child
			}
			f := &fakeHealth{pid: pid}
			serveHealth(t, port, f)
			if tt.lock {
				holdLock(t, state)
			}
			e, _, errOut := testEnv(cfg)
			err := shutdown(e, client.New(port), nil)

			var got bool
			select {
			case <-sigs:
				got = true
			case <-time.After(300 * time.Millisecond):
			}
			if got != tt.wantSignal {
				t.Errorf("SIGTERM sent = %v, want %v (err %v, stderr %q)", got, tt.wantSignal, err, errOut)
			}
			if tt.wantSignal && err != nil {
				t.Errorf("shutdown: %v", err)
			}
			if !tt.wantSignal && !errors.Is(err, client.ErrUnverified) {
				t.Errorf("shutdown = %v, want the unverified error", err)
			}
			if child != 0 && syscall.Kill(child, 0) != nil {
				t.Error("the claimed process is gone")
			}
			if got := f.requests(); len(got) != 0 {
				t.Errorf("requests sent to an unproven daemon: %v", got)
			}
		})
	}
}

func TestRestartRefusesNewerDaemon(t *testing.T) {
	defer func(v string) { app.Version = v }(app.Version)
	app.Version = "0.3.1"
	if err := refuseDowngrade(client.Health{Version: "0.4.3"}, false); err == nil || !strings.Contains(err.Error(), "--allow-downgrade") {
		t.Errorf("newer daemon: %v, want a refusal naming --allow-downgrade", err)
	}
	for _, c := range []struct {
		daemon string
		allow  bool
	}{{"0.4.3", true}, {"0.3.1", false}, {"0.2.0", false}, {"dev", false}} {
		if err := refuseDowngrade(client.Health{Version: c.daemon}, c.allow); err != nil {
			t.Errorf("daemon %s, allow %v: %v", c.daemon, c.allow, err)
		}
	}
}
