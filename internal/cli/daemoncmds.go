package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
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
	"github.com/padmk/portside/internal/supervisor"
)

func cmdDaemon(e *env, args []string) error {
	if len(args) == 0 {
		err := daemon.Run(e.cfgPath)
		if errors.Is(err, daemon.ErrAlreadyRunning) || errors.Is(err, daemon.ErrPortHeld) {
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
		ran, err := stopDaemon(e, c)
		if err != nil {
			return err
		}
		if !ran {
			fmt.Fprintln(e.out, "daemon is not running")
			return nil
		}
		fmt.Fprintln(e.out, "daemon stopped; servers it ran were stopped too. Any command starts it again.")
		if service.Query().Installed {
			fmt.Fprintf(e.errOut, "note: the login service starts it at your next login; `%s service uninstall` turns that off\n", app.ID)
		}
	case "restart":
		fs := flag.NewFlagSet("daemon restart", flag.ContinueOnError)
		inPlace := fs.Bool("in-place", false, "only upgrade a running daemon in place")
		allowDowngrade := fs.Bool("allow-downgrade", false, "replace a daemon of a newer release")
		if _, err := parse(fs, args[1:]); err != nil {
			return err
		}
		if _, err := config.Load(e.cfgPath); err != nil {
			return fmt.Errorf("refusing to restart daemon: %w", err)
		}
		if *inPlace {
			return daemonUpgrade(e, c, *allowDowngrade)
		}
		return daemonRestart(e, c, *allowDowngrade)
	case "info":
		return daemonInfo(e, c, args[1:])
	case "status":
		h, err := c.Ping(e.ctx)
		if err != nil {
			fmt.Fprintf(e.out, "%s not running (%v)\n", e.paint(dim, "○"), err)
			return silentError{3}
		}
		if !h.Verified {
			fmt.Fprintf(e.out, "%s unverified  pid %d  version %s  %s: it cannot prove it is this account's daemon\n", e.paint(yellow, "●"), h.PID, h.Version, c.Base)
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
		if !service.Supported() && runtime.GOOS == "linux" {
			fmt.Fprint(e.out, service.NoSystemdHint())
			return nil
		}
		if err := e.ensureConfig(); err != nil {
			return err
		}
		c := client.New(e.port())
		if _, err := c.Ping(e.ctx); err == nil {
			// Hand over from a manually started daemon; restore brings servers back.
			_ = shutdown(e, c, restartBody)
			_ = waitGone(c, e)
		}
		// Locations an earlier version kept under XDG variables move now that
		// no daemon uses them, before the service is pinned to them again.
		cfgPath, notes := paths.MoveLegacyForService(e.cfgPath)
		for _, note := range notes {
			fmt.Fprintf(e.errOut, "%s: %s\n", app.ID, note)
		}
		e.cfgPath = cfgPath
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
		// The daemon may run outside the service (started by the app or a
		// command), so it is stopped first, the same way as `daemon stop`.
		ran, err := stopDaemon(e, client.New(e.port()))
		if err != nil {
			return err
		}
		if err := service.Uninstall(); err != nil {
			return err
		}
		if ran {
			fmt.Fprintln(e.out, "Service removed and the daemon stopped, with the servers it ran. Any command starts it again on demand.")
		} else {
			fmt.Fprintln(e.out, "Service removed; no daemon was running. Any command starts one on demand.")
		}
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

// stopDaemon stops the daemon c reaches, with its servers, and waits until it
// has exited. It reports whether one was running. The stop marker keeps
// background clients (the menu bar app) from starting it again.
func stopDaemon(e *env, c *client.Client) (bool, error) {
	if err := daemon.MarkStopped(); err != nil {
		return false, err
	}
	if _, err := c.Ping(e.ctx); err != nil {
		return false, nil
	}
	if err := shutdown(e, c, nil); err != nil {
		// Nothing stopped, so nothing may keep the daemon from running.
		daemon.ClearStopped()
		return true, err
	}
	return true, waitGone(c, e)
}

// restartBody tells /api/shutdown that a new daemon follows, so the stop is
// not recorded as deliberate.
var restartBody = map[string]bool{"restart": true}

// shutdown asks the daemon to exit through the API. A daemon from before
// verified requests cannot prove itself, so the client sends it nothing
// signed (or it refuses signatures as unauthenticated); when it is provably
// this account's daemon (the process of this user listening on its port,
// with the pid it reports, holding this state directory's lock), it gets
// SIGTERM instead, which stops it the way a restart does: no stop marker,
// servers restored by the next daemon.
func shutdown(e *env, c *client.Client, body any) error {
	err := c.Do(e.ctx, http.MethodPost, "/api/shutdown", body, nil)
	var apiErr *client.APIError
	if !errors.Is(err, client.ErrUnverified) && (!errors.As(err, &apiErr) || apiErr.Status != http.StatusUnauthorized) {
		return err
	}
	h, pingErr := c.Ping(e.ctx)
	if pingErr != nil || h.PID <= 0 {
		return err
	}
	if !daemonListens(e.ctx, c.Port, h.PID) || syscall.Kill(h.PID, 0) != nil {
		return err
	}
	// A daemon of this account with another state directory is not this
	// one: this state directory's lock must be held, and by that process
	// where its open files can be seen.
	ph := client.PortHolder(e.ctx, c.Port)
	if !client.DaemonRunning() || (ph.StateDir != "" && !paths.SamePath(ph.StateDir, paths.StateDir())) {
		if ph.Ours() {
			return errors.New(ph.Refusal(c.Port))
		}
		return err
	}
	fmt.Fprintf(e.errOut, "note: the daemon (pid %d, %s) predates verified requests; stopping it with SIGTERM\n", h.PID, h.Version)
	return syscall.Kill(h.PID, syscall.SIGTERM)
}

// daemonListens reports whether pid, a process of this account, listens on
// port at 127.0.0.1 and at ::1 wherever something answers there. lsof and
// ss list only what this account may see, so a listener of another account
// at either address counts against it.
func daemonListens(ctx context.Context, port, pid int) bool {
	owners := ports.LoopbackListeners(ctx, port)
	if owners["127.0.0.1"] != pid {
		return false
	}
	if owners["::1"] == pid {
		return true
	}
	conn, err := net.DialTimeout("tcp", net.JoinHostPort("::1", strconv.Itoa(port)), 500*time.Millisecond)
	if err != nil {
		return true // nothing listens there
	}
	conn.Close()
	return false
}

// daemonRestart replaces a running daemon with this binary in place,
// keeping its servers running. A daemon too old for that, or none at all,
// is stopped and started again, through the login service when it runs
// this daemon, so the service keeps supervising it.
func daemonRestart(e *env, c *client.Client, allowDowngrade bool) error {
	old, pingErr := c.Ping(e.ctx)
	running := pingErr == nil
	// One that cannot prove itself gets no signed request; shutdown stops it
	// when it is this account's daemon.
	if running && old.Verified {
		if err := refuseDowngrade(old, allowDowngrade); err != nil {
			return err
		}
		if done, err := upgradeInPlace(e, c, old, allowDowngrade); done || err != nil {
			return err
		}
	}
	if service.Owns(e.cfgPath) {
		// A daemon started outside the service holds the lock, so a service
		// restart alone would not replace it.
		if running && !service.Query().Running {
			if err := shutdown(e, c, restartBody); err != nil {
				return err
			}
			if err := waitGone(c, e); err != nil {
				return err
			}
			running = false
		}
		err := service.Restart()
		if err == nil {
			deadline := time.Now().Add(100 * time.Second)
			for time.Now().Before(deadline) {
				next := e.nextClient()
				if h, err := next.Ping(e.ctx); err == nil && h.Verified && (!running || h.PID != old.PID) {
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
		if err := shutdown(e, c, restartBody); err != nil {
			return err
		}
		if err := waitGone(c, e); err != nil {
			return err
		}
	}
	next := e.nextClient()
	h, err := next.Ensure(e.ctx)
	if err != nil {
		return err
	}
	fmt.Fprintf(e.out, "daemon restarted (pid %d) at %s; servers that were running are being restored\n", h.PID, next.Base)
	return nil
}

// daemonUpgrade replaces a running daemon with this binary in place and
// fails instead of stopping it when that is not possible, so its servers
// never restart. The menu bar app runs it for a daemon older than itself.
func daemonUpgrade(e *env, c *client.Client, allowDowngrade bool) error {
	old, err := c.Ping(e.ctx)
	if err != nil {
		return fmt.Errorf("no daemon is running: %w", err)
	}
	if !old.Verified {
		return c.Unverified()
	}
	if err := refuseDowngrade(old, allowDowngrade); err != nil {
		return err
	}
	done, err := upgradeInPlace(e, c, old, allowDowngrade)
	if err != nil {
		return err
	}
	if !done {
		return fmt.Errorf("the daemon (version %s) cannot be upgraded in place; `%s daemon restart` replaces it and restarts its servers", old.Version, app.ID)
	}
	return nil
}

// refuseDowngrade fails when the running daemon is a newer release than
// this command, unless --allow-downgrade was given. The daemon checks the
// same for the binary it is asked to run.
func refuseDowngrade(old client.Health, allow bool) error {
	if allow || !app.Newer(old.Version) {
		return nil
	}
	return fmt.Errorf("the daemon runs %s, newer than this command (%s); `%s daemon restart --allow-downgrade` replaces it anyway", old.Version, app.Version, app.ID)
}

// nextClient is a client for the daemon that comes up next: at the port it
// records in daemon.port once it listens (it may have chosen another port
// than the config names), else where the config says.
func (e *env) nextClient() *client.Client {
	if p := e.envPort(); p > 0 {
		return client.New(p)
	}
	if p := client.LivePort(); p > 0 {
		return client.New(p)
	}
	return client.New(e.configPort())
}

// upgradeInPlace asks the daemon to re-execute this binary and waits until
// the new one answers with this version. It reports false without an error
// when the daemon predates in-place upgrades.
func upgradeInPlace(e *env, c *client.Client, old client.Health, allowDowngrade bool) (bool, error) {
	exe, err := os.Executable()
	if err != nil {
		return false, nil
	}
	err = c.Do(e.ctx, http.MethodPost, "/api/upgrade", map[string]any{"exe": exe, "allowDowngrade": allowDowngrade}, nil)
	if errors.Is(err, client.ErrUnverified) {
		return false, nil
	}
	var apiErr *client.APIError
	if errors.As(err, &apiErr) {
		// Not found or not implemented: a daemon without in-place upgrades.
		// Unauthorized: one from before verified requests.
		if apiErr.Status == http.StatusNotFound || apiErr.Status == http.StatusNotImplemented || apiErr.Status == http.StatusUnauthorized {
			return false, nil
		}
		return true, fmt.Errorf("the daemon keeps running %s: %s", old.Version, apiErr.Msg)
	}
	// On success the old daemon drops the connection as it turns into the
	// new one, so err is usually a transport error here.
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		next := e.nextClient()
		h, err := next.Ping(e.ctx)
		if err == nil && h.Verified && h.PID == old.PID && !h.StartedAt.Equal(old.StartedAt) {
			if h.Version != app.Version {
				return true, fmt.Errorf("the daemon now runs version %s, not %s", h.Version, app.Version)
			}
			fmt.Fprintf(e.out, "daemon %s in place to %s (pid %d) at %s; servers kept running\n", app.Replacement(old.Version), h.Version, h.PID, next.Base)
			return true, nil
		}
		if err == nil && h.Verified && h.PID != old.PID {
			fmt.Fprintf(e.out, "daemon restarted (pid %d) at %s; servers that were running are being restored\n", h.PID, next.Base)
			return true, nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	// The new binary did not come up. A fresh daemon recovers the servers
	// as after a crash.
	next := e.nextClient()
	h, err := next.Ensure(e.ctx)
	if err != nil {
		return true, fmt.Errorf("the upgraded daemon did not answer and none could be started: %w; see %s", err, paths.ShortenHome(paths.DaemonLog()))
	}
	return true, fmt.Errorf("the upgraded daemon did not answer; started a fresh one (pid %d), which restores servers that were running; see %s", h.PID, paths.ShortenHome(paths.DaemonLog()))
}

// waitGone polls until the daemon stops answering, giving it 90s to stop
// its servers.
func waitGone(c *client.Client, e *env) error {
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := c.Ping(e.ctx); err != nil {
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return errors.New("daemon did not stop within 90s")
}

// Exit codes of `daemon info` besides 0 (running) and 1 (other errors); the
// macOS app (macos/Sources/Portside/Daemon.swift) relies on them.
const (
	infoNotRunning  = 3
	infoConfigError = 4
)

// daemonInfo prints where the daemon listens as JSON, starting it unless
// --no-start is given. It is the menu bar app's contract: when no daemon
// answers it still prints JSON with running=false and exits 3, or exits 4
// with configError set when the config file does not load. A daemon that
// answers but cannot prove itself (one from before verified requests, or
// another program) is reported running with verified=false and the version
// it claims, so the app can offer a restart.
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
	// Without a config that loads the port is a guess, so what answers there
	// unproven says nothing about this daemon; the config error comes first.
	var cfgErr error
	if _, statErr := os.Stat(e.cfgPath); statErr == nil {
		_, cfgErr = config.Load(e.cfgPath)
	}
	answered := err == nil || errors.Is(err, client.ErrUnverified)
	if answered && !h.Verified && cfgErr != nil {
		err = c.Unverified()
	} else if answered {
		out := map[string]any{"running": true, "verified": h.Verified, "url": c.Base, "port": c.Port, "pid": h.PID, "version": h.Version,
			"config": e.cfgPath, "stateDir": paths.StateDir(), "cli": app.Version, "tokenFile": filepath.Join(paths.StateDir(), paths.TokenName)}
		if !h.Verified {
			out["error"] = c.Unverified().Error()
		} else if app.Older(h.Version) {
			// The app upgrades such a daemon in place on its own.
			out["outdated"] = true
		}
		return printJSON(e, out)
	}
	out := map[string]any{"running": false, "stopped": daemon.Stopped(), "config": e.cfgPath, "stateDir": paths.StateDir(), "cli": app.Version, "error": err.Error()}
	if cfgErr != nil {
		out["configError"] = cfgErr.Error()
		if perr := printJSON(e, out); perr != nil {
			return perr
		}
		return silentError{infoConfigError}
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
	fix := fs.Bool("fix", false, "")
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

	// Another program, or another account's daemon, on the default port
	// while none of this account's runs: the daemon picks another port.
	movable := cfg != nil && cfg.PortAuto && !client.DaemonRunning()
	c := client.New(e.port())
	h, err := c.Ping(e.ctx)
	taken := (err == nil && !h.Verified) || (err != nil && ports.Listening(e.port()))
	var ph client.Holder
	if taken && !client.DaemonRunning() {
		ph = client.PortHolder(e.ctx, e.port())
	}
	if ph.Ours() {
		bad("%s", ph.Refusal(e.port()))
	} else if taken && movable {
		fmt.Fprintf(e.out, "%s port %d is taken by another program or another account's daemon; the daemon will listen on a fallback port and keep it in the state directory\n", e.paint(dim, "•"), e.port())
	} else if err == nil && !h.Verified {
		bad("port %d answers as %s %s (pid %d) but cannot prove it is this account's daemon; `%s daemon restart` replaces a daemon from before verified requests", e.port(), app.Name, h.Version, h.PID, app.ID)
	} else if err == nil {
		ok("daemon running (pid %d, %s) at %s", h.PID, h.Version, c.Base)
		if app.Older(h.Version) {
			warn("the daemon runs %s, older than this command (%s); `%s daemon restart` updates it", h.Version, app.Version, app.ID)
		}
		// Browsers try ::1 first for *.localhost, so the daemon must hold it.
		if ports.IPv6Loopback() {
			v6 := client.NewAt("::1", e.port())
			if h6, err := v6.Ping(e.ctx); err != nil {
				bad("the daemon does not listen on %s; `%s daemon restart` binds it, or reports what holds it", v6.Base, app.ID)
			} else if !h6.Verified || h6.PID != h.PID {
				bad("%s is held by another program (claims pid %d), and browsers send dashboard requests there first; stop it (`lsof -nP -iTCP:%d -sTCP:LISTEN`) and run `%s daemon restart`", v6.Base, h6.PID, e.port(), app.ID)
			} else {
				ok("the daemon listens on 127.0.0.1 and ::1")
			}
		}
		var st server.State
		if err := c.Do(e.ctx, http.MethodGet, "/api/state", nil, &st); err == nil {
			switch d := st.Docker; {
			case d.Status == docker.Ready:
				ok("container engine %s %s on %s", d.Engine, d.Version, d.Host)
			case d.Error != "":
				fmt.Fprintf(e.out, "%s container engine unavailable: %s\n", e.paint(dim, "•"), d.Error)
			}
			doctorExposure(st, warn, func(msg string, a ...any) {
				fmt.Fprintf(e.out, "%s %s\n", e.paint(dim, "•"), fmt.Sprintf(msg, a...))
			})
		}
	} else if ports.Listening(e.port()) {
		bad("port %d is taken by another program; change `port` in the config", e.port())
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
		if service.MissingDisplay() {
			warn("the login service runs without DISPLAY or WAYLAND_DISPLAY, so notifications and opening an editor or terminal fail; `%[1]s daemon stop` and then `%[1]s daemon start` from this desktop session start it with them (this stops its servers)", app.ID)
		}
	case service.MenuBarAppStartsDaemon():
		ok("the %s app opens at login and starts the daemon", app.Name)
	case !service.Supported():
		fmt.Fprintf(e.out, "%s no systemd, so no login service; `%s daemon start` in your shell profile starts the daemon at login\n", e.paint(dim, "•"), app.ID)
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
	doctorShell(e, ok, warn)
	doctorAgents(e, *fix, ok, warn, bad)
	if problems == 0 {
		fmt.Fprintln(e.out, "\nEverything looks good.")
		return nil
	}
	return silentError{1}
}

// doctorShell reports how servers get their environment: which shell was
// read, whether directly or through bash or zsh, and the PATH it gave.
func doctorShell(e *env, ok, warn func(string, ...any)) {
	r, _ := supervisor.ReadLoginShell()
	sh := paths.ShortenHome(r.Shell)
	switch {
	case r.Err != "":
		warn("could not read the environment of your login shell %s (%s); servers get the daemon's environment, so set `path` in the config if commands are not found", paths.ShortenHome(r.Used), r.Err)
		return
	case r.Fallback():
		fmt.Fprintf(e.out, "%s login shell %s cannot be read directly; servers get the environment of %s -l -i, so set `path` in the config for entries only %s adds\n", e.paint(dim, "•"), sh, paths.ShortenHome(r.Used), filepath.Base(r.Shell))
	default:
		ok("servers get the environment of your login shell %s", sh)
	}
	fmt.Fprintf(e.out, "  PATH %s\n", shortPath(r.Path, 6))
}

// shortPath shows the first n entries of a PATH value with the home folder
// shortened and the number of the rest.
func shortPath(v string, n int) string {
	parts := filepath.SplitList(v)
	shown := make([]string, 0, n)
	for i, p := range parts {
		if i == n {
			break
		}
		shown = append(shown, paths.ShortenHome(p))
	}
	out := strings.Join(shown, string(os.PathListSeparator))
	if len(parts) > n {
		out += fmt.Sprintf(" (+%d more)", len(parts)-n)
	}
	return out
}
