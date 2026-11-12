// Package form is the terminal UI's form: one field per row with the label
// left and the value right, as in the dashboard's Settings (design/tui.md
// "Forms and dialogs"). It knows nothing about what it edits; the caller
// builds the fields, reads the values on save and passes the daemon's
// errors back, which land under the field they name.
package form

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/padmk/portside/internal/tui/ui"
)

// Kind is what a field holds and how it is edited.
type Kind int

const (
	// Text is one line typed in insert mode.
	Text Kind = iota
	// Number is a Text that takes digits only.
	Number
	// Toggle is on or off; space or enter switches it.
	Toggle
	// Choice is one of Choices; space or enter moves to the next.
	Choice
	// List is several items: one per line when Lines is set, otherwise
	// separated by commas.
	List
	// fold is the "More" row New puts before the first Advanced field;
	// enter shows the advanced fields in its place.
	fold
)

// Field is one row of a form.
type Field struct {
	// Key is the config key the daemon's errors name ("stop_timeout");
	// Aliases are other words its errors use for it ("directory").
	Key     string
	Aliases []string
	Label   string
	// Help is the line shown under the field while the cursor is on it.
	Help string
	// Section, when set, is a heading drawn above the field.
	Section string
	// Advanced fields stay behind a "More" row until it is opened, so the
	// form starts with the keys most edits need.
	Advanced bool
	Kind     Kind
	// Placeholder is shown, muted, for an empty value: what empty means.
	Placeholder string
	// Choices are a Choice field's values; "" shows as Placeholder.
	Choices []string
	// Lines makes a List take one item per line.
	Lines bool
	// ReadOnly, when set, says why the field cannot be edited here.
	ReadOnly string
	// Secret reports whether a line of a List is hidden until the field
	// is edited (a variable holding a token).
	Secret func(line string) bool

	// Value is the text of a Text, Number, Choice or List field (a List's
	// items separated by newlines); On is a Toggle's state.
	Value string
	On    bool
	// Err is the daemon's error about this field.
	Err string

	initial   string
	initialOn bool
	revealed  bool
}

func (f *Field) changed() bool {
	if f.Kind == Toggle {
		return f.On != f.initialOn
	}
	return f.Value != f.initial
}

// Outcome is what a key asks of the form's owner.
type Outcome int

const (
	// Stay keeps the form open.
	Stay Outcome = iota
	// Save asks the owner to save the values.
	Save
	// Close closes the form; nothing changed or the changes were dropped.
	Close
)

// DiscardMsg comes when the user confirmed dropping the changes of Form.
type DiscardMsg struct{ Form *Model }

// Model is an open form.
type Model struct {
	Title  string
	Fields []Field
	// Err is an error no field names; Busy says a request runs
	// ("saving…").
	Err  string
	Busy string

	cursor, offset int
	editing        bool
	// more is set once the advanced fields show.
	more bool
	in   input
	// rowField maps the content rows of the last View (0 is the row
	// below the top border) to fields, -1 for others.
	rowField []int
}

// New opens a form on fields; their values are the ones it starts from,
// against which Dirty compares.
func New(title string, fields []Field) *Model {
	if at := slices.IndexFunc(fields, func(f Field) bool { return f.Advanced }); at >= 0 {
		fields = slices.Insert(slices.Clone(fields), at, moreField(fields[at:]))
	}
	m := &Model{Title: title, Fields: fields}
	for i := range m.Fields {
		m.Fields[i].initial, m.Fields[i].initialOn = m.Fields[i].Value, m.Fields[i].On
	}
	return m
}

// moreField is the "More" row for the advanced fields among fields: how
// many there are, how many hold a value, and their sections and the
// labels of those with a value as its help.
func moreField(fields []Field) Field {
	var sections, set []string
	n := 0
	for _, f := range fields {
		if !f.Advanced {
			continue
		}
		n++
		if f.Section != "" && !slices.Contains(sections, f.Section) {
			sections = append(sections, f.Section)
		}
		if f.Kind != Toggle && f.Value != "" {
			set = append(set, f.Label)
		}
	}
	value, help := fmt.Sprintf("%d more settings", n), strings.Join(sections, ", ")+"."
	if len(set) > 0 {
		value += fmt.Sprintf(", %d set", len(set))
		help = "Set: " + strings.Join(set, ", ") + "."
	}
	return Field{Kind: fold, Label: "More", Value: value, Help: help}
}

// Field returns the field with the key, nil when there is none.
func (m *Model) Field(key string) *Field {
	for i := range m.Fields {
		if m.Fields[i].Key == key {
			return &m.Fields[i]
		}
	}
	return nil
}

// Suggest fills a field with a value the owner proposes, such as a free
// port; it counts as the value the form started from, so it alone does
// not make the form dirty.
func (m *Model) Suggest(key, value string) {
	if f := m.Field(key); f != nil {
		f.Value, f.initial = value, value
	}
}

