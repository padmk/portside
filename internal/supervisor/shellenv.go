package supervisor

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// The daemon may be started by launchd or systemd with a bare environment, so
// servers would not find node, pnpm or uv. Like editors do, it asks the user's
// login shell for its environment once in a while and runs servers with that.

const shellEnvTTL = 10 * time.Minute

type shellEnv struct {
	mu      sync.Mutex
	env     []string
	fetched time.Time
}

const envMarker = "__ENV_BEGIN__"

func (s *shellEnv) get() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.env != nil && time.Since(s.fetched) < shellEnvTTL {
		return s.env
	}
	if env := loginShellEnv(); env != nil {
		s.env = env
	} else if s.env == nil {
		s.env = os.Environ()
	}
	s.fetched = time.Now()
	return s.env
}

func loginShellEnv() []string {
	sh := accountShell()
	if sh == "" {
		sh = os.Getenv("SHELL")
	}
	if sh == "" {
		if _, err := os.Stat("/bin/zsh"); err == nil {
			sh = "/bin/zsh"
		} else {
			sh = "/bin/sh"
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	args := []string{"-l", "-c", "printf '" + envMarker + "'; command env -0"}
	// Interactive mode reads rc files (where most users set PATH) but some
	// shells print noise or hang on it, hence the marker and the timeout.
	if base := filepath.Base(sh); base == "zsh" || base == "bash" || base == "fish" {
		args = append([]string{"-i"}, args...)
	}
	cmd := exec.CommandContext(ctx, sh, args...)
	cmd.Stdin = nil
	cmd.Env = os.Environ()
	out, err := cmd.Output()
	if err != nil && len(out) == 0 {
		return nil
	}
	i := bytes.Index(out, []byte(envMarker))
	if i < 0 {
		return nil
	}
	var env []string
	for _, kv := range bytes.Split(out[i+len(envMarker):], []byte{0}) {
		s := string(kv)
		if !strings.Contains(s, "=") {
			continue
		}
		k := s[:strings.IndexByte(s, '=')]
		switch k {
		case "SHLVL", "PWD", "OLDPWD", "_", "TERM_PROGRAM", "TERM_SESSION_ID", "TMUX", "TMUX_PANE":
			continue
		}
		env = append(env, s)
	}
	if len(env) == 0 {
		return nil
	}
	return env
}

// accountShell returns the login shell recorded for the current user. launchd
// and systemd may set SHELL to a system default that differs from it.
func accountShell() string {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	name := os.Getenv("USER")
	if name == "" {
		out, err := exec.CommandContext(ctx, "id", "-un").Output()
		if err != nil {
			return ""
		}
		name = strings.TrimSpace(string(out))
	}
	if runtime.GOOS == "darwin" {
		out, err := exec.CommandContext(ctx, "dscl", ".", "-read", "/Users/"+name, "UserShell").Output()
		if err != nil {
			return ""
		}
		sh := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(string(out)), "UserShell:"))
		return existingShell(sh)
	}
	out, err := exec.CommandContext(ctx, "getent", "passwd", name).Output()
	if err != nil {
		return ""
	}
	fields := strings.Split(strings.TrimSpace(string(out)), ":")
	return existingShell(fields[len(fields)-1])
}

func existingShell(sh string) string {
	if !filepath.IsAbs(sh) {
		return ""
	}
	if _, err := os.Stat(sh); err != nil {
		return ""
	}
	return sh
}

// mergeEnv returns base with overrides applied (later keys win).
func mergeEnv(base []string, overrides map[string]string) []string {
	idx := map[string]int{}
	out := make([]string, 0, len(base)+len(overrides))
	for _, kv := range base {
		k := kv
		if i := strings.IndexByte(kv, '='); i >= 0 {
			k = kv[:i]
		}
		if j, ok := idx[k]; ok {
			out[j] = kv
			continue
		}
		idx[k] = len(out)
		out = append(out, kv)
	}
	for k, v := range overrides {
		if j, ok := idx[k]; ok {
			out[j] = k + "=" + v
		} else {
			idx[k] = len(out)
			out = append(out, k+"="+v)
		}
	}
	return out
}

func lookupEnv(env []string, key string) string {
	for i := len(env) - 1; i >= 0; i-- {
		if strings.HasPrefix(env[i], key+"=") {
			return env[i][len(key)+1:]
		}
	}
	return ""
}
