package cli

import (
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/padmk/portside/internal/app"
	"github.com/padmk/portside/internal/client"
	"github.com/padmk/portside/internal/config"
	"github.com/padmk/portside/internal/daemon"
	"github.com/padmk/portside/internal/docker"
	"github.com/padmk/portside/internal/paths"
	"github.com/padmk/portside/internal/ports"
	"github.com/padmk/portside/internal/server"
	"github.com/padmk/portside/internal/service"
)

func cmdDaemon(e *env, args []string) error {
	if len(args) == 0 {
		err := daemon.Run(e.cfgPath)
		if errors.Is(err, daemon.ErrAlreadyRunning) {
			// A clean exit, so launchd or systemd does not keep starting a second daemon.
			fmt.Fprintf(e.errOut, "%s: %v\n", app.ID, err)
			return nil
		}
		return err
	}
	c := client.New(e.port())
	switch args[0] {
	case "start":
		h, err := c.Ensure(e.ctx)
		if err != nil {
			return err
		}
		fmt.Fprintf(e.out, "%s daemon running (pid %d) at %s\n", app.Name, h.PID, c.Base)
	case "stop":
		// The marker keeps background clients (the menu bar app) from starting it again.
		if err := daemon.MarkStopped(); err != nil {
			return err
		}
		if _, err := c.Ping(e.ctx); err != nil {
			fmt.Fprintln(e.out, "daemon is not running")
			return nil
		}
		if err := c.Do(e.ctx, http.MethodPost, "/api/shutdown", nil, nil); err != nil {
			return err
		}
		if !waitGone(c, e, 90*time.Second) {
			return fmt.Errorf("daemon did not stop within 90s")
		}
		fmt.Fprintln(e.out, "daemon stopped; servers it ran were stopped too. Any command starts it again.")
		if service.Query().Installed {
			fmt.Fprintf(e.errOut, "note: the login service starts it at your next login; `%s service uninstall` turns that off\n", app.ID)
		}
	case "restart":
		if _, err := config.Load(e.cfgPath); err != nil {
			return fmt.Errorf("refusing to restart daemon: %w", err)
		}
		return daemonRestart(e, c)
	case "info":
		return daemonInfo(e, c, args[1:])
	case "status":
		h, err := c.Ping(e.ctx)
		if err != nil {
			fmt.Fprintf(e.out, "%s not running (%v)\n", e.paint(dim, "○"), err)
			return silentError{3}
		}
		fmt.Fprintf(e.out, "%s running  pid %d  version %s  %s\n", e.paint(green, "●"), h.PID, h.Version, c.Base)
	case "logs":
		cmd := exec.Command("tail", "-n", "100", "-F", paths.DaemonLog())
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		return cmd.Run()
	default:
		return usageError{"unknown subcommand " + args[0]}
	}
	return nil
}

func cmdService(e *env, args []string) error {
	if len(args) != 1 {
		return usageError{""}
	}
	switch args[0] {
	case "install":
		if err := e.ensureConfig(); err != nil {
			return err
		}
		c := client.New(e.port())
		if _, err := c.Ping(e.ctx); err == nil {
			// Hand over from a manually started daemon; restore brings servers back.
			_ = c.Do(e.ctx, http.MethodPost, "/api/shutdown", restartBody, nil)
			waitGone(c, e, 90*time.Second)
		}
		if err := service.Install(e.cfgPath); err != nil {
			return err
		}
		c = client.New(e.configPort())
		if _, err := c.Ensure(e.ctx); err != nil {
			return err
		}
		fmt.Fprintf(e.out, "%s Installed %s. The daemon now starts at login.\n", e.paint(green, "✓"), paths.ShortenHome(service.File()))
		if service.MenuBarAppStartsDaemon() {
			fmt.Fprintf(e.errOut, "note: the %s app opens at login too and would start the daemon itself; one of the two is enough (turn off Launch at Login in the app, or `%s service uninstall`)\n", app.Name, app.ID)
		}
		lingerHint(e)
	case "uninstall":
		// Removing the service stops its daemon; the marker keeps it stopped.
		if err := daemon.MarkStopped(); err != nil {
			return err
		}
		if err := service.Uninstall(); err != nil {
			return err
		}
		fmt.Fprintln(e.out, "Service removed. The daemon is stopped; any command starts it again on demand.")
	case "status":
		st := service.Query()
		switch {
		case !st.Installed:
			fmt.Fprintf(e.out, "not installed (run `%s service install`)\n", app.ID)
		case st.Running:
			fmt.Fprintf(e.out, "%s installed and running  %s\n", e.paint(green, "●"), paths.ShortenHome(st.File))
		default:
			fmt.Fprintf(e.out, "%s installed, not running  %s\n", e.paint(yellow, "○"), paths.ShortenHome(st.File))
		}
	default:
		return usageError{"unknown subcommand " + args[0]}
	}
	return nil
}

