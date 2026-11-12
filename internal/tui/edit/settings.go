package edit

import (
	"strconv"

	"github.com/padmk/portside/internal/config"
	"github.com/padmk/portside/internal/tui/form"
)

// settingsView is GET /api/settings: the top-level keys as written, the
// values in effect for those not set, and the file's revision.
type settingsView struct {
	Values   config.Settings `json:"values"`
	Defaults config.Settings `json:"defaults"`
	Revision string          `json:"revision"`
}

// or is a placeholder: the default when there is one, else what empty
// means.
func or(def, empty string) string {
	if def != "" {
		return def
	}
	return empty
}

// settingsFields are the top-level settings as form fields with the
// defaults as placeholders, in the dashboard's order and words. The keys
// the API does not serve here (editor, terminal, share) and the
// dashboard's own appearance stay out.
func settingsFields(v settingsView) []form.Field {
	s, d := v.Values, v.Defaults
	port := ""
	if s.Port != 0 {
		port = strconv.Itoa(s.Port)
	}
	return []form.Field{
		{Key: "root", Label: "Projects folder", Value: s.Root, Placeholder: d.Root, Help: "Relative server directories start here."},
		{Key: "restore", Label: "Restore running servers", Kind: form.Toggle, On: s.Restore, Help: "Servers that were running start again when the daemon comes back."},
		{Key: "notify", Label: "Desktop notifications", Kind: form.Toggle, On: s.Notify, Help: "Crashes, container problems and devices connecting."},
		{Key: "port", Aliases: []string{"dashboard port"}, Label: "Dashboard port", Kind: form.Number, Value: port, Placeholder: "chosen by the daemon", Help: "Applies after a daemon restart."},
		{Key: "stop_timeout", Label: "Stop timeout", Value: s.StopTimeout, Placeholder: d.StopTimeout, Section: "Servers", Help: "How long to wait after Ctrl-C, such as 30s."},
		{Key: "retry_after", Label: "Retry after", Value: s.RetryAfter, Placeholder: or(d.RetryAfter, "never"), Help: "Start a server again this long after its restarts gave up, such as 5m."},
		{Key: "memory_limit", Label: "Memory limit", Value: s.MemoryLimit, Placeholder: or(d.MemoryLimit, "none"), Help: "Per server, such as 2GB. A server over it is stopped as a crash."},
		{Key: "docker_start", Label: "Start Docker command", Value: s.DockerStart, Placeholder: "the active context's engine", Help: "Such as colima start."},
		{Key: "shell", Label: "Shell", Value: s.Shell, Placeholder: d.Shell, Section: "Environment", Help: "Runs each command as <shell> -c <cmd>."},
		{Key: "login_env", Label: "Login environment", Kind: form.Toggle, On: s.LoginEnv, Help: "Off: servers get the daemon's own environment."},
		{Key: "path", Label: "PATH folders", Kind: form.List, Lines: true, Value: form.JoinItems(s.Path, true), Help: "One per line, put in front of PATH for every server."},
		envField("env", "Variables", "One NAME=value per line, for every server.", s.Env),
		{Key: "umask", Label: "umask", Value: s.Umask, Placeholder: or(d.Umask, "the daemon's"), Help: "Mode mask for files servers create, such as 0007."},
		{Key: "on_crash", Label: "On crash", Value: s.OnCrash, Placeholder: "none", Section: "Commands on events", Help: "After a server crashed and its restarts gave up; runs with sh -c."},
		{Key: "on_unready", Label: "When not responding", Value: s.OnUnready, Placeholder: "none", Help: "When a running server stops answering."},
		{Key: "on_recover", Label: "On recovery", Value: s.OnRecover, Placeholder: "none", Help: "When a reported server is ready again."},
		{Key: "unready_after", Label: "Not responding after", Value: s.UnreadyAfter, Placeholder: d.UnreadyAfter, Help: "How long a running server must not answer before the command runs."},
	}
}

// settingsFrom is the settings the form's values describe: the ones v
// served with every changed field's key replaced; the other keys stay
// exactly as loaded.
func settingsFrom(m *form.Model, v settingsView) (config.Settings, error) {
	s := v.Values
	ch := changedKeys(m, settingsFields(v))
	str := func(key string, dst *string) {
		if ch[key] {
			*dst = text(m, key)
		}
	}
	flag := func(key string, dst *bool) {
		if ch[key] {
			*dst = m.On(key)
		}
	}
	if ch["port"] {
		port, err := number(m.Field("port"))
		if err != nil {
			return s, err
		}
		s.Port = 0
		if port != nil {
			s.Port = *port
		}
	}
	if ch["env"] {
		env, err := envMap(m.Field("env"))
		if err != nil {
			return s, err
		}
		s.Env = env
	}
	str("root", &s.Root)
	if ch["path"] {
		s.Path = items(m.Field("path"))
	}
	flag("restore", &s.Restore)
	flag("notify", &s.Notify)
	str("stop_timeout", &s.StopTimeout)
	str("shell", &s.Shell)
	str("docker_start", &s.DockerStart)
	str("on_crash", &s.OnCrash)
	str("on_unready", &s.OnUnready)
	str("on_recover", &s.OnRecover)
	str("unready_after", &s.UnreadyAfter)
	str("retry_after", &s.RetryAfter)
	str("memory_limit", &s.MemoryLimit)
	flag("login_env", &s.LoginEnv)
	str("umask", &s.Umask)
	return s, nil
}
