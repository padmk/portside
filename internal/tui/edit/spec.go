package edit

import (
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/padmk/portside/internal/config"
	"github.com/padmk/portside/internal/tui/form"
	"github.com/padmk/portside/internal/viewfmt"
)

// restartChoices are the restart policies; "" leaves the key out, so the
// daemon's default applies.
var restartChoices = []string{"", "on-failure", "always", "no"}

// secretLine reports whether a NAME=value line of an env list is hidden
// until the field is edited (viewfmt.SecretEnv).
func secretLine(line string) bool {
	k, v, _ := strings.Cut(line, "=")
	return viewfmt.SecretEnv(strings.TrimSpace(k), v)
}

// envText is a variables table as NAME=value lines, sorted by name, and
// whether a value holds a line break, which a line per variable cannot
// show: those are written escaped, as \n, and the field is read-only.
func envText(env map[string]string) (string, bool) {
	keys := slices.Sorted(maps.Keys(env))
	lines := make([]string, len(keys))
	multi := false
	for i, k := range keys {
		v := env[k]
		if strings.ContainsAny(v, "\r\n") {
			multi = true
			v = strings.NewReplacer("\r", `\r`, "\n", `\n`).Replace(v)
		}
		lines[i] = k + "=" + v
	}
	return strings.Join(lines, "\n"), multi
}

// envField is the field of a variables table.
func envField(key, label, help string, env map[string]string) form.Field {
	value, multi := envText(env)
	f := form.Field{Key: key, Label: label, Kind: form.List, Lines: true, Value: value, Secret: secretLine, Help: help}
	if multi {
		f.ReadOnly = "a value spans lines; edit with :config"
	}
	return f
}

// fieldError is a value the form cannot turn into the request, under the
// field with the key.
type fieldError struct{ key, msg string }

func (e fieldError) Error() string { return e.msg }

