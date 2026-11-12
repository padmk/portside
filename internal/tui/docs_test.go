package tui

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/padmk/portside/internal/tui/keymap"
)

// docKey is how docs/tui.md writes a key: the registry's names, with the
// arrows as arrows and the page keys as on the keyboard (`shift+↓`,
// `PgDn`, `ctrl+p`, `space`).
func docKey(k string) string {
	for _, mod := range []string{"ctrl+", "shift+", "alt+"} {
		if rest, ok := strings.CutPrefix(k, mod); ok {
			return mod + docKey(rest)
		}
	}
	if s, ok := map[string]string{
		"up": "↑", "down": "↓", "left": "←", "right": "→",
		"pgup": "PgUp", "pgdown": "PgDn", "home": "Home", "end": "End",
	}[k]; ok {
		return s
	}
	return k
}

// keySections splits the guide's "Keys" section into its subsections by
// heading text.
func keySections(doc string) map[string]string {
	_, doc, _ = strings.Cut(doc, "\n## Keys\n")
	doc, _, _ = strings.Cut(doc, "\n## ")
	out := map[string]string{}
	title := ""
	var body strings.Builder
	for line := range strings.SplitSeq(doc, "\n") {
		if strings.HasPrefix(line, "#") {
			out[title] += body.String()
			body.Reset()
			title = strings.TrimSpace(strings.TrimLeft(line, "#"))
			continue
		}
		body.WriteString(line + "\n")
	}
	out[title] += body.String()
	return out
}

// TestGuideListsEveryKeyAndCommand keeps docs/tui.md in line with the
// action table: every action's keys and its command appear in the key
// table of its scope under "Keys" (the navigation keys in "Moving in
// lists and panels"), the command with its argument in brackets when the
// argument is optional and in angle brackets when it is required. The
// guide holds no em dash.
func TestGuideListsEveryKeyAndCommand(t *testing.T) {
	data, err := os.ReadFile("../../docs/tui.md")
	if err != nil {
		t.Fatal(err)
	}
	doc := string(data)
	if strings.ContainsRune(doc, '\u2014') {
		t.Error("docs/tui.md holds an em dash")
	}
	sections := keySections(doc)
	for _, a := range keymap.Default().All() {
		title := a.Scope.Title()
		if a.Group == "Navigation" {
			title = "Moving in lists and panels"
		}
		sec, ok := sections[title]
		if !ok {
			t.Fatalf("docs/tui.md has no section %q under Keys", title)
		}
		for _, k := range a.Keys {
			if !strings.Contains(sec, "`"+docKey(k)+"`") {
				t.Errorf("%s %s: key `%s` missing from %q", a.Scope, a.ID, docKey(k), title)
			}
		}
		cmd := "`:" + a.ID
		switch {
		case a.Arg == keymap.ArgNone:
			if !strings.Contains(sec, cmd+"`") {
				t.Errorf("%s %s: command %s` missing from %q", a.Scope, a.ID, cmd, title)
			}
		case a.Optional:
			if !strings.Contains(sec, cmd+" [") {
				t.Errorf("%s %s: command %s [%s]` missing from %q", a.Scope, a.ID, cmd, a.Arg.Noun(), title)
			}
		default:
			if !strings.Contains(sec, cmd+" <") {
				t.Errorf("%s %s: command %s <%s>` missing from %q", a.Scope, a.ID, cmd, a.Arg.Noun(), title)
			}
		}
	}
}

// TestDocsNameOnlyRegisteredCommands keeps stale commands out of the
// guide and the design: every `:command` they write is an action's id
// (or `:q`), apart from the abbreviations the guide shows on purpose.
func TestDocsNameOnlyRegisteredCommands(t *testing.T) {
	abbreviations := map[string]bool{"rest": true}
	reg := keymap.Default()
	command := regexp.MustCompile("`:([a-z][a-z0-9-]*)")
	for _, path := range []string{"../../docs/tui.md", "../../design/tui.md"} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range command.FindAllStringSubmatch(string(data), -1) {
			id, _, err := keymap.ParseCommand(m[1])
			if err != nil {
				t.Fatal(err)
			}
			if _, ok := reg.ByID(id); !ok && !abbreviations[m[1]] {
				t.Errorf("%s names `:%s`, which is no command", path, m[1])
			}
		}
	}
}
