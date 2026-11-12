package form

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/padmk/portside/internal/tui/theme"
	"github.com/padmk/portside/internal/tui/ui"
)

func testEnv() *ui.Env {
	return &ui.Env{Styles: theme.New(theme.TerminalFamily.Variant(true)), Glyphs: theme.Unicode}
}

func testForm() *Model {
	return New("Edit web", []Field{
		{Key: "name", Label: "Name", Value: "web", ReadOnly: "rename it in :config"},
		{Key: "dir", Aliases: []string{"directory"}, Label: "Directory", Value: "web"},
		{Key: "port", Label: "Port", Kind: Number, Placeholder: "automatic"},
		{Key: "env", Label: "Environment", Kind: List, Lines: true, Value: "A=1\nTOKEN=abc",
			Secret: func(l string) bool { return strings.HasPrefix(l, "TOKEN=") }},
		{Key: "enabled", Label: "Enabled", Kind: Toggle, On: true},
		{Key: "restart", Label: "Restart", Kind: Choice, Choices: []string{"", "no", "on-failure", "always"}},
		{Key: "ready_after", Label: "Ready after", Section: "Advanced"},
		{Key: "stop_timeout", Label: "Stop timeout"},
	})
}

// press sends keys to the form; each word is one key ("space", "ctrl+s"),
// or text typed one rune at a time when quoted with a leading '.
func press(env *ui.Env, m *Model, keys ...string) (Outcome, tea.Cmd) {
	var out Outcome
	var cmd tea.Cmd
	for _, k := range keys {
		if text, ok := strings.CutPrefix(k, "'"); ok {
			for _, r := range text {
				out, cmd = m.Key(env, tea.KeyPressMsg{Code: r, Text: string(r)})
				env.TakeMode()
			}
			continue
		}
		out, cmd = m.Key(env, keyMsg(k))
		if mode, ok := env.TakeMode(); ok && mode == ui.ModeInsert != m.Editing() {
			panic("mode request out of step with the form: " + k)
		}
	}
	return out, cmd
}

func keyMsg(k string) tea.KeyPressMsg {
	switch k {
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	case "tab":
		return tea.KeyPressMsg{Code: tea.KeyTab}
	case "space":
		return tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}
	case "backspace":
		return tea.KeyPressMsg{Code: tea.KeyBackspace}
	case "down":
		return tea.KeyPressMsg{Code: tea.KeyDown}
	case "ctrl+u":
		return tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl}
	case "ctrl+s":
		return tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl}
	}
	r := []rune(k)[0]
	return tea.KeyPressMsg{Code: r, Text: k}
}

func TestMovesStayInsideTheForm(t *testing.T) {
	env, m := testEnv(), testForm()
	press(env, m, "k")
	if m.Cursor() != 0 {
		t.Errorf("k at the top moved to %d", m.Cursor())
	}
	press(env, m, "j", "tab", "down")
	if m.Cursor() != 3 {
		t.Errorf("three moves down: cursor %d, want 3", m.Cursor())
	}
	press(env, m, "G", "j")
	if m.Cursor() != len(m.Fields)-1 {
		t.Errorf("G then j: cursor %d, want the last field", m.Cursor())
	}
}

func TestInsertModeEditsAndKeepsTheValue(t *testing.T) {
	env, m := testEnv(), testForm()
	press(env, m, "j", "i")
	if !m.Editing() {
		t.Fatal("i did not enter insert mode")
	}
	// In insert mode j and q are text, not moves or a close.
	if out, _ := press(env, m, "backspace", "backspace", "backspace", "'jq/app"); out != Stay || !m.Editing() {
		t.Fatalf("typing left insert mode (outcome %v)", out)
	}
	press(env, m, "enter")
	if m.Editing() || m.Value("dir") != "jq/app" {
		t.Errorf("after enter: editing %v, dir %q; want jq/app kept", m.Editing(), m.Value("dir"))
	}
	// A number takes digits only.
	press(env, m, "j", "enter", "'80a8x0", "esc")
	if got := m.Value("port"); got != "8080" {
		t.Errorf("port = %q, want 8080", got)
	}
	// enter starts a new line in a list taken one item per line.
	press(env, m, "j", "enter", "enter", "'B=2", "esc")
	if got := m.Field("env").Items(); len(got) != 3 || got[2] != "B=2" {
		t.Errorf("env items = %q, want a third B=2", got)
	}
}

