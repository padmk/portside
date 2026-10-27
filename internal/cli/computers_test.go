package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/padmk/portside/internal/remote"
)

func TestPairReadsTheCodeFromStandardInput(t *testing.T) {
	cfg, state, port := isolateDaemon(t)
	var got map[string]string
	serveDaemon(t, state, port, map[string]func([]byte) any{"/api/remote/pair": func(b []byte) any {
		_ = json.Unmarshal(b, &got)
		return remote.Info{Status: remote.Open}
	}})

	e, _, _ := testEnv(cfg)
	e.in = strings.NewReader("abcd-efgh\n")
	if err := cmdPair(e, []string{"devbox"}); err != nil {
		t.Fatal(err)
	}
	if got["peer"] != "devbox" || got["code"] != "abcd-efgh" {
		t.Errorf("the daemon got %v, want the code from standard input", got)
	}

	got = nil
	e, _, _ = testEnv(cfg)
	e.in = strings.NewReader("abcd-efgh\n")
	err := cmdPair(e, []string{"devbox", "abcd-efgh"})
	if _, ok := err.(usageError); !ok || !strings.Contains(err.Error(), "not taken as an argument") {
		t.Errorf("code as an argument: %v, want a usage error", err)
	}
	if got != nil {
		t.Error("a code given as an argument reached the daemon")
	}

	e, _, _ = testEnv(cfg)
	e.in = strings.NewReader("\n")
	if err := cmdPair(e, []string{"devbox"}); err == nil || got != nil {
		t.Errorf("empty standard input: %v, daemon got %v; want an error and no request", err, got)
	}
}