// Value is the value of the field with the key ("" when there is none).
func (m *Model) Value(key string) string {
	if f := m.Field(key); f != nil {
		return f.Value
	}
	return ""
}

// On is the state of the toggle with the key.
func (m *Model) On(key string) bool {
	f := m.Field(key)
	return f != nil && f.On
}

// Dirty reports whether a value differs from the one the form opened with.
func (m *Model) Dirty() bool {
	return slices.ContainsFunc(m.Fields, func(f Field) bool { return f.changed() })
}

// Editing reports whether a field is in insert mode.
func (m *Model) Editing() bool { return m.editing }

// Cursor is the index of the field the cursor is on.
func (m *Model) Cursor() int { return m.cursor }

// ClearErrors removes the errors of the last save.
func (m *Model) ClearErrors() {
	m.Err = ""
	for i := range m.Fields {
		m.Fields[i].Err = ""
	}
}

// SetError shows an error from the daemon under the field it names, else
// above the hints; the cursor moves to that field.
func (m *Model) SetError(msg string) {
	i := m.fieldFor(msg)
	if i < 0 {
		m.Err = msg
		return
	}
	m.Fields[i].Err = errorText(msg)
	if m.Fields[i].Advanced {
		m.more = true
	}
	m.cursor = i
}

// errorText drops the config file's path and the server's table that the
// daemon puts before a message: "path: [servers.web]: port 70000 out of
// range" names the field already.
func errorText(msg string) string {
	if i := strings.LastIndex(msg, "]: "); i >= 0 {
		return msg[i+3:]
	}
	if i := strings.LastIndex(msg, ".toml: "); i >= 0 {
		return msg[i+7:]
	}
	return msg
}

// fieldFor is the field a daemon error names: the key or alias that comes
// first in the message, the longer one on a tie; -1 for none.
func (m *Model) fieldFor(msg string) int {
	text := errorText(msg)
	best, at, size := -1, len(text)+1, 0
	for i, f := range m.Fields {
		for _, word := range append([]string{f.Key}, f.Aliases...) {
			if word == "" {
				continue
			}
			loc := regexp.MustCompile(`\b` + regexp.QuoteMeta(word) + `\b`).FindStringIndex(text)
			if loc == nil {
				continue
			}
			if loc[0] < at || (loc[0] == at && len(word) > size) {
				best, at, size = i, loc[0], len(word)
			}
		}
	}
	return best
}

// Key handles a key press: in insert mode it edits the field's text,
// otherwise j/k, the arrows and tab move, enter or i edits (enter also
// switches a toggle, cycles a choice and opens "More"), space switches a
// toggle or a choice only, ctrl+s saves and esc or q close, asking first
// when something changed.
func (m *Model) Key(env *ui.Env, msg tea.KeyPressMsg) (Outcome, tea.Cmd) {
	if m.editing {
		return m.insertKey(env, msg)
	}
	f := &m.Fields[m.cursor]
	switch msg.String() {
	case "j", "down", "tab", "ctrl+n":
		m.Move(1)
	case "k", "up", "shift+tab", "ctrl+p":
		m.Move(-1)
	case "g", "home", "<":
		m.cursor = m.edge(1)
	case "G", "end", ">":
		m.cursor = m.edge(-1)
	case "ctrl+d", "pgdown":
		m.Move(5)
	case "ctrl+u", "pgup":
		m.Move(-5)
	case "enter", "i":
		return Stay, m.activate(env, f, msg.String() == "enter")
	case "space":
		if f.Kind == Toggle || f.Kind == Choice {
			return Stay, m.activate(env, f, true)
		}
	case "left", "h":
		if f.Kind == Choice && f.ReadOnly == "" {
			f.Value = cycle(f.Choices, f.Value, -1)
		}
	case "right", "l":
		if f.Kind == Choice && f.ReadOnly == "" {
			f.Value = cycle(f.Choices, f.Value, 1)
		}
	case "ctrl+s":
		if m.Busy != "" {
			return Stay, nil
		}
		return Save, nil
	case "esc", "q":
		return m.close()
	}
	return Stay, nil
}

// close closes the form, after a confirmation when something changed;
// the Cancel choice keeps it open.
func (m *Model) close() (Outcome, tea.Cmd) {
	if !m.Dirty() {
		return Close, nil
	}
	return Stay, func() tea.Msg {
		return ui.ConfirmMsg{
			Title:   "Discard changes?",
			Body:    "Your changes in " + m.Title + " have not been saved.",
			Confirm: "Discard",
			Yes:     func() tea.Msg { return DiscardMsg{Form: m} },
		}
	}
}

