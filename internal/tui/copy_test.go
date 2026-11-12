package tui

import (
	"reflect"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/padmk/portside/internal/tui/keymap"
	"github.com/padmk/portside/internal/tui/ui"
)

// copied runs cmd and the commands of its batches and returns what they
// put on the clipboard and the notices they show.
func copied(t *testing.T, cmd tea.Cmd) (clip []string, notices []string) {
	t.Helper()
	var walk func(tea.Cmd)
	walk = func(c tea.Cmd) {
		if c == nil {
			return
		}
		done := make(chan tea.Msg, 1)
		go func() { done <- c() }()
		var msg tea.Msg
		select {
		case msg = <-done:
		case <-time.After(time.Second):
			return
		}
		switch msg := msg.(type) {
		case tea.BatchMsg:
			for _, c := range msg {
				walk(c)
			}
		case ui.NoticeMsg:
			notices = append(notices, msg.Text)
		default:
			if v := reflect.ValueOf(msg); v.IsValid() && v.Kind() == reflect.String && v.Type().Name() == "setClipboardMsg" {
				clip = append(clip, v.String())
			}
		}
	}
	walk(cmd)
	return clip, notices
}

// On the Logs tab, :copy copies the line at the bottom and :copy-dir the
// shown server's directory, as they do on the other tabs.
func TestCopyCommandsWorkOnTheLogsTab(t *testing.T) {
	h := snapshotModel(t)
	m := h.m
	st := snapshotState()
	for i := range st.Servers {
		st.Servers[i].Dir = "/x/" + st.Servers[i].Name
	}
	h.send(ui.StateMsg{State: st})
	dir := "/x/" + m.env.Selected.Name
	m.focusPane(len(m.left))
	if m.focused().Scope() != keymap.ScopeLogs {
		t.Fatalf("the main panel shows %s, want the logs", m.focused().Scope())
	}
	run := func(id string) ([]string, []string) {
		a, err := m.env.Keys.Command(id)
		if err != nil {
			t.Fatal(err)
		}
		return copied(t, m.command(a, ""))
	}
	clip, notices := run("copy-dir")
	if len(clip) != 1 || clip[0] != dir {
		t.Errorf(":copy-dir put %q on the clipboard (notices %q)", clip, notices)
	}
	clip, notices = run("copy")
	if len(clip) != 1 || !strings.Contains(clip[0], "combining") {
		t.Errorf(":copy put %q on the clipboard, want the bottom line (notices %q)", clip, notices)
	}
	// From the servers list too, the directory is the shown server's.
	h.run(m.focusScope(keymap.ScopeServers))
	if clip, notices = run("copy-dir"); len(clip) != 1 || clip[0] != dir {
		t.Errorf(":copy-dir from the list put %q on the clipboard (notices %q)", clip, notices)
	}
}
