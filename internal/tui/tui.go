// Package tui is the full-screen terminal client of the daemon
// (design/tui.md).
package tui

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	tea "charm.land/bubbletea/v2"
	"golang.org/x/term"

	"github.com/padmk/portside/internal/app"
	"github.com/padmk/portside/internal/client"
	"github.com/padmk/portside/internal/tui/live"
	"github.com/padmk/portside/internal/tui/remote"
	"github.com/padmk/portside/internal/tui/theme"
	"github.com/padmk/portside/internal/tui/ui"
)

// frameRate caps the redraws per second (design/tui.md, principle "Fast and calm").
const frameRate = 30

// Options are the command's flags.
type Options struct {
	// Theme is a family id, optionally with "-light" or "-dark".
	Theme string
	// Icons is a glyph set id: "unicode", "nerd" or "ascii".
	Icons string
	// Computer names the connected computer the client acts on; empty for
	// this one.
	Computer string
	// Conn is the connection to that computer, for how its servers' URLs
	// open from here.
	Conn *client.Connection
	// Local is the client of this computer's daemon when the session
	// starts on another computer (--on): the computers, the daemon and the
	// agent setup always act here.
	Local *client.Client
}

// Validate checks the theme and icons flags.
func (o Options) Validate() error {
	if _, _, err := pickTheme(o.Theme, prefs{}); err != nil {
		return err
	}
	_, err := pickGlyphs(o.Icons, prefs{})
	return err
}

// Run shows the terminal UI until the user quits or ctx ends.
func Run(ctx context.Context, c *client.Client, o Options) error {
	p := loadPrefs()
	family, variant, err := pickTheme(o.Theme, p)
	if err != nil {
		return err
	}
	glyphs, err := pickGlyphs(o.Icons, p)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stream := live.New()
	m := newModel(c, stream, ui.Session{Computer: o.Computer, Conn: o.Conn}, p, family, variant, glyphs)
	m.env.Context = ctx
	if o.Local != nil {
		m.sys.Local = o.Local
	}
	opts := []tea.ProgramOption{tea.WithContext(ctx), tea.WithFPS(frameRate)}
	if os.Getenv("NO_COLOR") != "" {
		opts = append(opts, tea.WithEnvironment(noColorEnviron()))
	}
	prog := tea.NewProgram(m, opts...)
	m.streams = remote.NewStreams(ctx, prog.Send)
	m.streams.Start(stream, c, 0)
	return m.runProgram(prog, cancel)
}

const (
	// joinWait bounds the wait for the UI's own goroutines after the
	// program ended: the terminal is restored by then, and a goroutine
	// that does not end must not keep the process from exiting.
	joinWait = 3 * time.Second
	// quitWait is how long the program has to end after a SIGTERM, on
	// which Bubble Tea quits, before it is killed (which restores the
	// terminal); killWait is how long that may take before the terminal
	// is restored here and the process exits. A suspended session ends
	// within its own wait (ui.Process) before quitWait.
	quitWait = 5 * time.Second
	killWait = 2 * time.Second
)

// errStopped is the error of a program that did not end after a SIGTERM.
var errStopped = fmt.Errorf("the terminal UI did not end within %v of SIGTERM and was stopped", quitWait)

// runProgram runs the program until it quits, then ends the streams and
// the log feeds (cancel ends the program's context) and waits for them, at
// most joinWait. A goroutine's panic that quit the program is the error.
func (m *model) runProgram(prog *tea.Program, cancel context.CancelFunc) error {
	ended := make(chan struct{})
	killed := watchTermination(prog, ended)
	_, err := prog.Run()
	close(ended)
	if killed() {
		err = errStopped
	}
	// The streams end with the program, not with the process.
	cancel()
	joined := make(chan struct{})
	go func() {
		defer close(joined)
		if c, ok := m.right.(interface{ Close() }); ok {
			c.Close()
		}
		if m.streams != nil {
			m.streams.Wait()
		}
	}()
	select {
	case <-joined:
	case <-time.After(joinWait):
		fmt.Fprintf(os.Stderr, "%s tui: the live streams did not end within %v; exiting without them\n", app.ID, joinWait)
	}
	if m.panicked != nil {
		return m.panicked
	}
	return err
}

