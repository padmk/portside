package cli

import (
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/padmk/portside/internal/app"
	"github.com/padmk/portside/internal/client"
	"github.com/padmk/portside/internal/config"
	"github.com/padmk/portside/internal/daemon"
	"github.com/padmk/portside/internal/docker"
	"github.com/padmk/portside/internal/paths"
	"github.com/padmk/portside/internal/ports"
	"github.com/padmk/portside/internal/remote"
	"github.com/padmk/portside/internal/server"
	"github.com/padmk/portside/internal/service"
	"github.com/padmk/portside/internal/supervisor"
)

// cmdDoctor checks the setup and explains problems; --fix connects coding
// agents.
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
			// The daemon's own warnings, such as a state file it cannot save.
			for _, w := range st.Config.Warnings {
				if cfg == nil || !slices.Contains(cfg.Warnings, w) {
					warn("daemon: %s", w)
				}
			}
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
		var ov server.RemoteOverview
		if err := c.Do(e.ctx, http.MethodGet, "/api/remote", nil, &ov); err == nil {
			doctorRemote(ov, ok, warn)
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
	if cfg != nil {
		pid := 0
		if h, err := c.Ping(e.ctx); err == nil && h.Verified {
			pid = h.PID
		}
		doctorCrashReports(cfg, pid, ok, warn)
	}
	doctorShell(e, cfg != nil && cfg.OwnEnv, ok, warn)
	doctorAgents(e, *fix, ok, warn, bad)
	if problems == 0 {
		fmt.Fprintln(e.out, "\nEverything looks good.")
		return nil
	}
	return silentError{1}
}

// doctorCrashReports says where crashes are reported: by on_crash, and on
// Linux whether desktop notifications can be shown at all, by the daemon
// with pid when it runs.
func doctorCrashReports(cfg *config.Config, pid int, ok, warn func(string, ...any)) {
	hooked := countServers(cfg, func(s config.Server) bool { return s.OnCrash != "" })
	if hooked > 0 {
		ok("on_crash reports crashes of %d of %d servers", hooked, len(cfg.Servers))
	}
	if unready := countServers(cfg, func(s config.Server) bool { return s.OnUnready != "" }); unready > 0 {
		ok("on_unready reports servers that stop answering, for %d of %d servers", unready, len(cfg.Servers))
	}
	if runtime.GOOS != "linux" || !cfg.Notify || hooked == len(cfg.Servers) {
		return
	}
	reason := ""
	if _, err := exec.LookPath("notify-send"); err != nil {
		reason = "notify-send is not installed"
	} else if has, known := service.HasDisplay(pid); pid > 0 && known && !has {
		reason = "the daemon runs without a display"
	}
	if reason != "" {
		warn("desktop notifications cannot be shown (%s), so a crash of a server without on_crash notifies no one; set on_crash to run a command for it (docs/configuration.md), or notify = false", reason)
	}
}

// countServers counts the servers in cfg that match.
func countServers(cfg *config.Config, match func(config.Server) bool) int {
	n := 0
	for _, s := range cfg.Servers {
		if match(s) {
			n++
		}
	}
	return n
}

// doctorShell reports how servers get their environment: which shell was
// read, whether directly or through bash or zsh, and the PATH it gave; with
// login_env = false (own), the daemon's own environment.
func doctorShell(e *env, own bool, ok, warn func(string, ...any)) {
	if own {
		ok("servers get the daemon's own environment (login_env = false), with path and env from the config")
		return
	}
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

// doctorRemote reports sharing and the connection to another computer, one
// line each, when they are in use.
func doctorRemote(ov server.RemoteOverview, ok, warn func(string, ...any)) {
	if sh := ov.Share; sh.Enabled {
		connected := 0
		for _, d := range sh.Devices {
			if d.Connected {
				connected++
			}
		}
		if sh.Active {
			ok("sharing with your devices on Tailscale at %s (%d paired, %d connected now)", sh.URL, len(sh.Devices), connected)
		} else {
			warn("sharing is on but not active: %s", sh.Error)
		}
	}
	if cn := ov.Connection; cn != nil {
		forwarded, busy, off, over := 0, 0, 0, 0
		for _, m := range cn.Mirrors {
			switch m.State {
			case "busy":
				busy++
			case "off":
				off++
			case "limit":
				over++
			default:
				forwarded++
			}
		}
		ports := fmt.Sprintf("%d ports forwarded, %d busy here, %d turned off", forwarded, busy, off)
		if over > 0 {
			ports += fmt.Sprintf(", %d over the open-file limit", over)
		}
		if cn.Status == remote.Open {
			ok("connected to %s (%s)", cn.Peer.Name, ports)
		} else {
			warn("connection to %s is %s (%s)%s", cn.Peer.Name, cn.Status, ports, errSuffix(cn.Error))
		}
	}
}

func errSuffix(msg string) string {
	if msg == "" {
		return ""
	}
	return ": " + msg
}
