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
	"syscall"
	"time"

	"github.com/padmk/portside/internal/app"
	"github.com/padmk/portside/internal/client"
	"github.com/padmk/portside/internal/config"
	"github.com/padmk/portside/internal/daemon"
	"github.com/padmk/portside/internal/paths"
	"github.com/padmk/portside/internal/ports"
	"github.com/padmk/portside/internal/service"
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
