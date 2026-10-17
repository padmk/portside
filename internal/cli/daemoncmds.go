package cli

import (
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
		return daemon.Run(e.cfgPath)
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
		if svc := service.Query(); svc.Running {
			fmt.Fprintf(e.errOut, "note: the login service is installed; it will start the daemon again. Use `%s service uninstall` to stop that.\n", app.ID)
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
		fmt.Fprintln(e.out, "daemon stopped; servers it ran were stopped too")
	case "restart":
		if _, err := config.Load(e.cfgPath); err != nil {
			return fmt.Errorf("refusing to restart daemon: %w", err)
		}
		if _, err := c.Ping(e.ctx); err == nil {
			if err := c.Do(e.ctx, http.MethodPost, "/api/shutdown", nil, nil); err != nil {
				return err
			}
			if !waitGone(c, e, 90*time.Second) {
				return fmt.Errorf("daemon did not stop within 90s")
			}
		}
		if service.Query().Installed {
			time.Sleep(time.Second) // launchd/systemd brings it back
		}
		// The new daemon listens where the config says now.
		c = client.New(e.configPort())
		h, err := c.Ensure(e.ctx)
		if err != nil {
			return err
		}
		fmt.Fprintf(e.out, "daemon restarted (pid %d) at %s; servers that were running are being restored\n", h.PID, c.Base)
	case "info":
		// Machine-readable; starts the daemon if needed. Used by the macOS app.
		h, err := c.Ensure(e.ctx)
		if err != nil {
			return err
		}
		return printJSON(e, map[string]any{"url": c.Base, "port": c.Port, "pid": h.PID, "version": h.Version,
			"config": e.cfgPath, "cli": app.Version, "tokenFile": filepath.Join(paths.StateDir(), "auth-token")})
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
			_ = c.Do(e.ctx, http.MethodPost, "/api/shutdown", nil, nil)
			waitGone(c, e, 90*time.Second)
		}
		if err := service.Install(e.cfgPath); err != nil {
			return err
		}
		if _, err := c.Ensure(e.ctx); err != nil {
			return err
		}
		fmt.Fprintf(e.out, "%s Installed %s. The daemon now starts at login.\n", e.paint(green, "✓"), paths.ShortenHome(service.File()))
	case "uninstall":
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
	} else {
		fmt.Fprintf(e.out, "%s daemon not running; any command starts it\n", e.paint(dim, "•"))
	}

	svc := service.Query()
	if svc.Installed {
		ok("login service installed (%s)", paths.ShortenHome(svc.File))
	} else {
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