// restartBody tells /api/shutdown that a new daemon follows, so the stop is
// not recorded as deliberate.
var restartBody = map[string]bool{"restart": true}

// daemonRestart replaces a running daemon with this binary in place,
// keeping its servers running. A daemon too old for that, or none at all,
// is stopped and started again, through the login service when it runs
// this daemon, so the service keeps supervising it.
func daemonRestart(e *env, c *client.Client) error {
	old, pingErr := c.Ping(e.ctx)
	running := pingErr == nil
	if running {
		if done, err := upgradeInPlace(e, c, old); done || err != nil {
			return err
		}
	}
	// The new daemon listens where the config says now.
	next := client.New(e.configPort())
	if service.Owns(e.cfgPath) {
		// A daemon started outside the service holds the lock, so a service
		// restart alone would not replace it.
		if running && !service.Query().Running {
			if err := c.Do(e.ctx, http.MethodPost, "/api/shutdown", restartBody, nil); err != nil {
				return err
			}
			if !waitGone(c, e, 90*time.Second) {
				return fmt.Errorf("daemon did not stop within 90s")
			}
			running = false
		}
		err := service.Restart()
		if err == nil {
			deadline := time.Now().Add(100 * time.Second)
			for time.Now().Before(deadline) {
				if h, err := next.Ping(e.ctx); err == nil && (!running || h.PID != old.PID) {
					fmt.Fprintf(e.out, "daemon restarted by the login service (pid %d) at %s; servers that were running are being restored\n", h.PID, next.Base)
					return nil
				}
				time.Sleep(200 * time.Millisecond)
			}
			return fmt.Errorf("the login service did not bring the daemon back; see %s", paths.ShortenHome(paths.DaemonLog()))
		}
		fmt.Fprintf(e.errOut, "note: %v; restarting the daemon outside the login service\n", err)
		_, pingErr = c.Ping(e.ctx)
		running = pingErr == nil
	}
	if running {
		if err := c.Do(e.ctx, http.MethodPost, "/api/shutdown", restartBody, nil); err != nil {
			return err
		}
		if !waitGone(c, e, 90*time.Second) {
			return fmt.Errorf("daemon did not stop within 90s")
		}
	}
	h, err := next.Ensure(e.ctx)
	if err != nil {
		return err
	}
	fmt.Fprintf(e.out, "daemon restarted (pid %d) at %s; servers that were running are being restored\n", h.PID, next.Base)
	return nil
}

