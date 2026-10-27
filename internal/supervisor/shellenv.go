package supervisor

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// The daemon may be started by launchd or systemd with a bare environment, so
// servers would not find node, pnpm or uv. Like editors do, it asks the user's
// login shell for its environment once in a while and runs servers with that.

const shellEnvTTL = 10 * time.Minute

type shellEnv struct {
	mu  sync.Mutex
	env []string
	// volatile holds the keys whose values differed between two reads in a
	// row; they never mark servers stale.
	volatile map[string]bool
	probed   bool
	probing  bool // the second read runs; volatile keys are not known yet
	fetched  time.Time
	read     ShellRead
	// own gives servers the daemon's own environment instead
	// (login_env = false).
	own bool
}

// setOwn chooses between the daemon's own environment and the login shell's.
func (s *shellEnv) setOwn(own bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.own = own
}

const envMarker = "__ENV_BEGIN__"

// cached returns the last environment read and its volatile keys. The
// environment is nil while the volatile keys are still being found, so
// stale checks wait for them.
func (s *shellEnv) cached() ([]string, map[string]bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.own {
		return os.Environ(), nil
	}
	if s.probing {
		return nil, nil
	}
	return s.env, s.volatile
}

func (s *shellEnv) get() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.own {
		return os.Environ()
	}
	if s.env != nil && time.Since(s.fetched) < shellEnvTTL {
		return s.env
	}
	read, env := ReadLoginShell()
	if env != nil && !s.probed && !s.probing {
		// A second read shows which values change on every shell start
		// (session ids, agents, generated PATH entries). It runs without
		// the lock, so starts do not wait for it.
		s.probing = true
		go func() {
			_, again := ReadLoginShell()
			s.mu.Lock()
			defer s.mu.Unlock()
			s.probing = false
			if again != nil {
				s.volatile = differingKeys(env, again)
				s.probed = true
			}
		}()
	}
	if read.Shell != s.read.Shell || read.Used != s.read.Used || read.Err != s.read.Err {
		log.Print(read.Describe())
	}
	s.read = read
	if env != nil {
		s.env = env
	} else if s.env == nil {
		s.env = os.Environ()
	}
	s.fetched = time.Now()
	return s.env
}

// LoginShellEnv reads the account's login-shell environment once, for
// commands outside the daemon (such as the app's helper, which starts with a
// minimal PATH). It returns nil when the shell cannot be read.
func LoginShellEnv() []string {
	_, env := ReadLoginShell()
	return env
}

// ShellRead describes one read of the login-shell environment.
type ShellRead struct {
	Shell string // the account's login shell
	Used  string // the shell that was read: Shell, or bash or zsh in place of a non-POSIX one
	Err   string // why the read failed; servers then get the daemon's own environment
	Path  string // PATH from the read
}

// Fallback reports whether another shell was read in place of the login shell.
func (r ShellRead) Fallback() bool { return r.Used != r.Shell }

// Describe is a one-line summary for logs.
func (r ShellRead) Describe() string {
	switch {
	case r.Err != "":
		return fmt.Sprintf("login shell %s: environment not read (%s); servers get the daemon's environment", r.Used, r.Err)
	case r.Fallback():
		return fmt.Sprintf("login shell %s cannot be read directly; read the environment of %s -l -i instead", r.Shell, r.Used)
	default:
		return "login shell " + r.Shell + ": environment read"
	}
}

// ReadLoginShell reads the environment of the account's login shell. The
// environment is nil when the read fails.
func ReadLoginShell() (ShellRead, []string) {
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
	r := ShellRead{Shell: sh, Used: sh}
	if !posixShells[filepath.Base(sh)] {
		// nushell, tcsh, xonsh and others take other flags and syntax; a
		// login zsh or bash still reads the system and profile settings.
		r.Used = fallbackShell()
		if r.Used == "" {
			r.Used = sh
			r.Err = "not a POSIX shell, and no zsh or bash to read instead"
			return r, nil
		}
	}
	env, err := readShell(r.Used)
	if err != nil {
		r.Err = err.Error()
		return r, nil
	}
	r.Path = lookupEnv(env, "PATH")
	return r, env
}

