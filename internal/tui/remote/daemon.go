package remote

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/padmk/portside/internal/app"
	"github.com/padmk/portside/internal/client"
	"github.com/padmk/portside/internal/paths"
	"github.com/padmk/portside/internal/server"
	"github.com/padmk/portside/internal/tui/ui"
)

// restartWait is how long the terminal UI waits for the daemon to answer
// again after an upgrade or a restart in place.
const restartWait = 60 * time.Second

// Restart verbs.
const (
	VerbUpgrade = "upgrade"
	VerbRestart = "restart"
)

// restarting is an upgrade or a restart in place in progress: the
// daemon's process and start time before it, to tell the new one.
type restarting struct {
	seq     int
	verb    string
	pid     int
	started time.Time
	version string
}

type (
	// sentMsg is the reply to POST /api/upgrade: none on success (the
	// daemon drops the connection as it turns into the new one), an API
	// error when the daemon keeps running, or old when it predates
	// upgrades in place.
	sentMsg struct {
		seq int
		err error
		old bool
	}
	restartTimeoutMsg struct{ seq int }
	// execDoneMsg is the end of a command the terminal UI suspended for.
	execDoneMsg struct {
		what string
		err  error
		// seq is the restart the command falls back for, 0 for none.
		seq int
	}
)

// UpgradeOff is why `:upgrade` does nothing: it replaces the daemon of
// this computer with this binary, in place, only when that is newer.
func UpgradeOff(env *ui.Env) string {
	if why := daemonOff(env); why != "" {
		return why
	}
	v := env.State.Daemon.Version
	switch {
	case app.Newer(v):
		return fmt.Sprintf("runs %s, newer than this command (%s)", versionLabel(v), versionLabel(app.Version))
	case !app.Older(v):
		return "already runs this version (" + versionLabel(v) + ")"
	}
	return ""
}

// RestartOff is why `:restart-daemon` does nothing.
func RestartOff(env *ui.Env) string {
	if why := daemonOff(env); why != "" {
		return why
	}
	if v := env.State.Daemon.Version; app.Newer(v) {
		return fmt.Sprintf("the daemon runs %s, newer than this command (%s); `%s daemon restart --allow-downgrade` replaces it anyway", versionLabel(v), versionLabel(app.Version), app.ID)
	}
	return ""
}

func daemonOff(env *ui.Env) string {
	switch {
	case env.Session.Remote():
		return "only on this computer; :on local returns"
	case env.State == nil || !env.Connected:
		return "not connected to the daemon"
	}
	return ""
}

// confirmRestart asks before an upgrade or a restart in place.
func (h *Host) confirmRestart(env *ui.Env, verb string) tea.Cmd {
	v := versionLabel(env.State.Daemon.Version)
	title, body, confirm := "Restart the daemon?",
		"Restarts the daemon ("+v+") in place with this binary ("+versionLabel(app.Version)+"); servers keep running. "+
			"A daemon too old to restart in place is stopped and started instead, which restarts its servers. The terminal UI reconnects on its own.",
		"Restart"
	if verb == VerbUpgrade {
		title, body, confirm = "Upgrade the daemon?",
			"Replaces the daemon ("+v+") in place with this binary ("+versionLabel(app.Version)+"); servers keep running. The terminal UI reconnects on its own.",
			"Upgrade"
	}
	d := env.State.Daemon
	return func() tea.Msg {
		return ui.ConfirmMsg{Title: title, Body: body, Confirm: confirm, Yes: func() tea.Msg {
			return beginMsg{verb: verb, pid: d.PID, started: d.StartedAt, version: d.Version}
		}}
	}
}

// beginMsg starts a confirmed upgrade or restart.
type beginMsg struct {
	verb    string
	pid     int
	started time.Time
	version string
}

