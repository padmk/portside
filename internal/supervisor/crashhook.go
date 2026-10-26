package supervisor

import (
	"bytes"
	"log"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/padmk/portside/internal/app"
)

const (
	// crashHookTimeout bounds one on_crash run; its process group is then
	// terminated.
	crashHookTimeout = 30 * time.Second
	// crashHookLines and crashHookTailBytes bound the log tail a command gets.
	crashHookLines     = 20
	crashHookTailBytes = 8 << 10
	// crashHookOutput is how much of a failed command's output the daemon log keeps.
	crashHookOutput = 512
)

// crashHookSlots bounds the crash commands running at once; more wait for a slot.
var crashHookSlots = make(chan struct{}, 4)

// headWriter keeps the first max bytes written to it and drops the rest.
type headWriter struct {
	buf bytes.Buffer
	max int
}

func (w *headWriter) Write(p []byte) (int, error) {
	if room := w.max - w.buf.Len(); room > 0 {
		w.buf.Write(p[:min(len(p), room)])
	}
	return len(p), nil
}

// crashReport is what an on_crash command learns about a server that has
// crashed for good.
type crashReport struct {
	server   string
	group    string
	exitCode *int // nil when the process never ran (a failed start)
	uptime   time.Duration
	restarts int
	note     string
	logFile  string
	tail     []string
}

// vars are the report's environment variables, which is the only way the
// command gets it: never in its command line.
func (r crashReport) vars() map[string]string {
	code := ""
	if r.exitCode != nil {
		code = strconv.Itoa(*r.exitCode)
	}
	return map[string]string{
		app.Env("EVENT"):     "crashed",
		app.Env("SERVER"):    r.server,
		app.Env("GROUP"):     r.group,
		app.Env("EXIT_CODE"): code,
		app.Env("UPTIME"):    strconv.Itoa(int(r.uptime.Seconds())),
		app.Env("RESTARTS"):  strconv.Itoa(r.restarts),
		app.Env("NOTE"):      r.note,
		app.Env("LOG_FILE"):  r.logFile,
		app.Env("LOG_TAIL"):  boundedTail(r.tail, crashHookTailBytes),
	}
}

// boundedTail joins the last lines that fit in max bytes.
func boundedTail(lines []string, max int) string {
	size, start := 0, len(lines)
	for start > 0 && size+len(lines[start-1])+1 <= max {
		start--
		size += len(lines[start]) + 1
	}
	return strings.Join(lines[start:], "\n")
}

// reportCrashLocked runs the server's on_crash command for a crash that no
// automatic restart follows, or an exit after which restart = "always" gave
// up, whatever the notify setting and connected apps.
// The command runs in the background, so a slow one never delays the
// supervisor; its failures go to the daemon log.
func (s *Supervisor) reportCrashLocked(p *proc, exitCode *int, uptime time.Duration) {
	command := p.cfg.OnCrash
	if command == "" || s.closing {
		return
	}
	r := crashReport{
		server:   p.cfg.Name,
		group:    p.cfg.Group,
		exitCode: exitCode,
		uptime:   uptime,
		restarts: p.retries,
		note:     p.note,
		logFile:  p.logs.Path(),
		tail:     p.logs.Tail(crashHookLines, 1000),
	}
	prepend, global := s.cfg.PathPrepend, s.cfg.Env
	go func() {
		// The login-shell read may take seconds, so it happens here, outside the lock.
		env := mergeEnv(withPath(s.env.get(), prepend), global)
		runCrashHook(command, mergeEnv(env, r.vars()), r.server)
	}()
}

// runCrashHook runs command with sh in the home directory and its own
// process group, ending the group after crashHookTimeout.
func runCrashHook(command string, env []string, server string) {
	crashHookSlots <- struct{}{}
	defer func() { <-crashHookSlots }()
	cmd := exec.Command("sh", "-c", command)
	cmd.Env = env
	if home, err := os.UserHomeDir(); err == nil {
		cmd.Dir = home
	}
	out := &headWriter{max: crashHookOutput + 1}
	cmd.Stdout, cmd.Stderr = out, out
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		log.Printf("on_crash for %s: %v", server, err)
		return
	}
	done := make(chan struct{})
	waited := make(chan error, 1)
	go func() { waited <- cmd.Wait(); close(done) }()
	select {
	case err := <-waited:
		if err != nil {
			log.Printf("on_crash for %s failed: %v: %s", server, err, firstBytes(out.buf.String(), crashHookOutput))
			return
		}
		log.Printf("on_crash for %s ran", server)
	case <-time.After(crashHookTimeout):
		Terminate(cmd.Process.Pid, done, 2*time.Second)
		log.Printf("on_crash for %s did not finish in %s and was stopped", server, crashHookTimeout)
	}
}

func firstBytes(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}