// posixShells take -l and -c like sh; fish too, and it runs the same command.
var posixShells = map[string]bool{"sh": true, "bash": true, "zsh": true, "fish": true, "ksh": true, "mksh": true, "dash": true}

func fallbackShell() string {
	for _, sh := range []string{"/bin/zsh", "/bin/bash", "/usr/bin/zsh", "/usr/bin/bash"} {
		if _, err := os.Stat(sh); err == nil {
			return sh
		}
	}
	for _, name := range []string{"zsh", "bash"} {
		if p, err := exec.LookPath(name); err == nil {
			return p
		}
	}
	return ""
}

// envProgram is env by absolute path, so that neither a shell function nor a
// missing `command` builtin gets in the way.
func envProgram() string {
	if _, err := os.Stat("/usr/bin/env"); err == nil {
		return "/usr/bin/env"
	}
	return "env"
}

func readShell(sh string) ([]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	args := []string{"-l", "-c", "printf '" + envMarker + "'; " + envProgram() + " -0"}
	// Interactive mode reads rc files (where most users set PATH) but some
	// shells print noise or hang on it, hence the marker and the timeout.
	if base := filepath.Base(sh); base == "zsh" || base == "bash" || base == "fish" {
		args = append([]string{"-i"}, args...)
	}
	cmd := exec.CommandContext(ctx, sh, args...)
	cmd.Stdin = nil
	cmd.Env = os.Environ()
	agents, agentsKnown := sshAgents()
	out, err := cmd.Output()
	if err != nil && len(out) == 0 {
		if ctx.Err() != nil {
			return nil, errors.New("timed out")
		}
		return nil, err
	}
	_, after, ok := bytes.Cut(out, []byte(envMarker))
	if !ok {
		return nil, errors.New("no environment in its output")
	}
	var env []string
	for kv := range bytes.SplitSeq(after, []byte{0}) {
		s := string(kv)
		if !strings.Contains(s, "=") {
			continue
		}
		k := s[:strings.IndexByte(s, '=')]
		switch k {
		// Values of the shell that was read, of no use to servers.
		case "SHLVL", "PWD", "OLDPWD", "_", "TERM_PROGRAM", "TERM_SESSION_ID", "TMUX", "TMUX_PANE", "STARSHIP_SESSION_KEY":
			continue
		}
		env = append(env, s)
	}
	if len(env) == 0 {
		return nil, errors.New("empty environment")
	}
	if !agentsKnown {
		return env, nil
	}
	return stopSpawnedAgent(env, agents), nil
}

// sshAgents lists the ssh-agent processes running now, and whether the list
// could be read.
func sshAgents() (map[int]bool, bool) {
	cmd := exec.Command("ps", "-A", "-o", "pid=,comm=")
	cmd.Env = cLocaleEnv()
	out, err := cmd.Output()
	if err != nil {
		return nil, false
	}
	agents := map[int]bool{}
	for line := range strings.SplitSeq(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) < 2 || filepath.Base(strings.Join(f[1:], " ")) != "ssh-agent" {
			continue
		}
		if pid, err := strconv.Atoi(f[0]); err == nil {
			agents[pid] = true
		}
	}
	return agents, true
}

// stopSpawnedAgent ends an ssh-agent that the shell's rc files started for
// this read (`eval $(ssh-agent)`), which would otherwise be left behind on
// every read, and gives servers the daemon's own agent instead. before
// lists the agents running before the read: one the rc files reuse belongs
// to the user's terminals.
func stopSpawnedAgent(env []string, before map[int]bool) []string {
	v := lookupEnv(env, "SSH_AGENT_PID")
	if v == "" || v == os.Getenv("SSH_AGENT_PID") {
		return env
	}
	pid, err := strconv.Atoi(v)
	if err != nil || pid <= 1 || before[pid] || filepath.Base(processName(pid)) != "ssh-agent" {
		return env
	}
	_ = syscall.Kill(pid, syscall.SIGTERM)
	out := make([]string, 0, len(env))
	for _, kv := range env {
		if !strings.HasPrefix(kv, "SSH_AGENT_PID=") && !strings.HasPrefix(kv, "SSH_AUTH_SOCK=") {
			out = append(out, kv)
		}
	}
	for _, k := range []string{"SSH_AUTH_SOCK", "SSH_AGENT_PID"} {
		if own, ok := os.LookupEnv(k); ok {
			out = append(out, k+"="+own)
		}
	}
	return out
}

