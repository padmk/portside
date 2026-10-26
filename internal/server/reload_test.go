package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/padmk/portside/internal/config"
	"github.com/padmk/portside/internal/share"
	"github.com/padmk/portside/internal/signing"
	"github.com/padmk/portside/internal/supervisor"
	"github.com/padmk/portside/internal/tailnet"
)

// TestRequestSeesConfigEditedOutside edits the config file the way `add`
// and `rm` do, outside the daemon, and checks that the very next request
// sees the change instead of waiting for the periodic check.
func TestRequestSeesConfigEditedOutside(t *testing.T) {
	dir, state := t.TempDir(), t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte("port = 7999\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	sup := supervisor.New(cfg, state)
	defer sup.Shutdown()
	token := signing.RandomHex(32)
	s := New(Options{Supervisor: sup, Share: share.New(7999, state, tailnet.Preloaded(&tailnet.Status{})),
		ConfigPath: path, StateDir: state, Token: token, Port: 7999})

	// Stopping a server that is not running answers whether the daemon
	// knows the name, without starting anything.
	stop := func(name string) int {
		r := httptest.NewRequest(http.MethodPost, "/api/servers/"+name+"/stop", nil)
		r.Host = "127.0.0.1:7999"
		signing.Sign(r.Header, token, r.Method, r.RequestURI, nil, s.epoch)
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		return w.Code
	}

	port := 0
	if err := config.AddServer(path, config.Spec{Name: "api", Cmd: "sleep 30", Port: &port}); err != nil {
		t.Fatal(err)
	}
	if code := stop("api"); code != http.StatusOK {
		t.Fatalf("right after adding: status %d, want 200", code)
	}
	if err := config.RemoveServer(path, "api"); err != nil {
		t.Fatal(err)
	}
	if code := stop("api"); code != http.StatusNotFound {
		t.Errorf("right after removing: status %d, want 404", code)
	}
}