// watchTermination bounds the end of the program after a SIGTERM: past
// quitWait it is killed; past killWait more, the terminal is put back as
// it was before the program and the process exits. killed reports
// whether it had to kill the program.
func watchTermination(prog *tea.Program, ended <-chan struct{}) (killed func() bool) {
	var stopped atomic.Bool
	fd := int(os.Stdin.Fd())
	saved, _ := term.GetState(fd)
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM)
	go func() {
		defer signal.Stop(sig)
		select {
		case <-ended:
			return
		case <-sig:
		}
		select {
		case <-ended:
			return
		case <-time.After(quitWait):
		}
		stopped.Store(true)
		go prog.Kill()
		select {
		case <-ended:
			return
		case <-time.After(killWait):
		}
		if saved != nil {
			_ = term.Restore(fd, saved)
		}
		// Off: mouse, bracketed paste, modifyOtherKeys and the kitty
		// keyboard; the cursor on; the alternate screen left.
		fmt.Fprint(os.Stdout, "\x1b[?1002l\x1b[?1003l\x1b[?1006l\x1b[?2004l\x1b[>4m\x1b[<u\x1b[?25h\x1b[?1049l")
		fmt.Fprintf(os.Stderr, "%s: %v\n", app.ID, errStopped)
		os.Exit(1)
	}()
	return stopped.Load
}

// pickTheme resolves the theme family and forced variant from the flag,
// else the preferences, else Terminal following the terminal's background.
func pickTheme(flag string, p prefs) (theme.Family, string, error) {
	if flag == "" {
		if f, ok := theme.Lookup(p.Theme); ok {
			return f, p.Variant, nil
		}
		return theme.TerminalFamily, p.Variant, nil
	}
	if f, ok := theme.Lookup(flag); ok {
		return f, "", nil
	}
	for _, v := range []string{"dark", "light"} {
		if id, ok := strings.CutSuffix(flag, "-"+v); ok {
			if f, ok := theme.Lookup(id); ok {
				return f, v, nil
			}
		}
	}
	var ids []string
	for _, f := range theme.Families() {
		ids = append(ids, f.ID)
	}
	return theme.Family{}, "", fmt.Errorf("unknown theme %q; themes: %s, each with -light or -dark", flag, strings.Join(ids, ", "))
}

// pickGlyphs resolves the glyph set from the flag, else the preferences,
// else nerd when $NERD_FONT is set, ascii in a locale that is not UTF-8,
// else unicode.
func pickGlyphs(flag string, p prefs) (theme.Glyphs, error) {
	if flag != "" {
		if g, ok := glyphSet(flag); ok {
			return g, nil
		}
		return theme.Glyphs{}, fmt.Errorf("unknown icons %q; use unicode, nerd or ascii", flag)
	}
	if g, ok := glyphSet(p.Icons); ok {
		return g, nil
	}
	if os.Getenv("NERD_FONT") != "" {
		return theme.Nerd, nil
	}
	if !utf8Locale() {
		return theme.ASCII, nil
	}
	return theme.Unicode, nil
}

func glyphSet(id string) (theme.Glyphs, bool) {
	for _, g := range theme.GlyphSets() {
		if g.ID == id {
			return g, true
		}
	}
	return theme.Glyphs{}, false
}

// utf8Locale reports whether the locale's character set, from the first of
// LC_ALL, LC_CTYPE and LANG that is set, is UTF-8.
func utf8Locale() bool {
	for _, name := range []string{"LC_ALL", "LC_CTYPE", "LANG"} {
		if v := os.Getenv(name); v != "" {
			v = strings.ToLower(v)
			return strings.Contains(v, "utf-8") || strings.Contains(v, "utf8")
		}
	}
	return false
}

// noColorEnviron is the environment with NO_COLOR in the form the color
// profile detection reads as set, since it accepts only boolean values.
func noColorEnviron() []string {
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "NO_COLOR=") {
			env = append(env, kv)
		}
	}
	return append(env, "NO_COLOR=1")
}
