package supervisor

import (
	"bytes"
	"cmp"
	"errors"
	"log"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/padmk/portside/internal/app"
	"github.com/padmk/portside/internal/config"
)

// hookTimeout bounds one hook command's run; its process group is then
// terminated.
var hookTimeout = 30 * time.Second

const (
	// hookOutputWait bounds how long a finished command's output is read:
	// a background process it started (in another session, which ending
	// its group misses) can hold the output open.
	hookOutputWait = 2 * time.Second
	// hookLines and hookTailBytes bound the log tail a command gets.
	hookLines     = 20
	hookTailBytes = 8 << 10
	// hookOutput is how much of a failed command's output the daemon log keeps.
	hookOutput = 512
)

// hookSlots bounds the hook commands running at once; more wait for a slot.
var hookSlots = make(chan struct{}, 4)

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

// Events a hook command reports, in PORTSIDE_EVENT: a server that crashed
// for good (on_crash), a running one that stopped answering (on_unready),
// and one of those ready again (on_recover).
const (
	eventCrashed   = "crashed"
	eventUnready   = "unready"
	eventRecovered = "recovered"
)

// Triggers say what began a server's round of runs, which its automatic
// restarts continue, in PORTSIDE_TRIGGER: a start someone asked for (also
// of a dependency), a retry after retry_after, or the daemon bringing it
// back when it started.
const (
	triggerStart   = "start"
	triggerRetry   = "retry"
	triggerRestore = "restore"
)

// hookReport is what a hook command learns about a server.
type hookReport struct {
	event    string
	server   string
	group    string
	trigger  string
	exitCode *int // crashed only; nil when the process never ran (a failed start)
	uptime   time.Duration
	restarts int
	streak   int           // crashed: reports in a row without the server ready in between
	from     string        // recovered: the event it recovered from
	downtime time.Duration // recovered: since that event's problem began
	note     string
	logFile  string
	tail     []string
}

