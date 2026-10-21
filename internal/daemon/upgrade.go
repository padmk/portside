package daemon

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/padmk/portside/internal/app"
	"github.com/padmk/portside/internal/config"
	"github.com/padmk/portside/internal/paths"
	"github.com/padmk/portside/internal/supervisor"
)

// An upgrade replaces the daemon's binary without stopping its servers: the
// daemon re-executes the new binary in its own process, so the servers stay
// its children and launchd or systemd see no exit. The lock, the listening
// sockets and the servers' terminals stay open across the exec; a handoff
// file in the state directory tells the new daemon which descriptors they
// are and what state each server was in.

// handoffEnv names the variable that carries the handoff file's nonce into
// the new binary, so only an exec'd daemon reads the file.
var handoffEnv = app.Env("HANDOFF")

type handoff struct {
	Nonce     string               `json:"nonce"`
	PID       int                  `json:"pid"`
	Version   string               `json:"version"` // the version handing off
	Lock      int                  `json:"lock"`
	Port      int                  `json:"port"`
	Listeners []int                `json:"listeners"`
	Servers   []supervisor.Handoff `json:"servers"`
	// DashboardHost stays across the exec: the sockets were never released,
	// so no other program can have served a page on it.
	DashboardHost string `json:"dashboardHost"`
}

func handoffPath(stateDir string) string { return filepath.Join(stateDir, "handoff.json") }

// takeHandoff returns the handoff left for this process by the daemon it
// replaced, or nil. The file is removed either way.
func takeHandoff(stateDir string) *handoff {
	nonce := os.Getenv(handoffEnv)
	if nonce == "" {
		return nil
	}
	// Servers started from now on must not see it.
	_ = os.Unsetenv(handoffEnv)
	path := handoffPath(stateDir)
	data, err := os.ReadFile(path)
	_ = os.Remove(path)
	if err != nil {
		log.Printf("handoff: %v", err)
		return nil
	}
	var h handoff
	if err := json.Unmarshal(data, &h); err != nil {
		log.Printf("handoff: %v", err)
		return nil
	}
	// Every exec gets a new nonce, so a match means this exec.
	if h.Nonce != nonce || h.PID != os.Getpid() {
		log.Printf("handoff: the file does not belong to this process; starting afresh")
		return nil
	}
	return &h
}

// inheritLock takes over the lock descriptor after checking that it is the
// state directory's lock file and still holds the lock.
func inheritLock(fd int, path string) (*os.File, error) {
	var got, want syscall.Stat_t
	if fd <= 2 {
		return nil, errors.New("no lock descriptor")
	}
	if err := syscall.Fstat(fd, &got); err != nil {
		return nil, err
	}
	if err := syscall.Stat(path, &want); err != nil {
		return nil, err
	}
	if got.Dev != want.Dev || got.Ino != want.Ino {
		return nil, fmt.Errorf("descriptor %d is not %s", fd, path)
	}
	syscall.CloseOnExec(fd)
	f := os.NewFile(uintptr(fd), path)
	// The lock belongs to the open file, so locking it again succeeds.
	if err := syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}

// inheritListeners turns the handed-off sockets back into listeners and
// returns the port they share; when any is unusable, or they differ in
// port, all are closed and the caller listens anew.
func inheritListeners(fds []int) ([]net.Listener, int) {
	var out []net.Listener
	ok := len(fds) > 0
	port := 0
	for _, fd := range fds {
		syscall.CloseOnExec(fd)
		f := os.NewFile(uintptr(fd), "listener")
		ln, err := net.FileListener(f)
		f.Close()
		if err != nil {
			ok = false
			continue
		}
		a, isTCP := ln.Addr().(*net.TCPAddr)
		if !isTCP || (port != 0 && a.Port != port) {
			ok = false
		} else {
			port = a.Port
		}
		out = append(out, ln)
	}
	if !ok {
		closeListeners(out)
		return nil, 0
	}
	return out, port
}