// envMap parses NAME=value lines; nil for none. Only the name is
// trimmed: a value keeps every character after the first =, spaces
// included.
func envMap(f *form.Field) (map[string]string, error) {
	out := map[string]string{}
	for line := range strings.SplitSeq(f.Value, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if k = strings.TrimSpace(k); !ok || k == "" {
			return nil, fieldError{f.Key, fmt.Sprintf("%q: write one NAME=value per line", strings.TrimSpace(line))}
		}
		out[k] = v
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

// changedKeys are the keys of the form's fields whose value differs from
// orig, the fields built from what was loaded. Only those are read back:
// the others keep the loaded value as it is written, so a save never
// changes a value the user did not edit (trailing spaces, an escaped line
// break, a spelling the form would normalize).
func changedKeys(m *form.Model, orig []form.Field) map[string]bool {
	out := map[string]bool{}
	for _, o := range orig {
		if f := m.Field(o.Key); f != nil && (f.Value != o.Value || f.On != o.On) {
			out[o.Key] = true
		}
	}
	return out
}

// number parses a number field: nil for empty.
func number(f *form.Field) (*int, error) {
	v := strings.TrimSpace(f.Value)
	if v == "" {
		return nil, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return nil, fieldError{f.Key, f.Label + " must be a number"}
	}
	return &n, nil
}

func items(f *form.Field) []string {
	if it := f.Items(); len(it) > 0 {
		return it
	}
	return nil
}

func text(m *form.Model, key string) string { return strings.TrimSpace(m.Value(key)) }

// specFields are a server's keys as form fields, in the order and with
// the words of the dashboard's server dialog. The hook commands, whose
// "inherit" and "none" a text field cannot tell apart, and the comment are
// kept from the spec as loaded; :config edits them.
func specFields(sp config.Spec, isNew bool) []form.Field {
	port := ""
	if sp.Port != nil {
		port = strconv.Itoa(*sp.Port)
	}
	name := form.Field{Key: "name", Label: "Name", Value: sp.Name, Help: "Lowercase letters, digits and dashes; reachable at <name>.localhost."}
	if !isNew {
		name.ReadOnly = "rename with :config"
		name.Help = ""
	}
	return []form.Field{
		name,
		{Key: "dir", Aliases: []string{"directory"}, Label: "Directory", Value: sp.Dir, Placeholder: "the projects folder", Help: "Absolute, or relative to the projects folder."},
		{Key: "cmd", Aliases: []string{"command"}, Label: "Command", Value: sp.Cmd, Placeholder: "required", Help: "Use {PORT} where the server takes its port."},
		{Key: "port", Label: "Port", Kind: form.Number, Value: port, Placeholder: "automatic", Help: "Empty: the lowest free port from 3001, written in when the config loads; 0 for none."},
		{Key: "group", Label: "Group", Value: sp.Group, Placeholder: "no group"},
		{Key: "depends_on", Aliases: []string{"dependency"}, Label: "Depends on", Kind: form.List, Value: form.JoinItems(sp.DependsOn, false), Help: "Servers and tasks that start first, separated by commas."},
		envField("env", "Environment", "One NAME=value per line; {PORT} works in values.", sp.Env),
		{Key: "oneshot", Label: "Task (runs once)", Kind: form.Toggle, On: sp.Oneshot, Help: "Runs to completion, such as a migration, and has no port."},
		{Key: "restart", Label: "Restart policy", Kind: form.Choice, Choices: restartChoices, Value: sp.Restart, Placeholder: "default (on-failure, no for a task)", Help: "space or h/l picks another."},
		{Advanced: true, Key: "health", Label: "Health path", Value: sp.Health, Section: "Readiness", Help: "HTTP path that must answer 200 to 399, such as /healthz."},
		{Advanced: true, Key: "ready_log", Label: "Ready when a line matches", Value: sp.ReadyLog, Help: "Regular expression for a whole line of output."},
		{Advanced: true, Key: "ready_after", Label: "Ready after", Value: sp.ReadyAfter, Help: "How long the process must run first, such as 10s."},
		{Advanced: true, Key: "enabled", Label: "Enabled", Kind: form.Toggle, On: sp.Enabled == nil || *sp.Enabled, Section: "Lifecycle and limits", Help: "Off: left out of --all, restore and the schedule."},
		{Advanced: true, Key: "stop_timeout", Label: "Stop timeout", Value: sp.Stop, Placeholder: "the default", Help: "How long to wait after Ctrl-C, such as 30s."},
		{Advanced: true, Key: "memory_limit", Label: "Memory limit", Value: sp.MemoryLimit, Placeholder: "the default", Help: "Such as 2GB; 0 turns the inherited limit off."},
		{Advanced: true, Key: "retry_after", Label: "Retry after", Value: sp.RetryAfter, Placeholder: "the default", Help: "After the restarts gave up, such as 5m; 0s turns retries off."},
		{Advanced: true, Key: "unready_after", Label: "Not responding after", Value: sp.UnreadyAfter, Placeholder: "the default", Help: "How long it must not answer before on_unready runs."},
		{Advanced: true, Key: "schedule", Label: "Schedule (cron)", Value: sp.Schedule, Section: "Schedule and file watching", Help: "Local time, such as 30 2 * * * for 02:30 every day."},
		{Advanced: true, Key: "watch", Label: "Restart on changes to", Kind: form.List, Lines: true, Value: form.JoinItems(sp.Watch, true), Help: "One pattern per line, relative to the directory, such as src/**/*.py."},
		{Advanced: true, Key: "wrap", Label: "Tool environment", Value: sp.Wrap, Section: "Environment and tools", Placeholder: "auto", Help: "auto detects direnv, devbox, mise, fnm, nvm; none; or a command prefix."},
		{Advanced: true, Key: "umask", Label: "umask", Value: sp.Umask, Placeholder: "the default", Help: "Mode mask for files it creates, such as 0007."},
		{Advanced: true, Key: "lan", Label: "Reachable from other devices", Kind: form.Toggle, On: sp.LAN, Help: "Listening on the network is then not pointed out."},
		{Advanced: true, Key: "open", Label: "Open path", Value: sp.Open, Section: "Dashboard and containers", Help: "Appended to the URL, such as /admin."},
		{Advanced: true, Key: "icon", Label: "Icon", Value: sp.Icon, Placeholder: "detected", Help: "Relative to the directory or absolute."},
		{Advanced: true, Key: "projects", Label: "Compose projects", Kind: form.List, Value: form.JoinItems(sp.Projects, false), Help: "Project names attached to this server, separated by commas."},
		{Advanced: true, Key: "hooks", Label: "Commands on events", Value: hooksSet(sp), Placeholder: "from the settings", Section: "Commands on events",
			Help: "on_crash, on_unready and on_recover.", ReadOnly: "edit with :config"},
	}
}

// hooksSet names the hook commands the server sets itself, "" when it
// inherits all of them; an empty one is written as "none".
func hooksSet(sp config.Spec) string {
	var out []string
	for _, h := range []struct {
		key string
		cmd *string
	}{{"on_crash", sp.OnCrash}, {"on_unready", sp.OnUnready}, {"on_recover", sp.OnRecover}} {
		switch {
		case h.cmd == nil:
		case *h.cmd == "":
			out = append(out, h.key+" none")
		default:
			out = append(out, h.key+": "+*h.cmd)
		}
	}
	return strings.Join(out, ", ")
}

// specFrom is the spec the form's values describe: base (the spec as
// loaded, or a proposal) with the key of every field the user changed
// replaced; the other keys stay exactly as loaded.
func specFrom(m *form.Model, base config.Spec) (config.Spec, error) {
	sp := base
	ch := changedKeys(m, specFields(base, false))
	str := func(key string, dst *string, raw bool) {
		if !ch[key] {
			return
		}
		if raw {
			*dst = m.Value(key)
			return
		}
		*dst = text(m, key)
	}
	list := func(key string, dst *[]string) {
		if ch[key] {
			*dst = items(m.Field(key))
		}
	}
	var err error
	str("name", &sp.Name, false)
	str("dir", &sp.Dir, false)
	str("cmd", &sp.Cmd, false)
	if ch["port"] {
		if sp.Port, err = number(m.Field("port")); err != nil {
			return sp, err
		}
	}
	str("group", &sp.Group, false)
	list("depends_on", &sp.DependsOn)
	if ch["env"] {
		if sp.Env, err = envMap(m.Field("env")); err != nil {
			return sp, err
		}
	}
	if ch["oneshot"] {
		sp.Oneshot = m.On("oneshot")
	}
	str("restart", &sp.Restart, true)
	str("health", &sp.Health, false)
	str("ready_log", &sp.ReadyLog, true)
	str("ready_after", &sp.ReadyAfter, false)
	if ch["enabled"] {
		// Enabled stays unwritten while it is the default.
		if on := m.On("enabled"); base.Enabled != nil || !on {
			sp.Enabled = &on
		} else {
			sp.Enabled = nil
		}
	}
	str("stop_timeout", &sp.Stop, false)
	str("memory_limit", &sp.MemoryLimit, false)
	str("retry_after", &sp.RetryAfter, false)
	str("unready_after", &sp.UnreadyAfter, false)
	str("schedule", &sp.Schedule, false)
	list("watch", &sp.Watch)
	str("wrap", &sp.Wrap, false)
	str("umask", &sp.Umask, false)
	if ch["lan"] {
		sp.LAN = m.On("lan")
	}
	str("open", &sp.Open, false)
	str("icon", &sp.Icon, false)
	list("projects", &sp.Projects)
	return sp, nil
}