// upgradeInPlace asks the daemon to re-execute this binary and waits until
// the new one answers with this version. It reports false without an error
// when the daemon predates in-place upgrades.
func upgradeInPlace(e *env, c *client.Client, old client.Health) (bool, error) {
	exe, err := os.Executable()
	if err != nil {
		return false, nil
	}
	err = c.Do(e.ctx, http.MethodPost, "/api/upgrade", map[string]string{"exe": exe}, nil)
	var apiErr *client.APIError
	if errors.As(err, &apiErr) {
		if apiErr.Status == http.StatusNotFound || apiErr.Status == http.StatusNotImplemented {
			return false, nil
		}
		return true, fmt.Errorf("the daemon keeps running %s: %s", old.Version, apiErr.Msg)
	}
	// On success the old daemon drops the connection as it turns into the
	// new one, so err is usually a transport error here.
	next := client.New(e.configPort())
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		h, err := next.Ping(e.ctx)
		if err == nil && h.PID == old.PID && !h.StartedAt.Equal(old.StartedAt) {
			if h.Version != app.Version {
				return true, fmt.Errorf("the daemon now runs version %s, not %s", h.Version, app.Version)
			}
			fmt.Fprintf(e.out, "daemon upgraded in place to %s (pid %d) at %s; servers kept running\n", h.Version, h.PID, next.Base)
			return true, nil
		}
		if err == nil && h.PID != old.PID {
			fmt.Fprintf(e.out, "daemon restarted (pid %d) at %s; servers that were running are being restored\n", h.PID, next.Base)
			return true, nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	// The new binary did not come up. A fresh daemon recovers the servers
	// as after a crash.
	h, err := next.Ensure(e.ctx)
	if err != nil {
		return true, fmt.Errorf("the upgraded daemon did not answer and none could be started: %w; see %s", err, paths.ShortenHome(paths.DaemonLog()))
	}
	return true, fmt.Errorf("the upgraded daemon did not answer; started a fresh one (pid %d), which restores servers that were running; see %s", h.PID, paths.ShortenHome(paths.DaemonLog()))
}

// Exit codes of `daemon info` besides 0 (running) and 1 (other errors).
const (
	infoNotRunning  = 3
	infoConfigError = 4
)

// daemonInfo prints where the daemon listens as JSON, starting it unless
// --no-start is given. It is the menu bar app's contract: when no daemon
// answers it still prints JSON with running=false and exits 3, or exits 4
// with configError set when the config file does not load.
func daemonInfo(e *env, c *client.Client, args []string) error {
	fs := flag.NewFlagSet("daemon info", flag.ContinueOnError)
	noStart := fs.Bool("no-start", false, "only report a running daemon")
	if _, err := parse(fs, args); err != nil {
		return err
	}
	var h client.Health
	var err error
	if *noStart {
		h, err = c.Ping(e.ctx)
	} else {
		h, err = c.Ensure(e.ctx)
	}
	if err == nil {
		return printJSON(e, map[string]any{"running": true, "url": c.Base, "port": c.Port, "pid": h.PID, "version": h.Version,
			"config": e.cfgPath, "stateDir": paths.StateDir(), "cli": app.Version, "tokenFile": filepath.Join(paths.StateDir(), "auth-token")})
	}
	out := map[string]any{"running": false, "stopped": daemon.Stopped(), "config": e.cfgPath, "stateDir": paths.StateDir(), "cli": app.Version, "error": err.Error()}
	if _, statErr := os.Stat(e.cfgPath); statErr == nil {
		if _, cfgErr := config.Load(e.cfgPath); cfgErr != nil {
			out["configError"] = cfgErr.Error()
			if perr := printJSON(e, out); perr != nil {
				return perr
			}
			return silentError{infoConfigError}
		}
	}
	if !*noStart {
		return err
	}
	if perr := printJSON(e, out); perr != nil {
		return perr
	}
	return silentError{infoNotRunning}
}

// lingerHint explains, on Linux, that the daemon only runs while the account
// is logged in unless lingering is enabled.
func lingerHint(e *env) {
	if service.LingerOff() {
		fmt.Fprintf(e.errOut, "note: systemd stops the daemon and its servers when you log out, and starts it only at login. To keep it running and start it at boot: sudo loginctl enable-linger $USER\n")
	}
}

func cmdVersion(e *env, args []string) error {
	fmt.Fprintf(e.out, "%s %s (%s/%s)\n", app.ID, app.Version, runtime.GOOS, runtime.GOARCH)
	return nil
}

func cmdDoctor(e *env, args []string) error {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	if _, err := parse(fs, args); err != nil {
		return err
	}
	problems := 0
	ok := func(msg string, a ...any) {
		fmt.Fprintf(e.out, "%s %s\n", e.paint(green, "✓"), fmt.Sprintf(msg, a...))
	}
	warn := func(msg string, a ...any) {
		problems++
		fmt.Fprintf(e.out, "%s %s\n", e.paint(yellow, "!"), fmt.Sprintf(msg, a...))
	}
	bad := func(msg string, a ...any) {
		problems++
		fmt.Fprintf(e.out, "%s %s\n", e.paint(red, "✗"), fmt.Sprintf(msg, a...))
	}

	cfg, err := config.Load(e.cfgPath)
	if err != nil {
		bad("config: %v", err)
	} else {
		ok("config %s (%d servers)", paths.ShortenHome(e.cfgPath), len(cfg.Servers))
		for _, w := range cfg.Warnings {
			warn("config: %s", w)
		}
		for _, s := range cfg.Servers {
			if st, err := os.Stat(s.Dir); err != nil || !st.IsDir() {
				warn("%s: directory %s does not exist", s.Name, paths.ShortenHome(s.Dir))
			}
		}
		for _, p := range cfg.PathPrepend {
			if _, err := os.Stat(p); err != nil {
				warn("path entry %s does not exist", p)
			}
		}
	}

	c := client.New(e.port())
	if h, err := c.Ping(e.ctx); err == nil {
		ok("daemon running (pid %d, %s) at %s", h.PID, h.Version, c.Base)
		var st server.State
		if err := c.Do(e.ctx, http.MethodGet, "/api/state", nil, &st); err == nil {
			switch d := st.Docker; {
			case d.Status == docker.Ready:
				ok("container engine %s %s on %s", d.Engine, d.Version, d.Host)
			case d.Error != "":
				fmt.Fprintf(e.out, "%s container engine unavailable: %s\n", e.paint(dim, "•"), d.Error)
			}
		}
	} else if ports.Listening(e.port()) {
		bad("port %d is taken by another program; set `port` in the config", e.port())
	} else if daemon.Stopped() {
		fmt.Fprintf(e.out, "%s daemon stopped on request; any command starts it\n", e.paint(dim, "•"))
	} else {
		fmt.Fprintf(e.out, "%s daemon not running; any command starts it\n", e.paint(dim, "•"))
	}

	svc := service.Query()
	switch {
	case svc.Installed:
		ok("login service installed (%s)", paths.ShortenHome(svc.File))
		if service.LingerOff() {
			warn("systemd stops the daemon when you log out and starts it only at login; to keep it running: sudo loginctl enable-linger $USER")
		}
	case service.MenuBarAppStartsDaemon():
		ok("the %s app opens at login and starts the daemon", app.Name)
	default:
		fmt.Fprintf(e.out, "%s login service not installed; `%s service install` starts the daemon at login\n", e.paint(dim, "•"), app.ID)
	}

	if _, err := exec.LookPath("ps"); err != nil {
		warn("ps not found; CPU and memory usage will be missing")
	}
	_, lsofErr := exec.LookPath("lsof")
	_, ssErr := exec.LookPath("ss")
	if lsofErr != nil && ssErr != nil {
		warn("neither lsof nor ss found; owners of busy ports cannot be shown")
	}
	if sh := os.Getenv("SHELL"); sh != "" && strings.HasSuffix(sh, "fish") {
		fmt.Fprintf(e.out, "%s commands run with bash; your fish PATH is imported from a login shell\n", e.paint(dim, "•"))
	}
	doctorAgents(e, ok, warn)
	if problems == 0 {
		fmt.Fprintln(e.out, "\nEverything looks good.")
		return nil
	}
	return silentError{1}
}
