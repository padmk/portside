package cli

import (
	"testing"

	"github.com/padmk/portside/internal/server"
	"github.com/padmk/portside/internal/supervisor"
)

// Log lines are program output: `logs` prints characters that reorder or
// hide text (a right-to-left override, an isolate) as U+FFFD, as the
// terminal UI draws them, and --raw keeps every byte.
func TestLogsReplaceTextHidingCharacters(t *testing.T) {
	cfg, state, port := isolateDaemon(t)
	logs := []byte("invoice\u202egpj.exe ok\n\u2066hidden\u2069 👩‍💻\n")
	serveDaemon(t, state, port, map[string]func([]byte) any{
		"/api/state":            func([]byte) any { return server.State{Servers: []supervisor.View{{Name: "web"}}} },
		"/api/servers/web/logs": func([]byte) any { return logs },
	})

	e, out, _ := testEnv(cfg)
	if err := cmdLogs(e, []string{"web"}); err != nil {
		t.Fatal(err)
	}
	want := "invoice�gpj.exe ok\n�hidden� 👩‍💻\n"
	if out.String() != want {
		t.Errorf("logs printed %q, want %q", out, want)
	}

	e, out, _ = testEnv(cfg)
	if err := cmdLogs(e, []string{"--raw", "web"}); err != nil {
		t.Fatal(err)
	}
	if out.String() != string(logs) {
		t.Errorf("logs --raw printed %q, want the bytes unchanged", out)
	}
}