// vars are the report's environment variables, which is the only way the
// command gets it: never in its command line.
func (r hookReport) vars() map[string]string {
	code, streak, downtime := "", "", ""
	if r.exitCode != nil {
		code = strconv.Itoa(*r.exitCode)
	}
	if r.streak > 0 {
		streak = strconv.Itoa(r.streak)
	}
	if r.event == eventRecovered {
		downtime = strconv.Itoa(int(r.downtime.Seconds()))
	}
	return map[string]string{
		app.Env("EVENT"):          r.event,
		app.Env("SERVER"):         r.server,
		app.Env("GROUP"):          r.group,
		app.Env("TRIGGER"):        r.trigger,
		app.Env("EXIT_CODE"):      code,
		app.Env("UPTIME"):         strconv.Itoa(int(r.uptime.Seconds())),
		app.Env("RESTARTS"):       strconv.Itoa(r.restarts),
		app.Env("STREAK"):         streak,
		app.Env("RECOVERED_FROM"): r.from,
		app.Env("DOWNTIME"):       downtime,
		app.Env("NOTE"):           r.note,
		app.Env("LOG_FILE"):       r.logFile,
		app.Env("LOG_TAIL"):       boundedTail(r.tail, hookTailBytes),
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

// reportLocked starts a report on p for event, with the fields every event
// has; the caller adds the rest.
func reportLocked(p *proc, event string) hookReport {
	trigger := p.trigger
	if trigger == "" {
		trigger = triggerStart
	}
	return hookReport{
		event:    event,
		server:   p.cfg.Name,
		group:    p.cfg.Group,
		trigger:  trigger,
		restarts: p.retries,
		note:     p.note,
		logFile:  p.logs.Path(),
		tail:     p.logs.Tail(hookLines, 1000),
	}
}

// runHookLocked runs a hook command for r in the background, so a slow one
// never delays the supervisor; its failures go to the daemon log. It
// reports whether the command runs: not when it is "" or the daemon is
// stopping.
func (s *Supervisor) runHookLocked(key, command string, r hookReport) bool {
	if command == "" || s.closing {
		return false
	}
	prepend, global := s.cfg.PathPrepend, s.cfg.Env
	go func() {
		// The login-shell read may take seconds, so it happens here, outside the lock.
		env := mergeEnv(withPath(s.env.get(), prepend), global)
		runHook(key, command, mergeEnv(env, r.vars()), r.server)
	}()
	return true
}

// openIncidentLocked records that a hook reported a problem with p, which
// on_recover reports the end of; a later problem keeps the start of the
// first.
func openIncidentLocked(p *proc, event string, since time.Time) {
	if p.incident == "" {
		p.incidentSince = since
	}
	p.incident = event
}

// reportCrashLocked runs the server's on_crash command for a crash that no
// automatic restart follows, or an exit after which restart = "always" gave
// up, whatever the notify setting and connected apps.
func (s *Supervisor) reportCrashLocked(p *proc, exitCode *int, uptime time.Duration) {
	p.streak++
	r := reportLocked(p, eventCrashed)
	r.exitCode, r.uptime, r.streak = exitCode, uptime, p.streak
	if s.runHookLocked("on_crash", p.cfg.OnCrash, r) {
		openIncidentLocked(p, eventCrashed, time.Now())
	}
}

// startFailedLocked reports a start that failed and schedules the next try
// when retry_after asks for one.
func (s *Supervisor) startFailedLocked(p *proc) {
	s.reportCrashLocked(p, nil, 0)
	s.retryLocked(p)
}

// unreadyLocked runs on_unready once a running server has not answered for
// its unready_after, once per problem.
func (s *Supervisor) unreadyLocked(p *proc, now time.Time) {
	after := cmp.Or(p.cfg.UnreadyAfter, config.DefaultUnreadyAfter)
	if p.incident != "" || p.missingSince.IsZero() || now.Sub(p.missingSince) < after {
		return
	}
	r := reportLocked(p, eventUnready)
	r.uptime = now.Sub(p.startedAt)
	if s.runHookLocked("on_unready", p.cfg.OnUnready, r) {
		openIncidentLocked(p, eventUnready, p.missingSince)
	}
}

// readyLocked notes that p is ready: its crash streak ends, and a problem a
// hook reported is over, which on_recover reports.
func (s *Supervisor) readyLocked(p *proc, now time.Time) {
	p.streak = 0
	if p.incident == "" {
		return
	}
	r := reportLocked(p, eventRecovered)
	r.uptime = now.Sub(p.startedAt)
	r.from, r.downtime = p.incident, now.Sub(p.incidentSince)
	p.incident, p.incidentSince = "", time.Time{}
	s.runHookLocked("on_recover", p.cfg.OnRecover, r)
}

// runHook runs the command of hook key with sh in the home directory and
// its own process group, ending the group after hookTimeout.
func runHook(key, command string, env []string, server string) {
	hookSlots <- struct{}{}
	defer func() { <-hookSlots }()
	cmd := exec.Command("sh", "-c", command)
	cmd.Env = env
	if home, err := os.UserHomeDir(); err == nil {
		cmd.Dir = home
	}
	out := &headWriter{max: hookOutput + 1}
	cmd.Stdout, cmd.Stderr = out, out
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.WaitDelay = hookOutputWait
	if err := cmd.Start(); err != nil {
		log.Printf("%s for %s: %v", key, server, err)
		return
	}
	done := make(chan struct{})
	waited := make(chan error, 1)
	go func() { waited <- cmd.Wait(); close(done) }()
	select {
	case err := <-waited:
		if errors.Is(err, exec.ErrWaitDelay) {
			log.Printf("%s for %s ran; a process it left running kept its output open", key, server)
			return
		}
		if err != nil {
			log.Printf("%s for %s failed: %v: %s", key, server, err, firstBytes(out.buf.String(), hookOutput))
			return
		}
		log.Printf("%s for %s ran", key, server)
	case <-time.After(hookTimeout):
		Terminate(cmd.Process.Pid, done, 2*time.Second)
		log.Printf("%s for %s did not finish in %s and was stopped", key, server, hookTimeout)
	}
}

func firstBytes(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}