// takeOverListeners serves on the sockets of an upgrade. A config without
// a port keeps the inherited port. When the config now sets another port,
// the daemon moves there if it can and otherwise keeps the inherited
// sockets, so an upgrade never leaves the daemon without a port. cfg.Port
// is set to the port served; inherited reports whether every socket came
// over, so the dashboard host is kept. Nil listeners mean listening anew.
func takeOverListeners(ho *handoff, cfg *config.Config) (lns []net.Listener, inherited bool, err error) {
	lns, port := inheritListeners(ho.Listeners)
	if lns == nil {
		return nil, false, nil
	}
	if port != cfg.Port && !cfg.PortAuto {
		moved, err := listen(cfg.Port)
		if err == nil {
			closeListeners(lns)
			log.Printf("handoff: moved from port %d to %d", port, cfg.Port)
			return moved, false, nil
		}
		log.Printf("handoff: %v; staying on port %d until the port is free and the daemon restarts", err, port)
	}
	cfg.Port = port
	n := len(lns)
	if lns, err = completeListeners(lns, port); err != nil {
		return nil, false, err
	}
	return lns, len(lns) == n, nil
}

// checkBinary runs `<exe> version` and returns the version it reports, so a
// binary that cannot run on this machine is refused before the exec.
func checkBinary(exe string) (string, error) {
	if !filepath.IsAbs(exe) {
		return "", fmt.Errorf("%s is not an absolute path", exe)
	}
	st, err := os.Stat(exe)
	if err != nil {
		return "", err
	}
	if !st.Mode().IsRegular() || st.Mode()&0o111 == 0 {
		return "", fmt.Errorf("%s is not an executable file", exe)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, "version")
	cmd.Env = withoutEnv(os.Environ(), handoffEnv)
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("%s does not run: %w", exe, err)
	}
	fields := strings.Fields(string(out))
	if len(fields) < 2 || fields[0] != app.ID {
		return "", fmt.Errorf("%s is not %s", exe, app.Name)
	}
	return fields[1], nil
}

// selfExecutable is the daemon's own binary path. On Linux a replaced
// binary reads as "<path> (deleted)"; the path then holds the new one.
func selfExecutable() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return strings.TrimSuffix(exe, " (deleted)"), nil
}

func withoutEnv(env []string, key string) []string {
	out := env[:0:0]
	for _, kv := range env {
		if !strings.HasPrefix(kv, key+"=") {
			out = append(out, kv)
		}
	}
	return out
}

func newNonce() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// listenerFiles duplicates the listening sockets, so the listeners
// themselves can be closed while the sockets stay open.
func listenerFiles(lns []net.Listener) ([]*os.File, error) {
	var files []*os.File
	for _, ln := range lns {
		tl, ok := ln.(*net.TCPListener)
		if !ok {
			closeFiles(files)
			return nil, errors.New("not a TCP listener")
		}
		f, err := tl.File()
		if err != nil {
			closeFiles(files)
			return nil, err
		}
		files = append(files, f)
	}
	return files, nil
}

// keepOnExec keeps files open across exec and returns their descriptor
// numbers, or (keep false) lets exec close them again.
func keepOnExec(files []*os.File, keep bool) ([]int, error) {
	var fds []int
	for _, f := range files {
		fd, err := supervisor.KeepOnExec(f, keep)
		if err != nil && keep {
			return nil, err
		}
		fds = append(fds, fd)
	}
	return fds, nil
}

func closeFiles(fs []*os.File) {
	for _, f := range fs {
		f.Close()
	}
}

// upgradeRequest asks the main loop to exec a checked binary; result
// receives the error when the exec did not happen.
type upgradeRequest struct {
	exe     string
	version string
	result  chan error
}

func writeHandoff(path string, h handoff) error {
	data, err := json.Marshal(h)
	if err != nil {
		return err
	}
	return paths.WriteFile(path, data)
}
