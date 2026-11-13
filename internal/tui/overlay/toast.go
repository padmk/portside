package overlay

import (
	"sort"
	"strings"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/padmk/portside/internal/tui/theme"
	"github.com/padmk/portside/internal/tui/ui"
)

// Toast is a short notice at the top right; Item is what it is about, if
// anything (a click selects it).
type Toast struct {
	Title, Body string
	Level       ui.Level
	At          time.Time
	Item        ui.Selection
}

// ToastLife is how long a toast stays on screen.
const ToastLife = 6 * time.Second

// ToastStack is the rendered toasts and the rows each box takes, from the
// top of the stack, for clicks.
type ToastStack struct {
	View  string
	Boxes []ToastBox
}

// ToastBox is one toast's rows [Y0, Y1) in the stack and its item.
type ToastBox struct {
	Y0, Y1 int
	Item   ui.Selection
}

// Toasts renders the toasts younger than ToastLife as a stack of boxes,
// newest on top: at most three, each at most min(maxW, 44) cells wide with
// up to three body lines, or in compact form (a narrow terminal) only the
// newest, at most 32 cells wide with one body line. The stack is empty
// when there are none.
func Toasts(s theme.Styles, g theme.Glyphs, ts []Toast, now time.Time, maxW int, compact bool) ToastStack {
	var live []Toast
	for _, t := range ts {
		if age := now.Sub(t.At); age >= 0 && age < ToastLife {
			live = append(live, t)
		}
	}
	if len(live) == 0 {
		return ToastStack{}
	}
	sort.SliceStable(live, func(i, j int) bool { return live[i].At.After(live[j].At) })
	count, width, bodyLines := 3, 44, 3
	if compact {
		count, width, bodyLines = 1, 32, 1
	}
	live = live[:min(len(live), count)]
	// Toasts lie over the panes, not over a dimmed screen.
	pn := newPen(s, g)
	pn.border = edge(s, s.Surface)
	inner := max(min(maxW, width)-4, 4)
	mark := g.Cursor
	if g.ID == theme.ASCII.ID {
		mark = "|"
	}
	var stack ToastStack
	var boxes []string
	y := 0
	for _, t := range live {
		bar := levelStyle(pn.s, t.Level).Render(mark) + pn.space(1)
		rows := []string{bar + pn.s.Bold.Render(clean(t.Title))}
		if t.Body != "" {
			body := lipgloss.NewStyle().Width(inner - 2).Render(cleanLines(t.Body))
			for j, l := range strings.Split(body, "\n") {
				if j == bodyLines {
					break
				}
				rows = append(rows, bar+pn.s.Text2.Render(strings.TrimRight(l, " ")))
			}
		}
		box := pn.box("", rows, inner)
		boxes = append(boxes, box)
		h := len(rows) + 2
		stack.Boxes = append(stack.Boxes, ToastBox{Y0: y, Y1: y + h, Item: t.Item})
		y += h
	}
	stack.View = strings.Join(boxes, "\n")
	return stack
}

func levelStyle(s theme.Styles, l ui.Level) lipgloss.Style {
	switch l {
	case ui.LevelError:
		return s.Error
	case ui.LevelWarn:
		return s.Warn
	case ui.LevelSuccess:
		return s.Running
	}
	return s.Info
}