func processName(pid int) string {
	cmd := exec.Command("ps", "-o", "comm=", "-p", strconv.Itoa(pid))
	cmd.Env = cLocaleEnv()
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// sessionPrefixes name variables that terminals, prompts and version
// managers set for each shell session.
var sessionPrefixes = []string{"FNM_", "ATUIN_", "__MISE_", "KITTY_", "WEZTERM_", "ITERM_", "GHOSTTY_", "VSCODE_", "ZELLIJ", "TERM_SESSION_ID", "SSH_AGENT_PID", "WINDOWID", "SECURITYSESSIONID"}

// sessionValue reports whether a variable describes the shell session that
// was read rather than the user's setup.
func sessionValue(k, v string) bool {
	for _, p := range sessionPrefixes {
		if strings.HasPrefix(k, p) {
			return true
		}
	}
	// The socket of an agent that a shell started, in a temporary directory.
	return k == "SSH_AUTH_SOCK" && strings.Contains(v, "/ssh-") && strings.HasPrefix(filepath.Base(v), "agent.")
}

// stablePath drops PATH entries made for one shell session, such as fnm's
// per-shell links.
func stablePath(v string) string {
	var out []string
	for _, dir := range filepath.SplitList(v) {
		if strings.Contains(dir, "fnm_multishells") {
			continue
		}
		out = append(out, dir)
	}
	return strings.Join(out, string(os.PathListSeparator))
}

// comparableEnv is env as stale checks see it: sorted, without session and
// volatile variables, and with a stable PATH.
func comparableEnv(env []string, volatile map[string]bool) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		if volatile[k] || sessionValue(k, v) {
			continue
		}
		if k == "PATH" {
			kv = "PATH=" + stablePath(v)
		}
		out = append(out, kv)
	}
	slices.Sort(out)
	return out
}

// differingKeys returns the keys whose comparable values differ between two
// reads, or that only one of them has.
func differingKeys(a, b []string) map[string]bool {
	values := func(env []string) map[string]string {
		m := map[string]string{}
		for _, kv := range comparableEnv(env, nil) {
			k, v, _ := strings.Cut(kv, "=")
			m[k] = v
		}
		return m
	}
	ma, mb := values(a), values(b)
	out := map[string]bool{}
	for k, v := range ma {
		if w, ok := mb[k]; !ok || w != v {
			out[k] = true
		}
	}
	for k := range mb {
		if _, ok := ma[k]; !ok {
			out[k] = true
		}
	}
	return out
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
		if before, _, ok := strings.Cut(kv, "="); ok {
			k = before
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
	for _, e := range slices.Backward(env) {
		if strings.HasPrefix(e, key+"=") {
			return e[len(key)+1:]
		}
	}
	return ""
}

// sameEnv reports whether two environments match for stale checks.
func sameEnv(a, b []string, volatile map[string]bool) bool {
	// A temporary agent socket on either side (such as one that an older
	// daemon kept from a read) is session state on both.
	for _, env := range [][]string{a, b} {
		if v := lookupEnv(env, "SSH_AUTH_SOCK"); sessionValue("SSH_AUTH_SOCK", v) && !volatile["SSH_AUTH_SOCK"] {
			volatile = maps.Clone(volatile)
			if volatile == nil {
				volatile = map[string]bool{}
			}
			volatile["SSH_AUTH_SOCK"] = true
		}
	}
	return slices.Equal(comparableEnv(a, volatile), comparableEnv(b, volatile))
}