func TestToggleChoiceAndReadOnly(t *testing.T) {
	env, m := testEnv(), testForm()
	_, cmd := press(env, m, "enter")
	if m.Editing() || cmd == nil {
		t.Fatal("enter on a read-only field edited it or said nothing")
	}
	if n, ok := cmd().(ui.NoticeMsg); !ok || !strings.Contains(n.Text, ":config") {
		t.Errorf("read-only notice = %#v", cmd())
	}
	press(env, m, "j", "j", "j", "j", "space")
	if m.On("enabled") {
		t.Error("space did not switch the toggle off")
	}
	press(env, m, "j", "space", "space")
	if got := m.Value("restart"); got != "on-failure" {
		t.Errorf("two spaces on the choice: %q, want on-failure", got)
	}
}

func TestDirtyAndDiscardConfirmation(t *testing.T) {
	env, m := testEnv(), testForm()
	if out, cmd := press(env, m, "esc"); out != Close || cmd != nil {
		t.Errorf("esc on an unchanged form: %v, want Close at once", out)
	}
	press(env, m, "j", "j", "j", "j", "space")
	if !m.Dirty() {
		t.Fatal("a switched toggle is not a change")
	}
	out, cmd := press(env, m, "esc")
	if out != Stay || cmd == nil {
		t.Fatalf("esc on a changed form: %v, want a confirmation", out)
	}
	c, ok := cmd().(ui.ConfirmMsg)
	if !ok || c.Yes == nil {
		t.Fatalf("esc asked with %#v", cmd())
	}
	if d, ok := c.Yes().(DiscardMsg); !ok || d.Form != m {
		t.Errorf("confirming sends %#v, want DiscardMsg for this form", c.Yes())
	}
	// Switching back is no change.
	press(env, m, "space")
	if m.Dirty() {
		t.Error("a toggle switched back still counts as a change")
	}
	if out, _ := press(env, m, "ctrl+s"); out != Save {
		t.Errorf("ctrl+s: %v, want Save", out)
	}
}

func TestDaemonErrorsLandUnderTheirField(t *testing.T) {
	for _, c := range []struct{ msg, key, text string }{
		{"/x/portside.toml: [servers.web]: port 70000 out of range", "port", "port 70000 out of range"},
		{"/x/portside.toml: [servers.web]: ready_after must not be negative", "ready_after", "ready_after must not be negative"},
		{"directory ~/gone does not exist", "dir", "directory ~/gone does not exist"},
		{"/x/portside.toml: [servers.web]: stop_timeout: invalid duration", "stop_timeout", "stop_timeout: invalid duration"},
		{`server "web" already exists`, "", ""},
	} {
		m := testForm()
		m.SetError(c.msg)
		if c.key == "" {
			if m.Err != c.msg {
				t.Errorf("%q: form error %q, want the message", c.msg, m.Err)
			}
			continue
		}
		f := m.Field(c.key)
		if f.Err != c.text || m.Fields[m.Cursor()].Key != c.key {
			t.Errorf("%q: %s has %q and the cursor is on %s", c.msg, c.key, f.Err, m.Fields[m.Cursor()].Key)
		}
	}
}

func TestViewMasksSecretsAndScrolls(t *testing.T) {
	env, m := testEnv(), testForm()
	view := ansi.Strip(m.View(env, 80, 24))
	if strings.Contains(view, "abc") || !strings.Contains(view, "TOKEN=••••••") {
		t.Errorf("the secret shows before editing:\n%s", view)
	}
	press(env, m, "j", "j", "j", "enter")
	if view := ansi.Strip(m.View(env, 80, 24)); !strings.Contains(view, "TOKEN=abc") {
		t.Errorf("editing does not show the secret:\n%s", view)
	}
	press(env, m, "esc", "G")
	view = m.View(env, 80, 10)
	if h := strings.Count(view, "\n") + 1; h > 10 {
		t.Errorf("the form is %d rows on a screen of 10", h)
	}
	if !strings.Contains(ansi.Strip(view), "Stop timeout") {
		t.Errorf("the cursor's field scrolled out:\n%s", ansi.Strip(view))
	}
	for _, l := range strings.Split(view, "\n") {
		if w := ansi.StringWidth(l); w > 80 {
			t.Errorf("row %d cells wide on 80: %q", w, ansi.Strip(l))
		}
	}
	// A click on a row selects its field.
	for y, f := range m.rowField {
		if f == len(m.Fields)-2 {
			if !m.Click(y) || m.Cursor() != f {
				t.Errorf("click on row %d did not select field %d", y, f)
			}
			break
		}
	}
}

