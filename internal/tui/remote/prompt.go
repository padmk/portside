package remote

import (
	"strings"

	tea "charm.land/bubbletea/v2"
)

// prompt is a one-line input whose text is drawn masked: a pairing code is
// typed here, never on the command line, so it stays out of the command
// history and off the screen.
type prompt struct {
	label string
	text  []rune
}

// key edits the text; done is set by enter (submit) and esc (cancel).
func (p *prompt) key(msg tea.KeyPressMsg) (done, submit bool) {
	switch msg.String() {
	case "enter":
		return true, strings.TrimSpace(string(p.text)) != ""
	case "esc":
		return true, false
	case "backspace":
		if n := len(p.text); n > 0 {
			p.text = p.text[:n-1]
		}
	case "ctrl+u", "ctrl+w":
		p.text = nil
	case "space":
	default:
		k := msg.Key()
		if k.Text != "" && k.Mod&(tea.ModCtrl|tea.ModAlt|tea.ModSuper) == 0 {
			p.insert(k.Text)
		}
	}
	return false, false
}

// insert adds typed or pasted text without spaces or control characters
// (a code is copied with them sometimes).
func (p *prompt) insert(s string) {
	for _, r := range s {
		if r > ' ' && r != 0x7f {
			p.text = append(p.text, r)
		}
	}
}

func (p *prompt) value() string { return strings.TrimSpace(string(p.text)) }

// masked is one bullet per character typed.
func (p *prompt) masked(ascii bool) string {
	dot := "•"
	if ascii {
		dot = "*"
	}
	return strings.Repeat(dot, len(p.text))
}