// begin asks the daemon to re-execute this binary in place, as
// `daemon restart --in-place` does (internal/cli/daemoncmds.go
// upgradeInPlace), and waits for the new daemon on the live stream.
func (h *Host) begin(msg beginMsg) tea.Cmd {
	h.seq++
	h.restart = &restarting{seq: h.seq, verb: msg.verb, pid: msg.pid, started: msg.started, version: msg.version}
	seq, c := h.seq, h.Local
	send := func() tea.Msg {
		exe, err := os.Executable()
		if err != nil {
			return sentMsg{seq: seq, err: err}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		err = c.Do(ctx, http.MethodPost, "/api/upgrade", map[string]any{"exe": exe}, nil)
		if errors.Is(err, client.ErrUnverified) {
			return sentMsg{seq: seq, old: true}
		}
		if apiErr, ok := errors.AsType[*client.APIError](err); ok {
			switch apiErr.Status {
			case http.StatusNotFound, http.StatusNotImplemented, http.StatusUnauthorized:
				return sentMsg{seq: seq, old: true}
			}
			return sentMsg{seq: seq, err: errors.New(apiErr.Msg)}
		}
		// A transport error is the usual answer: the connection dropped as
		// the daemon turned into the new one.
		return sentMsg{seq: seq}
	}
	timeout := tea.Tick(restartWait, func(time.Time) tea.Msg { return restartTimeoutMsg{seq: seq} })
	return tea.Batch(send, timeout)
}

// Restarting is the top bar's word while an upgrade or a restart in
// place runs, "" otherwise.
func (h *Host) Restarting() string {
	if h.restart == nil {
		return ""
	}
	return "restarting, reconnecting"
}

// Observe sees a snapshot: once the daemon answers with another start
// time than before the restart, the restart is over and a notice says
// with what version.
func (h *Host) Observe(st *server.State) tea.Cmd {
	r := h.restart
	if r == nil || st == nil || st.Daemon.StartedAt.Equal(r.started) {
		return nil
	}
	h.restart = nil
	d := st.Daemon
	text := fmt.Sprintf("daemon restarted in place (pid %d) with %s; servers kept running", d.PID, versionLabel(d.Version))
	switch {
	case d.PID != r.pid:
		text = fmt.Sprintf("daemon restarted (pid %d) with %s; servers that were running are being restored", d.PID, versionLabel(d.Version))
	case r.verb == VerbUpgrade || d.Version != r.version:
		text = fmt.Sprintf("daemon %s in place from %s to %s (pid %d); servers kept running", replacement(r.version, d.Version), versionLabel(r.version), versionLabel(d.Version), d.PID)
	}
	return notice(ui.LevelSuccess, text)
}

// replacement is what an in-place change from one version to another was.
func replacement(from, to string) string {
	if from == to {
		return "replaced"
	}
	if to == app.Version {
		return app.Replacement(from)
	}
	return "replaced"
}

// runCLI suspends the terminal UI and runs this binary with args in the
// same config and state (the environment carries them), then waits for
// enter so its output can be read. It ends with the program (ui.Process);
// seq is the restart it falls back for, 0 for none.
func runCLI(env *ui.Env, seq int, what string, args ...string) tea.Cmd {
	exe, err := os.Executable()
	if err != nil {
		return notice(ui.LevelError, what+": "+err.Error())
	}
	script := `"$0" "$@"; status=$?; printf '\n[exit %s] Press enter to return to the terminal UI ' "$status"; read _; exit $status`
	cmd := exec.Command("/bin/sh", append([]string{"-c", script, exe}, args...)...)
	return ui.Exec(&ui.Process{Ctx: env.ProgramContext(), Cmd: cmd}, func(err error) tea.Msg {
		return execDoneMsg{what: what, err: err, seq: seq}
	})
}

// daemonLines are the daemon's details: version, process, port, uptime,
// files, the dashboard's address and where the token is.
func daemonLines(env *ui.Env, now time.Time) [][2]string {
	st := env.State
	if st == nil {
		return [][2]string{{"Daemon", "not connected yet"}}
	}
	d, cfg := st.Daemon, st.Config
	version := versionLabel(d.Version)
	switch {
	case env.Session.Remote():
	case app.Older(d.Version):
		version += " (this command is " + versionLabel(app.Version) + "; :upgrade replaces it in place)"
	case app.Newer(d.Version):
		version += " (newer than this command, " + versionLabel(app.Version) + ")"
	}
	out := [][2]string{}
	if env.Session.Remote() {
		out = append(out, [2]string{"Computer", plain(env.Session.Computer)})
	}
	out = append(out,
		[2]string{"Version", version},
		[2]string{"Process", "pid " + strconv.Itoa(d.PID)},
		[2]string{"Port", strconv.Itoa(d.Port)},
	)
	if !d.StartedAt.IsZero() {
		out = append(out, [2]string{"Uptime", uptime(now.Sub(d.StartedAt)) + " (since " + d.StartedAt.Local().Format("Jan 2 15:04") + ")"})
	}
	cfgPath := cfg.PathDisplay
	if cfgPath == "" {
		cfgPath = cfg.Path
	}
	out = append(out, [2]string{"Config", plain(cfgPath)})
	if cfg.StateDir != "" {
		out = append(out, [2]string{"State", plain(paths.ShortenHome(cfg.StateDir))})
	}
	if env.Session.Remote() {
		return out
	}
	out = append(out, [2]string{"Dashboard", client.DashboardURL(d.Port) + " (`" + app.ID + " open` signs a browser in)"})
	if cfg.StateDir != "" {
		out = append(out, [2]string{"Token", paths.ShortenHome(filepath.Join(cfg.StateDir, paths.TokenName)) + " (requests are signed with it; it is never sent)"})
	}
	return out
}

// uptime is a duration in its two largest units: "3d 4h", "2h 5m", "40s".
func uptime(d time.Duration) string {
	d = max(0, d.Round(time.Second))
	days, hours, mins := int(d/(24*time.Hour)), int(d/time.Hour)%24, int(d/time.Minute)%60
	switch {
	case days > 0:
		return fmt.Sprintf("%dd %dh", days, hours)
	case hours > 0:
		return fmt.Sprintf("%dh %dm", hours, mins)
	case mins > 0:
		return fmt.Sprintf("%dm %ds", mins, int(d/time.Second)%60)
	}
	return fmt.Sprintf("%ds", int(d/time.Second))
}
