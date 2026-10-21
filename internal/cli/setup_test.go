package cli

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/padmk/portside/internal/app"
)

// agentSandbox puts fake claude, codex and gemini CLIs, which append their
// arguments to the returned log, and a link to this test binary as the
// product's command on an otherwise empty PATH. With all of them on PATH
// and TERM set, setup never reads the login shell, whose PATH could hold
// the real CLIs.
func agentSandbox(t *testing.T) (home, log string) {
	t.Helper()
	_, _, _ = isolateDaemon(t)
	home = os.Getenv("HOME")
	bin := filepath.Join(home, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	log = filepath.Join(home, "argv.log")
	t.Setenv("PATH", bin)
	t.Setenv("TERM", "dumb")
	t.Setenv("FAKE_LOG", log)
	for _, k := range []string{"CLAUDE_CONFIG_DIR", "CODEX_HOME", "GEMINI_CLI_HOME"} {
		t.Setenv(k, "")
	}
	script := "#!/bin/sh\n{ printf '%s' \"${0##*/}\"; for a in \"$@\"; do printf ' %s' \"$a\"; done; printf '\\n'; } >> \"$FAKE_LOG\"\n"
	for _, name := range []string{"claude", "codex", "gemini"} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(self, filepath.Join(bin, app.ID)); err != nil {
		t.Fatal(err)
	}
	return home, log
}

func keys(m map[string]any) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func TestSetupJSONShape(t *testing.T) {
	home, log := agentSandbox(t)
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	e, out, _ := testEnv(os.Getenv(app.Env("CONFIG")))
	if err := cmdSetup(e, []string{"--json"}); err != nil {
		t.Fatal(err)
	}
	var items []map[string]any
	if err := json.Unmarshal(out.Bytes(), &items); err != nil {
		t.Fatalf("not a JSON array: %v\n%s", err, out)
	}
	var ids []string
	for _, it := range items {
		ids = append(ids, it["id"].(string))
		k := keys(it)
		want := []string{"applicable", "command", "id", "label", "state"}
		if _, ok := it["detail"]; ok {
			want = []string{"applicable", "command", "detail", "id", "label", "state"}
		}
		if !reflect.DeepEqual(k, want) {
			t.Errorf("item %v keys %v, want %v", it["id"], k, want)
		}
		if _, ok := it["applicable"].(bool); !ok {
			t.Errorf("applicable of %v is not a bool", it["id"])
		}
	}
	wantIDs := []string{"claude-mcp", "codex-mcp", "gemini-mcp", "claude-skill", "codex-skill"}
	if !reflect.DeepEqual(ids, wantIDs) {
		t.Errorf("ids %v, want %v", ids, wantIDs)
	}
	if b, _ := os.ReadFile(log); len(b) != 0 {
		t.Errorf("setup --json ran agent CLIs: %s", b)
	}

	// --apply needs --yes, and then prints the results as JSON.
	if err := cmdSetup(e, []string{"--apply", "claude-mcp"}); !errors.As(err, new(usageError)) {
		t.Errorf("--apply without --yes: %v, want a usage error", err)
	}
	out.Reset()
	if err := cmdSetup(e, []string{"--apply", "claude-mcp,claude-skill", "--yes"}); err != nil {
		t.Fatalf("--apply: %v\n%s", err, out)
	}
	var results []map[string]any
	if err := json.Unmarshal(out.Bytes(), &results); err != nil {
		t.Fatalf("results not a JSON array: %v\n%s", err, out)
	}
	if len(results) != 2 || results[0]["id"] != "claude-mcp" || results[1]["id"] != "claude-skill" {
		t.Fatalf("results %v", results)
	}
	for _, r := range results {
		if r["ok"] != true || r["changed"] != true || r["state"] != "ok" || r["label"] == "" {
			t.Errorf("result %v, want ok and changed", r)
		}
	}
	if b, _ := os.ReadFile(log); !strings.HasPrefix(string(b), "claude mcp add --scope user "+app.ID+" -- "+filepath.Join(home, "bin", app.ID)+" mcp\n") {
		t.Errorf("claude ran with %q", b)
	}
	if err := cmdSetup(e, []string{"--apply", "nope", "--yes"}); err == nil || !strings.Contains(err.Error(), "nope") {
		t.Errorf("unknown item: %v", err)
	}
}
