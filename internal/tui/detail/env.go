package detail

import (
	"sort"
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/padmk/portside/internal/config"
	"github.com/padmk/portside/internal/tui/ui"
	"github.com/padmk/portside/internal/viewfmt"
)

// isSecretEnv and secretMask are the shared rule (viewfmt.SecretEnv).
var isSecretEnv = viewfmt.SecretEnv

const secretMask = viewfmt.SecretMask

// envLayout puts the variables' names left-aligned before " = ".
func envLayout(spec config.Spec) layout {
	w := 0
	for k := range spec.Env {
		w = max(w, ansi.StringWidth(viewfmt.PlainText(k)))
	}
	return layout{labelW: min(w, 30), sep: " = "}
}

// envFields are the configured variables by name, secret values hidden
// unless revealed (masking first, then the layout makes what is shown
// terminal-safe), and the line saying where they come from.
func envFields(env *ui.Env, spec config.Spec, reveal bool, source string) []field {
	s := env.Styles
	if len(spec.Env) == 0 {
		return []field{
			{value: "No variables set for this server", style: s.Muted, info: true},
			{value: source, style: s.Muted, info: true, gap: true},
		}
	}
	keys := make([]string, 0, len(spec.Env))
	for k := range spec.Env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	fields := []field{{value: "From the config; the process also gets the login shell's environment.", style: s.Muted, info: true}}
	hidden := false
	for i, k := range keys {
		raw := spec.Env[k]
		f := field{label: k, value: raw, style: s.Text, copy: raw, gap: i == 0}
		if isSecretEnv(k, raw) {
			f.secret = true
			if !reveal {
				f.value, f.style, hidden = secretMask, s.Muted, true
			}
		}
		if f.copy == "" {
			// An empty value is still a value to show.
			f.value = `""`
		}
		fields = append(fields, f)
	}
	tail := source
	if hidden {
		tail = strings.TrimPrefix(source+env.Glyphs.Separator+"secrets hidden", env.Glyphs.Separator)
	}
	return append(fields, field{value: tail, style: s.Muted, info: true, gap: true})
}
