package keymap

import (
	"slices"
	"strings"
	"testing"
)

// shadowed lists the keys a pane scope deliberately takes from the global
// scope, by scope; none today.
var shadowed = map[Scope][]string{}

func TestNoKeyRunsTwoActionsInOneScope(t *testing.T) {
	r := Default()
	global := map[string]string{}
	for _, a := range r.InScope(ScopeGlobal) {
		for _, k := range a.Keys {
			if other, ok := global[k]; ok {
				t.Errorf("global key %q runs %s and %s", k, other, a.ID)
			}
			global[k] = a.ID
		}
	}
	for _, sc := range Scopes() {
		if sc == ScopeGlobal {
			continue
		}
		seen := map[string]string{}
		for _, a := range r.InScope(sc) {
			for _, k := range a.Keys {
				if other, ok := seen[k]; ok {
					t.Errorf("%s: key %q runs %s and %s", sc, k, other, a.ID)
				}
				seen[k] = a.ID
				if g, ok := global[k]; ok && !slices.Contains(shadowed[sc], k) {
					t.Errorf("%s: key %q of %s hides the global %s", sc, k, a.ID, g)
				}
			}
		}
	}
}

func TestShownKeysRunTheShownAction(t *testing.T) {
	r := Default()
	for _, a := range r.All() {
		if !a.Footer && !a.Menu {
			continue
		}
		for _, k := range a.Keys {
			got, ok := r.Lookup(k, a.Scope, ScopeGlobal)
			if !ok || got.ID != a.ID || got.Scope != a.Scope {
				t.Errorf("%s %s shows key %q, which runs %s/%s", a.Scope, a.ID, k, got.Scope, got.ID)
			}
		}
		// Every shown action is also a command that resolves to it.
		got, err := r.Command(a.ID)
		if err != nil || got.ID != a.ID {
			t.Errorf(":%s resolves to %q, %v", a.ID, got.ID, err)
		}
	}
}

func TestEveryActionHasHelpAndAShortWord(t *testing.T) {
	for _, a := range Default().All() {
		if a.Help == "" || a.Short == "" || a.Group == "" {
			t.Errorf("%s/%s lacks help, a short word or a group: %+v", a.Scope, a.ID, a)
		}
		if a.Destructive && len(a.Keys) > 0 && a.Keys[0] == "space" {
			t.Errorf("%s: a destructive action on space", a.ID)
		}
	}
}

func TestCommandNames(t *testing.T) {
	r := Default()
	for _, c := range []struct{ in, id, err string }{
		{"restart", "restart", ""},
		{"rest", "restart", ""}, // restart begins restart-group
		{"filter-s", "filter-state", ""},
		{"t", "", "ambiguous"},
		{"restrat", "", `did you mean "restart"`},
		{"zzzzzzzz", "", "unknown command"},
	} {
		a, err := r.Command(c.in)
		switch {
		case c.err != "" && (err == nil || !strings.Contains(err.Error(), c.err)):
			t.Errorf("%q: error %v, want %q", c.in, err, c.err)
		case c.err == "" && (err != nil || a.ID != c.id):
			t.Errorf("%q: %q, %v; want %q", c.in, a.ID, err, c.id)
		}
	}
}

func TestParseCommandAndArguments(t *testing.T) {
	r := Default()
	for _, c := range []struct {
		line string
		id   string
		args []string
		err  string
	}{
		{":restart web", "restart", []string{"web"}, ""},
		{`  restart "my app"`, "restart", []string{"my app"}, ""},
		{"q", "quit", nil, ""},
		{`restart "web`, "", nil, "unterminated"},
		{":", "", nil, "empty"},
	} {
		id, args, err := ParseCommand(c.line)
		if c.err != "" {
			if err == nil || !strings.Contains(err.Error(), c.err) {
				t.Errorf("%q: error %v, want %q", c.line, err, c.err)
			}
			continue
		}
		if err != nil || id != c.id || !slices.Equal(args, c.args) {
			t.Errorf("%q: %q %q %v", c.line, id, args, err)
		}
	}
	check := func(id string, args ...string) error {
		a, _ := r.ByID(id)
		return a.CheckArgs(args)
	}
	if err := check("messages", "x"); err == nil || !strings.Contains(err.Error(), "no argument") {
		t.Errorf("messages x: %v", err)
	}
	if err := check("restart", "web", "api"); err == nil || !strings.Contains(err.Error(), "one name") {
		t.Errorf("restart web api: %v", err)
	}
	if err := check("filter-state"); err == nil || !strings.Contains(err.Error(), "needs a state") {
		t.Errorf("filter-state without a state: %v", err)
	}
	if err := check("clear-logs", "web"); err != nil {
		t.Errorf("clear-logs web: %v", err)
	}
}

func TestCompletion(t *testing.T) {
	r := Default()
	n := Names{Servers: []string{"web", "api", "my app"}, Groups: []string{"shop"}, States: []string{"running", "crashed"}, Themes: []string{"nord", "nord-light"}}
	for _, c := range []struct {
		line   string
		cursor int // -1 is the end
		start  int
		end    int
		items  []string
	}{
		{"res", -1, 0, 3, []string{"restart", "restart-daemon", "restart-group"}},
		{"restart ", -1, 8, 8, []string{"web", "api", `"my app"`, "shop"}},
		{"restart w", -1, 8, 9, []string{"web"}},
		{"restart a", -1, 8, 9, []string{"api", `"my app"`}}, // prefix first, then contains
		{`restart "my`, -1, 8, 11, []string{`"my app"`}},
		// The cursor in the middle completes the word it is in, up to it.
		{"rest web", 3, 0, 4, []string{"restart", "restart-daemon", "restart-group"}},
		{"filter-state c", -1, 13, 14, []string{"crashed"}},
		{"filter-state ", -1, 13, 13, []string{"running", "crashed", "all"}},
		{"theme nord-", -1, 6, 11, []string{"nord-light"}},
		{"messages ", -1, 9, 9, nil},
		{"restart web ", -1, 12, 12, nil},
	} {
		cur := c.cursor
		if cur < 0 {
			cur = len([]rune(c.line))
		}
		got := r.Complete(c.line, cur, n)
		if got.Start != c.start || got.End != c.end || !slices.Equal(got.Items, c.items) {
			t.Errorf("%q at %d: [%d,%d) %q; want [%d,%d) %q", c.line, cur, got.Start, got.End, got.Items, c.start, c.end, c.items)
		}
	}
}

func TestFuzzyPrefersWordStarts(t *testing.T) {
	a, okA := Fuzzy("rw", "Restart web")
	b, okB := Fuzzy("rw", "Clear the daemon's scrollback: worker")
	if !okA || !okB || a <= b {
		t.Errorf("rw: %d (%v) for Restart web, %d (%v) for the scrollback", a, okA, b, okB)
	}
	if _, ok := Fuzzy("restart web", "Restart: api"); ok {
		t.Error("restart web matches Restart: api")
	}
	if _, ok := Fuzzy("theme nord", "Theme Nord dark"); !ok {
		t.Error("theme nord does not match Theme Nord dark")
	}
}
