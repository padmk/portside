package client

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/padmk/portside/internal/app"
	"github.com/padmk/portside/internal/paths"
	"github.com/padmk/portside/internal/ports"
)

// Holder is what listens on a port that this account's daemon wants.
type Holder struct {
	PID     int
	Command string
	// Own is true when lsof or ss name the listening process, it belongs to
	// this account and its command is this product's binary. A pid claimed
	// only by what answers on the port, or a process of this account that
	// forwards the port (such as an ssh tunnel to another computer's
	// daemon), is not taken for this product.
	Own bool
	// StateDir is the state directory of such a daemon, when its lock file
	// can be seen among its open files.
	StateDir string
}

// Ours reports whether the holder is this product running for this account,
// such as a daemon with another state directory. Its port is never taken
// over: moving away would leave two daemons serving one config.
func (h Holder) Ours() bool { return h.Own }

// Refusal explains why a daemon does not start on port while h holds it.
func (h Holder) Refusal(port int) string {
	who := fmt.Sprintf("another %s daemon of this account (pid %d", app.Name, h.PID)
	if h.StateDir != "" {
		who += ", state " + paths.ShortenHome(h.StateDir)
	}
	who += ")"
	return fmt.Sprintf("port %d is held by %s; stop that daemon first, or run this one with its own config (%s) that sets another `port`", port, who, app.Env("CONFIG"))
}

// PortHolder finds out what listens on port on loopback. Only lsof or ss
// are trusted to name the process (another account's are usually invisible
// to them), and its command name to tell whether it is this product; what
// answers on the port cannot prove either.
func PortHolder(ctx context.Context, port int) Holder {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	var h Holder
	l, ok := ports.Owner(ctx, port)
	if !ok || l.PID <= 0 {
		return h
	}
	h.PID, h.Command = l.PID, l.Command
	h.Own = strings.HasPrefix(filepath.Base(h.Command), app.ID) && syscall.Kill(h.PID, 0) == nil
	if h.Own {
		h.StateDir = lockDir(ctx, h.PID)
	}
	return h
}

// lockDir returns the directory of the daemon lock that pid holds open, or "".
func lockDir(ctx context.Context, pid int) string {
	const lock = paths.LockName
	if runtime.GOOS == "linux" {
		fdDir := "/proc/" + strconv.Itoa(pid) + "/fd"
		if ents, err := os.ReadDir(fdDir); err == nil {
			for _, e := range ents {
				if t, err := os.Readlink(filepath.Join(fdDir, e.Name())); err == nil && filepath.Base(t) == lock {
					return filepath.Dir(t)
				}
			}
			return ""
		}
	}
	out, err := exec.CommandContext(ctx, "lsof", "-nP", "-a", "-p", strconv.Itoa(pid), "-Fn").Output()
	if err != nil && len(out) == 0 {
		return ""
	}
	for line := range strings.SplitSeq(string(out), "\n") {
		if n, ok := strings.CutPrefix(line, "n"); ok && filepath.Base(n) == lock {
			return filepath.Dir(n)
		}
	}
	return ""
}
