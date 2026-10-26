package cli

import (
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/padmk/portside/internal/app"
	"github.com/padmk/portside/internal/client"
	"github.com/padmk/portside/internal/config"
	"github.com/padmk/portside/internal/server"
)

// Exit codes of check, a contract for monitors.
const (
	checkDegraded = 1
	checkUnknown  = 2
)

type checkServer struct {
	Name     string `json:"name"`
	Status   string `json:"status"`
	Expected bool   `json:"expected"`
	Ready    bool   `json:"ready"`
	Reason   string `json:"reason,omitempty"`
}

type checkDaemon struct {
	Version string `json:"version"`
	PID     int    `json:"pid"`
}

type checkReport struct {
	OK      bool          `json:"ok"`
	Daemon  checkDaemon   `json:"daemon"`
	Servers []checkServer `json:"servers"`
}

// cmdCheck tells a monitor whether every server that should run is ready,
// by its exit code. It never starts a daemon.
func cmdCheck(e *env, args []string) error {
	fs := flag.NewFlagSet("check", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "")
	if _, err := parse(fs, args); err != nil {
		return err
	}
	st, err := e.checkState()
	if err != nil {
		if *asJSON {
			if perr := printJSON(e, map[string]any{"ok": false, "error": err.Error()}); perr != nil {
				return perr
			}
		} else {
			fmt.Fprintf(e.errOut, "%s %s\n", e.paint(red, "error:"), err)
		}
		return silentError{checkUnknown}
	}
	report := checkReport{Daemon: checkDaemon{Version: st.Daemon.Version, PID: st.Daemon.PID}, Servers: []checkServer{}}
	var degraded []checkServer
	expected, ready := 0, 0
	for _, s := range st.Servers {
		h := s.Health()
		cs := checkServer{Name: s.Name, Status: string(s.Status), Expected: h.Expected, Ready: h.Ready, Reason: h.Reason}
		report.Servers = append(report.Servers, cs)
		if h.Expected {
			expected++
			if h.Ready {
				ready++
			}
		}
		if h.Degraded() {
			degraded = append(degraded, cs)
		}
	}
	report.OK = len(degraded) == 0
	if *asJSON {
		if err := printJSON(e, report); err != nil {
			return err
		}
	} else {
		nameW := 4
		for _, s := range degraded {
			nameW = max(nameW, len(s.Name))
		}
		for _, s := range degraded {
			fmt.Fprintf(e.out, "  %s %-*s  %-9s %s\n", e.paint(red, "●"), nameW, s.Name, s.Status, e.paint(dim, s.Reason))
		}
		summary := fmt.Sprintf("%d of %d servers that should run are ready", ready, expected)
		switch expected {
		case 0:
			summary = "No server should run"
		case 1:
			summary = fmt.Sprintf("%d of 1 server that should run is ready", ready)
		}
		if report.OK {
			summary = e.paint(green, summary)
		} else {
			summary = e.paint(red, summary)
		}
		fmt.Fprintln(e.out, summary)
	}
	if !report.OK {
		return silentError{checkDegraded}
	}
	return nil
}

// checkState reads the state from a daemon that already runs and proves
// itself, and refuses when the config does not load.
func (e *env) checkState() (*server.State, error) {
	if _, err := os.Stat(e.cfgPath); err == nil {
		if _, err := config.Load(e.cfgPath); err != nil {
			return nil, fmt.Errorf("config: %w", err)
		}
	}
	c := client.New(e.port())
	if _, err := c.Running(e.ctx); err != nil {
		if errors.Is(err, client.ErrNotRunning) {
			if client.DaemonRunning() {
				return nil, fmt.Errorf("the daemon runs, but this process cannot reach it on port %d", c.Port)
			}
			return nil, fmt.Errorf("the daemon is not running (`%s daemon start` starts it)", app.ID)
		}
		return nil, err
	}
	st, err := e.stateFrom(c)
	if err != nil {
		return nil, err
	}
	if st.Config.Error != "" {
		return nil, fmt.Errorf("config: %s", st.Config.Error)
	}
	return st, nil
}
