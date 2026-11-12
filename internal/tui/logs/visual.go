package logs

import (
	"slices"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/padmk/portside/internal/tui/ui"
)

// startVisual selects the bottom line; j and k extend the selection.
// false when there is nothing to select.
func (p *Pane) startVisual(env *ui.Env) bool {
	if p.seqLen() == 0 {
		return false
	}
	abs := p.anchorAbs()
	p.vfollow = p.follow
	p.setFollow(false)
	p.anchor, p.rowOff = abs, 0
	p.visual, p.vstart, p.vcur = true, abs, abs
	env.EnterMode(ui.ModeSelect)
	return true
}

// visualKey moves the selection's end, copies it (y) or cancels (esc).
func (p *Pane) visualKey(env *ui.Env, k tea.KeyPressMsg) tea.Cmd {
	switch k.String() {
	case "j", "down", "shift+down":
		p.moveSelection(1)
	case "k", "up", "shift+up":
		p.moveSelection(-1)
	case "y", "enter":
		p.visual = false
		env.EnterMode(ui.ModeNormal)
		// Following again would scroll the copied lines away, so the
		// pane follows only when the selection reached the newest line.
		if p.vfollow && max(p.vstart, p.vcur) >= p.seqAt(p.seqLen()-1) {
			p.setFollow(true)
		}
		return p.copyLines(p.vstart, p.vcur)
	case "esc", "v":
		p.visual = false
		env.EnterMode(ui.ModeNormal)
		if p.vfollow {
			p.setFollow(true)
		}
	}
	return nil
}

// moveSelection moves the selection's end by delta lines and scrolls to
// keep it on screen.
func (p *Pane) moveSelection(delta int) {
	n := p.seqLen()
	if n == 0 {
		return
	}
	k := max(0, min(p.seqIndex(p.vcur)+delta, n-1))
	p.vcur = p.seqAt(k)
	switch q := p.bottomPos(); {
	case k > q.k || k == q.k && q.off > 0:
		p.setPos(pos{k, 0})
	case k < p.top || k == p.top && k < q.k:
		p.setPos(p.posWithTop(k))
	}
	p.clampAnchor()
}

func (p *Pane) selected(abs int) bool {
	return abs >= min(p.vstart, p.vcur) && abs <= max(p.vstart, p.vcur)
}

// copyLines sends the plain text of the shown lines from a to b to the
// terminal's clipboard (OSC 52), which cannot report whether it took it.
func (p *Pane) copyLines(a, b int) tea.Cmd {
	from, to := max(min(a, b), p.base), max(a, b)
	var lines []string
	for abs := from; abs <= to && abs < p.base+p.buf.Len(); abs++ {
		if p.filtering() {
			if _, ok := slices.BinarySearch(p.search.matches, abs); !ok {
				continue
			}
		}
		lines = append(lines, p.line(abs).Plain)
	}
	if len(lines) == 0 {
		return notice("nothing to copy", ui.LevelWarn)
	}
	text := "sent 1 line to the terminal's clipboard"
	if len(lines) > 1 {
		text = "sent " + strconv.Itoa(len(lines)) + " lines to the terminal's clipboard"
	}
	return tea.Batch(tea.SetClipboard(strings.Join(lines, "\n")), notice(text, ui.LevelSuccess))
}
