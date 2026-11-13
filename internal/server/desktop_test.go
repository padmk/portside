package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/padmk/portside/internal/config"
	"github.com/padmk/portside/internal/paths"
	"github.com/padmk/portside/internal/supervisor"
)

// The log opener gets a server's log, never a path a name leads to. The
// command is recorded instead of started: a started opener reports nothing
// back, so waiting for its effect raced with how fast the system started it.
func TestOpenLogsRefusesPaths(t *testing.T) {
	var launched [][]string
	old := launch
	launch = func(c *exec.Cmd) error {
		launched = append(launched, c.Args)
		return nil
	}
	t.Cleanup(func() { launch = old })

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
	if len(launched) != 0 {
		t.Fatalf("refused names launched %q", launched)
	}
	if code := open("web"); code != http.StatusOK {
		t.Fatalf("open logs web: status %d, want 200", code)
	}
	// macOS reveals the log file; elsewhere the opener gets its folder.
	want := filepath.Join(paths.LogDir(state), "web.log")
	if runtime.GOOS != "darwin" {
		want = paths.LogDir(state)
	}
	if len(launched) != 1 || launched[0][len(launched[0])-1] != want {
		t.Errorf("launched %q, want only %s", launched, want)
	}
}
