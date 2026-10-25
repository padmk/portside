package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/padmk/portside/internal/config"
	"github.com/padmk/portside/internal/paths"
	"github.com/padmk/portside/internal/supervisor"
)

func TestOpenLogsRefusesPaths(t *testing.T) {
	// Fake desktop openers on an otherwise empty PATH record what they
	// are asked to open instead of opening it.
	bin := t.TempDir()
	launched := filepath.Join(bin, "launched.log")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> " + launched + "\n"
	for _, name := range []string{"open", "xdg-open"} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin)

	state := t.TempDir()
	sup := supervisor.New(&config.Config{Servers: []config.Server{{Name: "web"}}}, state)
	defer sup.Shutdown()
	srv := &Server{sup: sup, opt: Options{StateDir: state}}
	open := func(name string) int {
		body, _ := json.Marshal(openBody{Target: "logs", Name: name})
		rec := httptest.NewRecorder()
		srv.open(rec, httptest.NewRequest(http.MethodPost, "/api/open", bytes.NewReader(body)))
		return rec.Code
	}

	for _, name := range []string{"../web", "..", "a/b", "/etc/passwd", ".web", ".hidden.log", "web/../../x"} {
		if code := open(name); code != http.StatusBadRequest {
			t.Errorf("open logs %q: status %d, want 400", name, code)
		}
	}
	// A plain server name opens its log, which shows the openers work and
	// gives any launch for a refused name time to show up.
	if code := open("web"); code != http.StatusOK {
		t.Fatalf("open logs web: status %d, want 200", code)
	}
	// macOS reveals the log file; elsewhere the opener gets its folder.
	want := filepath.Join(paths.LogDir(state), "web.log")
	if runtime.GOOS != "darwin" {
		want = paths.LogDir(state)
	}
	var got string
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if b, err := os.ReadFile(launched); err == nil && strings.Contains(string(b), want) {
			time.Sleep(100 * time.Millisecond)
			b, _ = os.ReadFile(launched)
			got = string(b)
			break
		}
	}
	if lines := strings.Split(strings.TrimSpace(got), "\n"); len(lines) != 1 || !strings.HasSuffix(lines[0], want) {
		t.Errorf("launched %q, want only %s", got, want)
	}
}
