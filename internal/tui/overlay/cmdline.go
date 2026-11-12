package overlay

import (
	"strconv"
	"strings"
	"unicode"

	"github.com/charmbracelet/x/ansi"

	"github.com/padmk/portside/internal/tui/keymap"
	"github.com/padmk/portside/internal/tui/theme"
)

// Completer returns the completions of the word at a rune index of a line.
type Completer func(line string, cursor int) keymap.Completion

// CmdLine is the command line (`:`): the text, its cursor, the history and
// the completions of the word under the cursor, shown while typing and
// cycled with tab.
type CmdLine struct {
	text   []rune
	cursor int
	// history holds earlier commands, oldest first; at is the entry shown,
	// len(history) for the draft, which draft keeps while in history.
	history []string
	at      int
	draft   string

	complete Completer
	// comp holds the completions of the word under the cursor; chosen is
	// the one tab put in the line, -1 before the first tab. While cycling,
	// base is the line before the first tab, which every choice replaces a
	// span of.
	comp   keymap.Completion
	chosen int
	base   []rune
}

// NewCmdLine returns an empty command line over history, oldest first,
// with its completions from complete.
func NewCmdLine(history []string, complete Completer) *CmdLine {
	c := &CmdLine{history: history, at: len(history), complete: complete}
	c.edited()
	return c
}

// Value is the text without the colon.
func (c *CmdLine) Value() string { return string(c.text) }

// Cursor is the cursor's rune index into Value.
func (c *CmdLine) Cursor() int { return c.cursor }

// Rows is the height View takes: 2 while there are completions to show.
func (c *CmdLine) Rows() int {
	if len(c.comp.Items) > 0 {
		return 2
	}
	return 1
}

func (c *CmdLine) set(s string) {
	c.text = []rune(s)
	c.cursor = len(c.text)
}

// edited ends a completion cycle and finds the completions at the cursor.
func (c *CmdLine) edited() {
	c.chosen, c.base = -1, nil
	if c.complete != nil {
		c.comp = c.complete(string(c.text), c.cursor)
	}
}

// Insert types text at the cursor.
func (c *CmdLine) Insert(text string) {
	r := []rune(text)
	c.text = append(c.text[:c.cursor], append(r, c.text[c.cursor:]...)...)
	c.cursor += len(r)
	c.edited()
}

// Backspace deletes the character before the cursor.
func (c *CmdLine) Backspace() {
	if c.cursor > 0 {
		c.text = append(c.text[:c.cursor-1], c.text[c.cursor:]...)
		c.cursor--
	}
	c.edited()
}

// DeleteWord deletes the word before the cursor and the spaces after it.
func (c *CmdLine) DeleteWord() {
	i := c.cursor
	for i > 0 && unicode.IsSpace(c.text[i-1]) {
		i--
	}
	for i > 0 && !unicode.IsSpace(c.text[i-1]) {
		i--
	}
	c.text = append(c.text[:i], c.text[c.cursor:]...)
	c.cursor = i
	c.edited()
}

// Left, Right, Home and End move the cursor.
func (c *CmdLine) Left()  { c.cursor = max(0, c.cursor-1); c.edited() }
func (c *CmdLine) Right() { c.cursor = min(len(c.text), c.cursor+1); c.edited() }
func (c *CmdLine) Home()  { c.cursor = 0; c.edited() }
func (c *CmdLine) End()   { c.cursor = len(c.text); c.edited() }

// Prev shows the previous history entry, keeping the draft when leaving it.
func (c *CmdLine) Prev() {
	if c.at > 0 {
		if c.at == len(c.history) {
			c.draft = string(c.text)
		}
		c.at--
		c.set(c.history[c.at])
	}
	c.edited()
}

// Next shows the next history entry, and the draft after the newest.
func (c *CmdLine) Next() {
	if c.at < len(c.history) {
		c.at++
		if c.at == len(c.history) {
			c.set(c.draft)
		} else {
			c.set(c.history[c.at])
		}
	}
	c.edited()
}

// Complete puts the next completion (the previous one with back) in place
// of the word under the cursor, keeping the text after it; a command name
// gets a space after it, for its argument.
func (c *CmdLine) Complete(back bool) {
	n := len(c.comp.Items)
	if n == 0 {
		return
	}
	switch {
	case c.chosen < 0:
		c.base = append([]rune{}, c.text...)
		c.chosen = 0
		if back {
			c.chosen = n - 1
		}
	case back:
		c.chosen = (c.chosen + n - 1) % n
	default:
		c.chosen = (c.chosen + 1) % n
	}
	item := []rune(c.comp.Items[c.chosen])
	if c.comp.Word == 0 && (c.comp.End >= len(c.base) || !unicode.IsSpace(c.base[c.comp.End])) {
		item = append(item, ' ')
	}
	head := c.base[:c.comp.Start]
	tail := c.base[c.comp.End:]
	c.text = append(append(append([]rune{}, head...), item...), tail...)
	c.cursor = len(head) + len(item)
	if n == 1 {
		// The only choice is in place: offer what follows it.
		c.edited()
	}
}

// View renders Rows() rows, each exactly w cells wide: the completions
// (the chosen one highlighted, scrolled to keep it in view) above ":" and
// the text with a block cursor, scrolled by cells so the cursor stays in
// view with wide characters too.
func (c *CmdLine) View(s theme.Styles, w int) string {
	if w <= 0 {
		return ""
	}
	room := max(w-2, 1)
	from := 0
	for from < c.cursor && ansi.StringWidth(string(c.text[from:c.cursor])) >= room {
		from++
	}
	text := c.text[from:]
	at := c.cursor - from
	under := " "
	after := ""
	if at < len(text) {
		under = string(text[at])
		after = string(text[at+1:])
	}
	cursor := s.Text.Reverse(true).Render(under)
	line := s.Row(s.Accent.Render(":")+s.Text.Render(string(text[:at]))+cursor+s.Text.Render(after), w)
	if len(c.comp.Items) == 0 {
		return line
	}
	return c.candidates(s, w) + "\n" + line
}

// candidates is the row of completions, from the chosen one's
// neighborhood, as many as fit.
func (c *CmdLine) candidates(s theme.Styles, w int) string {
	items := c.comp.Items
	first := max(0, c.chosen-2)
	var parts []string
	used := 0
	for i := first; i < len(items); i++ {
		st := s.Text2
		if i == c.chosen {
			st = s.OverlaySelected
		}
		part := st.Render(" " + items[i] + " ")
		pw := ansi.StringWidth(part) + 1
		if used+pw > w && len(parts) > 0 {
			parts = append(parts, s.Muted.Render("+"+strconv.Itoa(len(items)-i)))
			break
		}
		parts = append(parts, part)
		used += pw
	}
	return s.Row(strings.Join(parts, s.Surface.Render(" ")), w)
}
