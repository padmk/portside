package detail

import (
	"fmt"
	"math"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/padmk/portside/internal/supervisor"
	"github.com/padmk/portside/internal/tui/theme"
	"github.com/padmk/portside/internal/tui/ui"
	"github.com/padmk/portside/internal/viewfmt"
)

// Metrics renders CPU and memory histories in w x h: per chart a header
// with the current value and the peak, the chart in the glyph set's
// alphabet, a baseline and the span it covers.
func Metrics(env *ui.Env, v supervisor.View, interval time.Duration, w, h int) []string {
	s, g := env.Styles, env.Glyphs
	if h <= 0 {
		return nil
	}
	out := make([]string, 0, h)
	if len(v.CPUHistory) == 0 && len(v.MemHistory) == 0 {
		for _, l := range wrap(emptyMetrics(v, interval), w, g.Ellipsis) {
			if len(out) < h {
				out = append(out, s.Row(s.Muted.Render(l), w))
			}
		}
		for len(out) < h {
			out = append(out, s.Row("", w))
		}
		return out
	}
	running := v.Status == supervisor.Running || v.Status == supervisor.Starting
	mem := make([]float64, len(v.MemHistory))
	for i, n := range v.MemHistory {
		mem[i] = float64(n)
	}
	render := func(name string, current float64, vals []float64, floor float64, style lipgloss.Style, format func(float64) string, height int) {
		peak := 0.0
		for _, x := range vals {
			if !math.IsNaN(x) && x > peak {
				peak = x
			}
		}
		now := "stopped"
		if running {
			now = format(current)
		}
		left := s.Bold.Render(name) + s.Text.Render("  "+now)
		right := s.Muted.Render("peak ") + s.Text.Render(format(peak))
		out = append(out, s.Row(spread(left, right, w, g.Ellipsis), w))
		for _, row := range chart(g.Chart, vals, w, max(0, height-3), floor) {
			out = append(out, s.Row(style.Render(row), w))
		}
		if height >= 2 {
			out = append(out, s.Row(s.Muted.Render(strings.Repeat(g.Rule, max(0, w))), w))
		}
		if height >= 3 {
			// The span shown: braille holds two samples per cell.
			perCell := 2
			if _, ok := g.Chart.(theme.Levels); ok {
				perCell = 1
			}
			span := time.Duration(min(len(vals), perCell*w)) * interval
			out = append(out, note(env, "last "+durationLabel(span)+g.Separator+"a sample every "+durationLabel(interval), s.Muted, w))
		}
	}
	first := min(9, h/2)
	second := min(9, h-h/2)
	padding := h - first - second
	for i := 0; i < padding/3; i++ {
		out = append(out, s.Row("", w))
	}
	if first > 0 {
		render("CPU", v.CPU, v.CPUHistory, theme.CPUFloor, s.Running, percent, first)
	}
	for i := 0; i < padding/3; i++ {
		out = append(out, s.Row("", w))
	}
	if second > 0 {
		render("Memory", float64(v.Mem), mem, 0, s.Info, func(x float64) string { return bytes(int64(x)) }, second)
	}
	for len(out) < h {
		out = append(out, s.Row("", w))
	}
	return out[:h]
}

// emptyMetrics explains why there is no chart yet.
func emptyMetrics(v supervisor.View, interval time.Duration) string {
	switch v.Status {
	case supervisor.Running, supervisor.Starting:
		return "Running; the first sample comes within " + durationLabel(interval)
	}
	return "No samples yet. The daemon samples CPU and memory while the server runs."
}

// spread puts left at the start and right at the end of w cells, or both
// after each other when they do not fit.
func spread(left, right string, w int, ellipsis string) string {
	gap := w - ansi.StringWidth(left) - ansi.StringWidth(right)
	if gap < 1 {
		return cut(left+" "+right, w, ellipsis)
	}
	return left + strings.Repeat(" ", gap) + right
}

func percent(x float64) string {
	if x < 1 {
		return fmt.Sprintf("%.2f%%", x)
	}
	if x < 10 {
		return fmt.Sprintf("%.1f%%", x)
	}
	return fmt.Sprintf("%.0f%%", x)
}

func bytes(n int64) string {
	if n <= 0 {
		return "0 B"
	}
	return viewfmt.HumanBytes(n)
}

// chart draws vals newest at the right in w x rows cells with the plot
// alphabet p, scaled from 0 to their peak, or to floor when the peak is
// lower, so idle noise stays near the baseline.
func chart(p theme.Plot, vals []float64, w, rows int, floor float64) []string {
	if w <= 0 || rows <= 0 {
		return nil
	}
	peak := floor
	for _, v := range vals {
		if finite(v) && v > peak {
			peak = v
		}
	}
	scale := func(v float64) float64 {
		if peak <= 0 || !finite(v) {
			return 0
		}
		return math.Max(0, math.Min(1, v/peak))
	}
	if l, ok := p.(theme.Levels); ok {
		return levelsChart(l, vals, w, rows, scale)
	}
	return brailleChart(vals, w, rows, scale)
}

func finite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }

// levelsChart draws one sample per cell: full cells up to the sample's
// row and the step for the rest in its top cell; columns without a
// sample stay blank.
func levelsChart(l theme.Levels, vals []float64, w, rows int, scale func(float64) float64) []string {
	full := l.Steps[len(l.Steps)-1]
	cells := make([][]rune, rows)
	for y := range cells {
		cells[y] = []rune(strings.Repeat(string(l.Blank), w))
	}
	for x := max(0, w-len(vals)); x < w; x++ {
		level := scale(vals[len(vals)-w+x]) * float64(rows)
		for r := 0; r < rows; r++ {
			y := rows - 1 - r
			switch {
			case level >= float64(r+1):
				cells[y][x] = full
			case level > float64(r) || r == 0:
				// The bottom row always shows a sample, at its lowest
				// step when the value is 0.
				cells[y][x] = l.Step(level - float64(r))
			}
		}
	}
	out := make([]string, rows)
	for i := range cells {
		out[i] = string(cells[i])
	}
	return out
}

// brailleChart draws two samples per cell as a line of braille dots, four
// levels per row, joining each sample to the previous one.
func brailleChart(vals []float64, w, rows int, scale func(float64) float64) []string {
	cells := make([][]rune, rows)
	for y := range cells {
		cells[y] = []rune(strings.Repeat("⠀", w))
	}
	bits := [2][4]rune{{1, 2, 4, 64}, {8, 16, 32, 128}}
	columns := 2 * w
	previous := -1
	for x := max(0, columns-len(vals)); x < columns && len(vals) > 0; x++ {
		y := int(math.Round(scale(vals[len(vals)-columns+x]) * float64(rows*4-1)))
		low, high := y, y
		if previous >= 0 {
			low, high = min(y, previous), max(y, previous)
		}
		for z := low; z <= high; z++ {
			cells[rows-1-z/4][x/2] |= bits[x%2][3-z%4]
		}
		previous = y
	}
	out := make([]string, rows)
	for i := range cells {
		out[i] = string(cells[i])
	}
	return out
}
