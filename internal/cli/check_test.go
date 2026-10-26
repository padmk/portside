package cli

import (
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/padmk/portside/internal/app"
	"github.com/padmk/portside/internal/config"
	"github.com/padmk/portside/internal/paths"
	"github.com/padmk/portside/internal/server"
	"github.com/padmk/portside/internal/signing"
	"github.com/padmk/portside/internal/supervisor"
)

// serveState runs a daemon stand-in on 127.0.0.1:port that proves itself
// with the token in state and answers signed /api/state requests with st.
func serveState(t *testing.T, state string, port int, st server.State) {
	t.Helper()
	token := signing.RandomHex(32)
	if err := os.WriteFile(filepath.Join(state, paths.TokenName), []byte(token+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	epoch := signing.NewEpoch()
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body []byte
		switch r.URL.Path {
		case "/api/health":
			body, _ = json.Marshal(server.Health{App: app.ID, Version: "9.9.9", PID: 4242})
		case "/api/state":
			var v signing.Verifier
			if v.Verify(r, token, r.RequestURI, nil) != nil || signing.CheckEpoch(r.Header, epoch) != nil {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			body, _ = json.Marshal(st)
		default:
			w.WriteHeader(http.StatusNotFound)
			return
		}
		signing.ProveReply(w.Header(), r.Header, token, epoch, http.StatusOK, body)
		_, _ = w.Write(body)
	})
	ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: h}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
}

func TestCheckExitCodes(t *testing.T) {
	running := supervisor.View{Name: "web", Status: supervisor.Running, Wanted: true, Restart: config.RestartNo}
	stopped := supervisor.View{Name: "docs", Status: supervisor.Stopped, Restart: config.RestartNo}
	crashed := supervisor.View{Name: "api", Status: supervisor.Crashed, Note: "exited with code 1", Restart: config.RestartNo}
	tests := []struct {
		name     string
		servers  []supervisor.View
		wantCode int
		wantOut  []string
	}{
		{"all ready", []supervisor.View{running, stopped}, 0, []string{"1 of 1 server that should run is ready"}},
		{"one crashed", []supervisor.View{running, crashed, stopped}, checkDegraded, []string{"api", "crashed", "exited with code 1", "1 of 2 servers that should run are ready"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, state, port := isolateDaemon(t)
			serveState(t, state, port, server.State{Daemon: server.Health{Version: "9.9.9", PID: 4242}, Servers: tt.servers})

			e, out, _ := testEnv(cfg)
			if got := exitCode(cmdCheck(e, nil)); got != tt.wantCode {
				t.Fatalf("exit %d, want %d; output:\n%s", got, tt.wantCode, out)
			}
			for _, w := range tt.wantOut {
				if !strings.Contains(out.String(), w) {
					t.Errorf("output %q lacks %q", out, w)
				}
			}
			if tt.wantCode == 0 && strings.Contains(out.String(), "web") {
				t.Errorf("output names a ready server: %q", out)
			}

			e, out, _ = testEnv(cfg)
			if got := exitCode(cmdCheck(e, []string{"--json"})); got != tt.wantCode {
				t.Fatalf("--json: exit %d, want %d", got, tt.wantCode)
			}
			var report struct {
				OK     bool `json:"ok"`
				Daemon struct {
					Version string `json:"version"`
					PID     int    `json:"pid"`
				} `json:"daemon"`
				Servers []map[string]any `json:"servers"`
			}
			if err := json.Unmarshal(out.Bytes(), &report); err != nil {
				t.Fatalf("%v: %s", err, out)
			}
			if report.OK != (tt.wantCode == 0) || report.Daemon.Version != "9.9.9" || report.Daemon.PID != 4242 || len(report.Servers) != len(tt.servers) {
				t.Errorf("report = %+v", report)
			}
			for _, s := range report.Servers {
				switch s["name"] {
				case "web":
					if s["expected"] != true || s["ready"] != true || s["reason"] != nil {
						t.Errorf("web = %v", s)
					}
				case "docs":
					if s["expected"] != false || s["ready"] != false || s["status"] != "stopped" {
						t.Errorf("docs = %v", s)
					}
				case "api":
					if s["expected"] != true || s["ready"] != false || s["reason"] != "exited with code 1" {
						t.Errorf("api = %v", s)
					}
				}
			}
		})
	}
}

