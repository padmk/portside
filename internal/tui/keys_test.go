package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/padmk/portside/internal/tui/keymap"
)

// keyMsg is the key press whose String() is k, as the registry names
// keys: "j", "S", "space", "ctrl+d", "shift+down", "pgup".
func keyMsg(t *testing.T, k string) tea.KeyPressMsg {
	t.Helper()
	named := map[string]rune{
		"enter": tea.KeyEnter, "esc": tea.KeyEscape, "tab": tea.KeyTab, "space": tea.KeySpace,
		"up": tea.KeyUp, "down": tea.KeyDown, "left": tea.KeyLeft, "right": tea.KeyRight,
		"home": tea.KeyHome, "end": tea.KeyEnd, "pgup": tea.KeyPgUp, "pgdown": tea.KeyPgDown,
	}
	var msg tea.KeyPressMsg
	base, mod := k, tea.KeyMod(0)
	if rest, ok := strings.CutPrefix(k, "ctrl+"); ok {
		base, mod = rest, tea.ModCtrl
	} else if rest, ok := strings.CutPrefix(k, "shift+"); ok {
		base, mod = rest, tea.ModShift
	}
	if code, ok := named[base]; ok {
		msg = tea.KeyPressMsg{Code: code, Mod: mod}
		if k == "space" {
			msg.Text = " "
		}
	} else {
		r := []rune(base)
		msg = tea.KeyPressMsg{Code: r[0], Mod: mod}
		if mod == 0 {
			msg.Text = base
		}
	}
	if got := msg.String(); got != k {
		t.Fatalf("key %q builds a press that reads %q", k, got)
	}
	return msg
}

// TestEveryKeyWorksInItsScope presses every key of every action in a
// fresh model with the action's pane focused: none is reported as doing
// nothing and none panics. The commands it starts are not run, so no
// browser, editor or shell opens.
func TestEveryKeyWorksInItsScope(t *testing.T) {
	h := snapshotModel(t)
	base := h.m
	for _, a := range base.env.Keys.All() {
		for _, k := range a.Keys {
			h := snapshotModel(t)
			m := h.m
			if !focusOf(h, a.Scope) {
				t.Fatalf("no pane of scope %s to focus", a.Scope)
			}
			m.notice = nil
			func() {
				defer func() {
					if r := recover(); r != nil {
						t.Errorf("%s %s (%s) panics: %v", a.Scope, k, a.ID, r)
					}
				}()
				m.Update(keyMsg(t, k))
				m.screen()
			}()
			if m.notice != nil && strings.Contains(m.notice.Text, "does nothing here") {
				t.Errorf("%s %s (%s): %q", a.Scope, k, a.ID, m.notice.Text)
			}
		}
	}
}

// focusOf focuses a pane of the scope: the left column's lists, the Logs
// tab of the main panel or its Details tab.
func focusOf(h *harness, sc keymap.Scope) bool {
	m := h.m
	switch sc {
	case keymap.ScopeGlobal:
		return true
	case keymap.ScopeLogs:
		m.focusPane(len(m.left))
	case keymap.ScopeDetail:
		m.focusPane(len(m.left))
		h.key("]")
	default:
		h.run(m.focusScope(sc))
	}
	return m.focused().Scope() == sc
}

// TestEveryCommandRunsWithoutAnArgument runs every command line name with
// no argument, with the pane of its action's scope focused: each is known
// and reaches a pane or an executor, or says what argument it needs.
func TestEveryCommandRunsWithoutAnArgument(t *testing.T) {
	h := snapshotModel(t)
	for _, id := range h.m.env.Keys.Commands() {
		h := snapshotModel(t)
		m := h.m
		a, _ := m.env.Keys.ByID(id)
		if !focusOf(h, a.Scope) {
			t.Fatalf("no pane of scope %s to focus for :%s", a.Scope, id)
		}
		m.notice = nil
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf(":%s panics: %v", id, r)
				}
			}()
			m.execute(id)
			m.screen()
		}()
		if m.notice == nil {
			continue
		}
		for _, bad := range []string{"unknown command", "not available in these panes", "does nothing here"} {
			if strings.Contains(m.notice.Text, bad) {
				t.Errorf(":%s: %q", id, m.notice.Text)
			}
		}
	}
}
