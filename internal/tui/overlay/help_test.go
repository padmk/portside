package overlay

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/padmk/portside/internal/tui/keymap"
	"github.com/padmk/portside/internal/tui/theme"
)

func TestHelpListsEachScopeWithItsMeaning(t *testing.T) {
	secs := sections(keymap.Default(), []keymap.Scope{keymap.ScopeServers, keymap.ScopeGlobal, keymap.ScopeLogs, keymap.ScopeDetail}, false)
	if secs[0].title != "Servers (this pane)" || secs[1].title != "Lists and the main panel" {
		t.Fatalf("first sections %q, %q", secs[0].title, secs[1].title)
	}
	where := map[string][]string{}
	for _, s := range secs {
		for _, e := range s.entries {
			where[e.keys] = append(where[e.keys], s.title)
		}
	}
	// a and c mean the same thing on a different server in two scopes,
	// / something else: each is listed in both; y copies something
	// different in all three.
	for _, k := range []string{"a", "c", "/"} {
		if len(where[k]) != 2 {
			t.Errorf("key %s is listed in %v, want the servers and the logs", k, where[k])
		}
	}
	if len(where["y"]) != 3 {
		t.Errorf("key y is listed in %v, want the servers, the logs and the detail tabs", where["y"])
	}
	if n := len(where["j ↓"]); n != 1 {
		t.Errorf("the shared navigation keys are listed %d times", n)
	}
}

// TestHelpWrapsLongDescriptions checks that two-column help shows every
// description whole, wrapped rather than cut.
func TestHelpWrapsLongDescriptions(t *testing.T) {
	r := keymap.Default()
	view := ansi.Strip((&Help{}).View(theme.New(theme.TerminalFamily.Dark), theme.Unicode, r, keymap.Scopes(), false, 100, 400))
	// Read each column top to bottom: the box's border and padding take
	// two cells, the columns (96-3)/2 cells with three between them.
	var left, right []string
	for _, l := range strings.Split(view, "\n") {
		left = append(left, ansi.Cut(l, 2, 2+46))
		right = append(right, ansi.Cut(l, 2+46+3, 2+96))
	}
	text := strings.Join(strings.Fields(strings.Join(append(left, right...), " ")), " ")
	for _, a := range r.All() {
		if a.Group == "Navigation" {
			continue
		}
		if !strings.Contains(text, strings.Join(strings.Fields(a.Help), " ")) {
			t.Errorf("the help cuts %q", a.Help)
		}
	}
}