func TestPasteKeepsTheTextExactly(t *testing.T) {
	e, m := testEnv(), testForm()
	press(e, m, "j", "enter", "ctrl+u")
	m.Paste("printf 'a  b'\t|  cat\r\n")
	press(e, m, "esc")
	if got := m.Value("dir"); got != "printf 'a  b'\t|  cat" {
		t.Errorf("one line took %q", got)
	}
	press(e, m, "enter", "ctrl+u")
	m.Paste("a\r\nb\n\nc")
	press(e, m, "esc")
	if got := m.Value("dir"); got != "a b  c" {
		t.Errorf("line breaks became %q, want one space each", got)
	}
	press(e, m, "j", "j", "enter")
	m.Paste("\nB= two \r\n")
	press(e, m, "esc")
	if got := m.Value("env"); got != "A=1\nTOKEN=abc\nB= two \n" {
		t.Errorf("a list of lines took %q", got)
	}
}

func TestSpaceOnlySwitches(t *testing.T) {
	env, m := testEnv(), testForm()
	press(env, m, "j", "space")
	if m.Editing() {
		t.Error("space on a text field started editing")
	}
	press(env, m, "j", "j", "j", "space")
	if m.On("enabled") {
		t.Error("space did not switch the toggle")
	}
}

func TestInsertModeDrawsSafeTextInCells(t *testing.T) {
	env := testEnv()
	m := New("t", []Field{{Key: "a", Label: "A", Value: "x\x1b[31mred\x07"}, {Key: "b", Label: "B", Value: strings.Repeat("界", 40)}})
	press(env, m, "enter")
	view := m.View(env, 80, 24)
	if strings.Contains(view, "\x1b[31m") || strings.Contains(view, "\x07") || !strings.Contains(ansi.Strip(view), "x�[31mred�") {
		t.Errorf("a control character reached the screen: %q", view)
	}
	press(env, m, "esc")
	if m.Value("a") != "x\x1b[31mred\x07" {
		t.Errorf("editing changed the stored text to %q", m.Value("a"))
	}
	// At a narrow width the caret at the end of a wide value stays in view
	// and no row is wider than the screen.
	press(env, m, "j", "enter")
	view = m.View(env, 30, 24)
	for _, l := range strings.Split(view, "\n") {
		if w := ansi.StringWidth(l); w > 30 {
			t.Errorf("row %d cells wide on 30: %q", w, ansi.Strip(l))
		}
	}
	line, _ := m.in.lines(lipgloss.NewStyle(), lipgloss.NewStyle().Reverse(true), 9)
	if w := ansi.StringWidth(line[0]); w > 9 || !strings.HasSuffix(line[0], lipgloss.NewStyle().Reverse(true).Render(" ")) {
		t.Errorf("caret line %q (%d cells), want the caret last within 9 cells", line[0], w)
	}
}

func TestTallFieldKeepsTheEditingLineInView(t *testing.T) {
	env := testEnv()
	var lines []string
	for i := range 40 {
		lines = append(lines, fmt.Sprintf("V%02d=x", i))
	}
	m := New("t", []Field{{Key: "env", Label: "Env", Kind: List, Lines: true, Value: strings.Join(lines, "\n")}})
	press(env, m, "enter")
	if view := ansi.Strip(m.View(env, 80, 16)); !strings.Contains(view, "V39=x") {
		t.Errorf("the caret's last line is off screen:\n%s", view)
	}
	for range 39 {
		m.Key(env, tea.KeyPressMsg{Code: tea.KeyUp})
	}
	if view := ansi.Strip(m.View(env, 80, 16)); !strings.Contains(view, "V00=x") || strings.Contains(view, "V39=x") {
		t.Errorf("the caret's first line is off screen:\n%s", view)
	}
}

func TestMoreFoldsAdvancedFields(t *testing.T) {
	env := testEnv()
	m := New("t", []Field{{Key: "a", Label: "A"}, {Key: "b", Label: "B", Advanced: true, Value: "set"}, {Key: "c", Label: "C", Advanced: true}})
	press(env, m, "G")
	view := ansi.Strip(m.View(env, 80, 24))
	if !strings.Contains(view, "2 more settings, 1 set") || strings.Contains(view, "\n│   B ") {
		t.Errorf("collapsed:\n%s", view)
	}
	press(env, m, "enter")
	if f := m.Fields[m.Cursor()]; f.Key != "b" {
		t.Errorf("opening More put the cursor on %q", f.Key)
	}
	press(env, m, "G")
	if m.Fields[m.Cursor()].Key != "c" || strings.Contains(ansi.Strip(m.View(env, 80, 24)), "more settings") {
		t.Error("the advanced fields do not show after More opened")
	}
	// An error under a folded field opens the fold.
	m2 := New("t", []Field{{Key: "a", Label: "A"}, {Key: "c", Label: "C", Advanced: true}})
	m2.SetError("c is wrong")
	if m2.Fields[m2.Cursor()].Key != "c" || !m2.shown(m2.Cursor()) {
		t.Error("an error under a folded field stays hidden")
	}
}
