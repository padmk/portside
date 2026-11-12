package detail

import (
	"fmt"
	"strings"
	"time"

	"github.com/padmk/portside/internal/ports"
	"github.com/padmk/portside/internal/supervisor"
	"github.com/padmk/portside/internal/tui/ui"
	"github.com/padmk/portside/internal/viewfmt"
)

// detailsLayout right-aligns the labels in front of their values.
var detailsLayout = layout{labelW: 12, right: true, sep: "  "}

// detailFields are the server's facts in groups: its state and history,
// its addresses, what it runs and where, and how the config ties it to
// others. v's addresses must already be as they open from here
// (Session.URL).
func detailFields(env *ui.Env, v supervisor.View) []field {
	s := env.Styles
	now := env.Now()
	var fields []field
	gap := false
	group := func() { gap = true }
	add := func(f field) {
		if f.value == "" {
			return
		}
		f.gap, gap = gap, false
		if f.copy == "" && !f.info {
			f.copy = f.value
		}
		fields = append(fields, f)
	}
	text := func(label, value string) field { return field{label: label, value: value, style: s.Text} }

	// State and history.
	d := v.Display()
	add(field{label: "State", value: env.Glyphs.State(d, env.Frame) + " " + string(d), style: s.State(string(d)), copy: string(d)})
	add(text("Note", viewfmt.StateNote(v)))
	if !v.Enabled && !v.Temporary {
		add(text("Enabled", "no: --all and restore skip it"))
	}
	if v.StartedAt != nil {
		value := detailTime(*v.StartedAt, now)
		if v.Status == supervisor.Running || v.Status == supervisor.Starting {
			value += " (for " + durationLabel(now.Sub(*v.StartedAt)) + ")"
		}
		add(text("Started", value))
	}
	if v.ReadyAt != nil && v.StartedAt != nil && v.ReadyAt.Sub(*v.StartedAt) >= time.Second {
		add(text("Ready", detailTime(*v.ReadyAt, now)+" (after "+durationLabel(v.ReadyAt.Sub(*v.StartedAt))+")"))
	}
	if v.ExitedAt != nil || v.ExitCode != nil {
		var parts []string
		if v.ExitedAt != nil {
			parts = append(parts, detailTime(*v.ExitedAt, now))
		}
		if v.ExitCode != nil {
			parts = append(parts, fmt.Sprintf("exit code %d", *v.ExitCode))
		}
		add(text("Exited", strings.Join(parts, ", ")))
	}
	add(text("Restart", restartValue(v)))
	if v.Schedule != "" {
		value := v.Schedule
		if v.NextRun != nil {
			value += " (next " + viewfmt.NextRunLabel(*v.NextRun, now) + ")"
		}
		add(field{label: "Schedule", value: value, style: s.Text, copy: v.Schedule})
		if v.LastRun != nil {
			add(text("Last run", detailTime(*v.LastRun, now)))
		}
	}
	if v.PID > 0 {
		add(field{label: "Process", value: fmt.Sprintf("pid %d", v.PID), style: s.Text, copy: fmt.Sprint(v.PID)})
	}

	// Addresses.
	group()
	add(field{label: "URL", value: v.URL, style: s.Link, link: v.URL})
	add(field{label: "Address", value: v.Alias, style: s.Link, link: v.Alias})
	if v.Port > 0 {
		value := fmt.Sprint(v.Port)
		if v.PortAuto {
			value += " (chosen automatically)"
		}
		add(field{label: "Port", value: value, style: s.Port, copy: fmt.Sprint(v.Port)})
	}
	if len(v.ListenPorts) > 0 {
		list := make([]string, len(v.ListenPorts))
		for i, n := range v.ListenPorts {
			list[i] = fmt.Sprint(n)
		}
		value := strings.Join(list, ", ")
		if where := exposureLabel(v.Exposure); where != "" {
			value += " (" + where + ")"
		}
		add(field{label: "Listening", value: value, style: s.Port, copy: strings.Join(list, " ")})
	}
	if v.NetworkWarning {
		add(field{label: "Network", value: "reachable from the network; set lan = true if that is intended", style: s.Warn})
	}

	// What runs and where.
	group()
	add(text("Command", v.Cmd))
	dir := v.DirDisplay
	if dir == "" {
		dir = v.Dir
	}
	add(field{label: "Directory", value: dir, style: s.Text, copy: v.Dir})
	if v.Temporary {
		add(field{label: "Copy of", value: v.Origin + " in " + v.Worktree, style: s.Text, copy: v.Worktree})
	}
	add(text("Tool", v.Tool))
	add(text("Watch", strings.Join(v.Watch, ", ")))

	// Config ties.
	group()
	add(text("Group", v.Group))
	add(text("Depends on", strings.Join(v.DependsOn, ", ")))
	return fields
}

// restartValue is the restart policy, the automatic restarts so far and
// a restart waiting to happen.
func restartValue(v supervisor.View) string {
	var parts []string
	if v.Restart != "" {
		parts = append(parts, v.Restart)
	}
	switch v.Restarts {
	case 0:
	case 1:
		parts = append(parts, "restarted once")
	default:
		parts = append(parts, fmt.Sprintf("restarted %d times", v.Restarts))
	}
	if v.RestartPending {
		if v.Retrying {
			parts = append(parts, "retry pending")
		} else {
			parts = append(parts, "restart pending")
		}
	}
	return strings.Join(parts, ", ")
}

// exposureLabel says who can reach the server's listeners.
func exposureLabel(e ports.Exposure) string {
	switch e {
	case ports.Loopback:
		return "this computer only"
	case ports.Tailnet:
		return "this computer and the tailnet"
	case ports.Network:
		return "the network"
	}
	return ""
}

func durationLabel(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	if d < time.Minute {
		return fmt.Sprintf("%d sec", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%d min", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%d hr", int(d.Hours()))
	}
	return fmt.Sprintf("%d days", int(d.Hours()/24))
}

func detailTime(t, now time.Time) string {
	t = t.Local()
	now = now.Local()
	if t.Year() != now.Year() {
		return t.Format("Jan 2 2006 15:04")
	}
	if t.YearDay() != now.YearDay() {
		return t.Format("Jan 2 15:04")
	}
	return t.Format("15:04:05")
}