func TestCheckCannotTell(t *testing.T) {
	tests := []struct {
		name    string
		prepare func(t *testing.T, cfg, state string, port int)
		want    string
	}{
		{"no daemon", func(*testing.T, string, string, int) {}, "not running"},
		{"unproven daemon", func(t *testing.T, _, _ string, port int) { serveHealth(t, port, &fakeHealth{pid: 4242}) }, "cannot prove"},
		{"config error in the daemon", func(t *testing.T, _, state string, port int) {
			serveState(t, state, port, server.State{Config: server.ConfigInfo{Error: "line 3: unknown key"}})
		}, "unknown key"},
		{"config error", func(t *testing.T, cfg, _ string, _ int) {
			if err := os.WriteFile(cfg, []byte("root = \"a\"\nroot = \"b\"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}, "config"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, state, port := isolateDaemon(t)
			tt.prepare(t, cfg, state, port)

			e, _, errOut := testEnv(cfg)
			if got := exitCode(cmdCheck(e, nil)); got != checkUnknown {
				t.Fatalf("exit %d, want %d", got, checkUnknown)
			}
			if !strings.Contains(errOut.String(), tt.want) {
				t.Errorf("stderr %q lacks %q", errOut, tt.want)
			}

			e, out, _ := testEnv(cfg)
			if got := exitCode(cmdCheck(e, []string{"--json"})); got != checkUnknown {
				t.Fatalf("--json: exit %d, want %d", got, checkUnknown)
			}
			var report map[string]any
			if err := json.Unmarshal(out.Bytes(), &report); err != nil {
				t.Fatalf("%v: %s", err, out)
			}
			if report["ok"] != false || !strings.Contains(report["error"].(string), tt.want) {
				t.Errorf("report = %v", report)
			}
			// Never starts a daemon.
			if _, err := os.Stat(filepath.Join(state, paths.LockName)); err == nil {
				t.Error("a daemon was started")
			}
		})
	}
}

func TestCheckNamedServersAreRequired(t *testing.T) {
	cfg, state, port := isolateDaemon(t)
	serveState(t, state, port, server.State{Daemon: server.Health{Version: "9.9.9", PID: 4242}, Servers: []supervisor.View{
		{Name: "web", Group: "shop", Status: supervisor.Running, Wanted: true, Restart: config.RestartNo},
		{Name: "docs", Status: supervisor.Stopped, Restart: config.RestartNo},
		{Name: "api", Status: supervisor.Crashed, Restart: config.RestartNo},
	}})
	for _, c := range []struct {
		args []string
		want int
		out  string
	}{
		{[]string{"shop"}, 0, "1 of 1 server that should run is ready"},
		{[]string{"web", "docs"}, checkDegraded, "docs"},
		{[]string{"wbe"}, checkUnknown, `unknown server or group "wbe"`},
		{[]string{""}, checkUnknown, "an empty server or group name"},
	} {
		e, out, errOut := testEnv(cfg)
		if got := exitCode(cmdCheck(e, c.args)); got != c.want {
			t.Fatalf("check %v: exit %d, want %d; output:\n%s%s", c.args, got, c.want, out, errOut)
		}
		if !strings.Contains(out.String()+errOut.String(), c.out) {
			t.Errorf("check %v: output %q%q lacks %q", c.args, out, errOut, c.out)
		}
		if strings.Contains(out.String(), "api") {
			t.Errorf("check %v reported a server it was not asked about: %q", c.args, out)
		}
	}
}
