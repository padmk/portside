// Package editor runs the user's text editor on a file in this terminal, for
// the command line's `config edit` and the terminal UI's `:config`.
package editor

import (
	"os"
	"os/exec"
)

// Name is the editor command: $VISUAL, else $EDITOR, else vi.
func Name() string {
	for _, k := range []string{"VISUAL", "EDITOR"} {
		if v := os.Getenv(k); v != "" {
			return v
		}
	}
	return "vi"
}

// Command runs the editor on file. The editor command goes through sh, as
// git runs it, so it may hold arguments and quotes ("code --wait"); the file
// is passed as one argument and never interpreted by the shell. The caller
// connects the terminal.
func Command(file string) *exec.Cmd {
	// An editor runs until the user closes it; the terminal UI hangs it up
	// when the UI ends (ui.Process).
	return exec.Command("/bin/sh", "-c", Name()+` "$1"`, "sh", file) //nolint:noctx // see above
}
