package edit

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"

	"github.com/padmk/portside/internal/app"
	"github.com/padmk/portside/internal/editor"
	"github.com/padmk/portside/internal/paths"
	"github.com/padmk/portside/internal/tui/ui"
)

// copyPrefix starts the name of the private directory that holds the
// copy `:config` edits; the process id follows it, so leftovers of a run
// that was killed can be told from the copies of a running one.
var copyPrefix = app.ID + "-config-"

// runEditor runs the user's editor on file in this terminal. Tests
// replace it.
var runEditor = func(ctx context.Context, file string, in io.Reader, out, errOut io.Writer) error {
	cmd := editor.Command(file)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = in, out, errOut
	// The editor ends with the program (ui.Process): the UI then quits.
	return (&ui.Process{Ctx: ctx, Cmd: cmd}).Run()
}

// editCopy is a tea.ExecCommand that edits text in a private copy: Run
// writes the copy into a fresh private directory, runs the editor on it,
// reads it back and removes the directory before it returns, whatever
// happens to the result afterwards.
type editCopy struct {
	text        string
	ctx         context.Context
	in          io.Reader
	out, errOut io.Writer

	edited string
	ran    bool
	err    error
}

func (c *editCopy) SetStdin(r io.Reader)  { c.in = r }
func (c *editCopy) SetStdout(w io.Writer) { c.out = w }
func (c *editCopy) SetStderr(w io.Writer) { c.errOut = w }

func (c *editCopy) Run() error {
	c.ran = true
	c.edited, c.err = c.run()
	return c.err
}

func (c *editCopy) run() (string, error) {
	dir, err := os.MkdirTemp("", copyPrefix+strconv.Itoa(os.Getpid())+"-")
	if err != nil {
		return "", err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	file := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(file, []byte(c.text), paths.FileMode); err != nil {
		return "", err
	}
	if err := runEditor(c.ctx, file, c.in, c.out, c.errOut); err != nil {
		return "", errors.New("the editor (" + editor.Name() + ") failed: " + err.Error())
	}
	data, err := os.ReadFile(file)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// result is the edited text, or why there is none; err is what the
// program reported (the terminal could not be handed over when Run never
// ran). An error restoring the terminal after a good edit keeps the edit.
func (c *editCopy) result(err error) (string, error) {
	switch {
	case !c.ran && err != nil:
		return "", err
	case !c.ran:
		return "", errors.New("the editor did not run")
	}
	return c.edited, c.err
}

var leftovers sync.Once

// removeLeftovers removes, once per process, the private copies that a
// `:config` of a process that no longer runs left behind (it was killed
// while the editor ran). Copies of running processes stay.
func removeLeftovers() {
	leftovers.Do(func() { removeLeftoversIn(os.TempDir()) })
}

func removeLeftoversIn(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if leftover(dir, e) {
			_ = os.RemoveAll(filepath.Join(dir, e.Name()))
		}
	}
}

// leftover reports whether e in dir is a copy's directory of this user
// whose process has ended.
func leftover(dir string, e os.DirEntry) bool {
	rest, ok := strings.CutPrefix(e.Name(), copyPrefix)
	if !ok || !e.IsDir() {
		return false
	}
	pidText, _, ok := strings.Cut(rest, "-")
	pid, err := strconv.Atoi(pidText)
	if !ok || err != nil || pid <= 0 || pid == os.Getpid() {
		return false
	}
	info, err := os.Lstat(filepath.Join(dir, e.Name()))
	if err != nil {
		return false
	}
	if st, ok := info.Sys().(*syscall.Stat_t); !ok || int(st.Uid) != os.Getuid() {
		return false
	}
	return errors.Is(syscall.Kill(pid, 0), syscall.ESRCH)
}