// activate edits, switches or cycles the field; toggle is set for keys
// that switch a toggle (enter and space, not i).
func (m *Model) activate(env *ui.Env, f *Field, toggle bool) tea.Cmd {
	if f.ReadOnly != "" {
		return func() tea.Msg { return ui.NoticeMsg{Text: f.Label + ": " + f.ReadOnly, Level: ui.LevelInfo} }
	}
	switch f.Kind {
	case fold:
		m.more = true
		m.Move(1)
	case Toggle:
		if toggle {
			f.On = !f.On
		}
	case Choice:
		if toggle {
			f.Value = cycle(f.Choices, f.Value, 1)
		}
	default:
		m.editing = true
		f.revealed = true
		m.in = newInput(f.Value)
		env.EnterMode(ui.ModeInsert)
	}
	return nil
}

// cycle is the choice d steps from v, wrapping around.
func cycle(choices []string, v string, d int) string {
	if len(choices) == 0 {
		return v
	}
	i := max(0, slices.Index(choices, v))
	return choices[(i+d+len(choices))%len(choices)]
}

// Move moves the cursor n shown fields, staying inside the form.
func (m *Model) Move(n int) {
	d := 1
	if n < 0 {
		d, n = -1, -n
	}
	for ; n > 0; n-- {
		next := m.cursor + d
		for next >= 0 && next < len(m.Fields) && !m.shown(next) {
			next += d
		}
		if next < 0 || next >= len(m.Fields) {
			return
		}
		m.cursor = next
	}
}

// shown reports whether field i is on the form: the advanced fields once
// "More" opened, the "More" row until then.
func (m *Model) shown(i int) bool {
	switch {
	case m.Fields[i].Kind == fold:
		return !m.more
	case m.Fields[i].Advanced:
		return m.more
	}
	return true
}

// edge is the first shown field (d 1) or the last (d -1).
func (m *Model) edge(d int) int {
	i := 0
	if d < 0 {
		i = len(m.Fields) - 1
	}
	for ; i >= 0 && i < len(m.Fields); i += d {
		if m.shown(i) {
			return i
		}
	}
	return m.cursor
}

// insertKey edits the text of the field in insert mode: esc and enter
// keep it (enter starts a new line in a list taken one item per line),
// tab keeps it and moves on, ctrl+s keeps it and saves.
func (m *Model) insertKey(env *ui.Env, msg tea.KeyPressMsg) (Outcome, tea.Cmd) {
	f := &m.Fields[m.cursor]
	multi := f.Kind == List && f.Lines
	switch k := msg.String(); k {
	case "esc":
		m.commit(env)
	case "enter":
		if multi {
			m.in.insert("\n")
			break
		}
		m.commit(env)
	case "tab":
		m.commit(env)
		m.Move(1)
	case "shift+tab":
		m.commit(env)
		m.Move(-1)
	case "ctrl+s":
		m.commit(env)
		if m.Busy == "" {
			return Save, nil
		}
	case "backspace", "ctrl+h":
		m.in.backspace()
	case "delete", "ctrl+d":
		m.in.delete()
	case "left", "ctrl+b":
		m.in.left()
	case "right", "ctrl+f":
		m.in.right()
	case "home", "ctrl+a":
		m.in.home()
	case "end", "ctrl+e":
		m.in.end()
	case "ctrl+w":
		m.in.deleteWord()
	case "ctrl+u":
		m.in.deleteLine()
	case "up":
		m.in.vertical(-1)
	case "down":
		m.in.vertical(1)
	case "space":
		m.typeText(f, " ")
	default:
		key := msg.Key()
		if key.Text != "" && key.Mod&(tea.ModCtrl|tea.ModAlt|tea.ModSuper) == 0 {
			m.typeText(f, key.Text)
		}
	}
	return Stay, nil
}

// Paste inserts pasted text into the field in insert mode as it is,
// line breaks as \n; a field of one line takes each line break as one
// space (a trailing one is dropped) and keeps every other character.
func (m *Model) Paste(text string) {
	if !m.editing {
		return
	}
	f := &m.Fields[m.cursor]
	text = strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n")
	if f.Kind != List || !f.Lines {
		text = strings.ReplaceAll(strings.TrimRight(text, "\n"), "\n", " ")
	}
	m.typeText(f, text)
}

// typeText inserts text; a number takes its digits only.
func (m *Model) typeText(f *Field, text string) {
	if f.Kind == Number {
		text = strings.Map(func(r rune) rune {
			if r >= '0' && r <= '9' {
				return r
			}
			return -1
		}, text)
	}
	if text != "" {
		m.in.insert(text)
	}
}

// commit keeps the edited text and leaves insert mode.
func (m *Model) commit(env *ui.Env) {
	f := &m.Fields[m.cursor]
	f.Value = m.in.value()
	f.Err = ""
	m.editing = false
	env.EnterMode(ui.ModeNormal)
}

// Click moves the cursor to the field drawn at content row y of the last
// View (0 is the row below the top border); false when no field is there.
func (m *Model) Click(y int) bool {
	if y < 0 || y >= len(m.rowField) || m.rowField[y] < 0 || m.editing {
		return false
	}
	m.cursor = m.rowField[y]
	return true
}
