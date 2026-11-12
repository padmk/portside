package tui_test

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/creack/pty"
	"golang.org/x/sys/unix"

	"github.com/padmk/portside/internal/app"
)

// TestTerminalIsRestored runs the built command's terminal UI in a pty
// against an isolated daemon and ends it three ways: q after the daemon
// went away (a lost connection), SIGTERM, and the terminal closing (the
// pty's master side, as when an SSH session drops). Each time the program
// exits, and where the output can still be read, its last bytes leave the
// alternate screen, show the cursor and turn mouse reporting, bracketed
// paste and keyboard enhancements off; the tty is back in the mode it
// started in (canonical input with echo).
func TestTerminalIsRestored(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the command and starts a daemon")
	}
	bin := buildCommand(t)
	env, port := isolatedEnv(t)
	daemon := startDaemon(t, bin, env, port)

	t.Run("SIGTERM", func(t *testing.T) {
		s := startTUI(t, bin, env)
		if err := s.cmd.Process.Signal(syscall.SIGTERM); err != nil {
			t.Fatal(err)
		}
		s.waitExit(t)
		s.checkRestored(t)
	})
	t.Run("terminal closed", func(t *testing.T) {
		s := startTUI(t, bin, env)
		close(s.stop)
		<-s.stopped
		_ = s.tty.Close()
		_ = s.master.Close()
		s.waitExit(t)
	})
	t.Run("q after a lost connection", func(t *testing.T) {
		s := startTUI(t, bin, env)
		stopProcess(t, daemon)
		// The UI keeps running and says it lost the daemon.
		time.Sleep(1500 * time.Millisecond)
		if s.exited() {
			t.Fatalf("the UI ended with the daemon:\n%q", s.tail(400))
		}
		s.write(t, "q")
		s.waitExit(t)
		s.checkRestored(t)
	})
}

func buildCommand(t *testing.T) string {
	t.Helper()
	goTool, err := exec.LookPath("go")
	if err != nil {
		t.Skip("no go command to build with")
	}
	mod, err := exec.Command(goTool, "env", "GOMOD").Output()
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(t.TempDir(), app.ID)
	build := exec.Command(goTool, "build", "-o", bin, "./cmd/"+app.ID)
	build.Dir = filepath.Dir(strings.TrimSpace(string(mod)))
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	return bin
}

// isolatedEnv is the environment of a daemon on a free loopback port with
// its config and state in temporary directories.
func isolatedEnv(t *testing.T) ([]string, int) {
	t.Helper()
	dir := t.TempDir()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	if err := os.MkdirAll(filepath.Join(dir, "projects", "web"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := fmt.Sprintf(`root = %q
port = %d
restore = false
notify = false

[servers.web]
dir = "web"
cmd = "sleep 600"
port = 0
`, filepath.Join(dir, "projects"), port)
	conf := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(conf, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	return append(os.Environ(),
		app.Env("CONFIG")+"="+conf,
		app.Env("STATE_DIR")+"="+filepath.Join(dir, "state"),
		"TERM=xterm-256color", "LANG=en_US.UTF-8", "NO_COLOR=",
	), port
}

// startDaemon runs the daemon until the test ends; it is up when its
// port accepts connections and its token is written.
func startDaemon(t *testing.T, bin string, env []string, port int) *exec.Cmd {
	t.Helper()
	d := exec.Command(bin, "daemon")
	d.Env = env
	var out bytes.Buffer
	d.Stdout, d.Stderr = &out, &out
	if err := d.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stopProcess(t, d) })
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	for deadline := time.Now().Add(15 * time.Second); ; {
		if c, err := net.DialTimeout("tcp", addr, time.Second); err == nil {
			c.Close()
			return d
		}
		if time.Now().After(deadline) {
			t.Fatalf("the daemon did not answer:\n%s", out.String())
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// stopProcess ends a process this test started, once.
func stopProcess(t *testing.T, c *exec.Cmd) {
	t.Helper()
	if c.ProcessState != nil {
		return
	}
	_ = c.Process.Signal(syscall.SIGTERM)
	done := make(chan struct{})
	go func() { _ = c.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		_ = c.Process.Kill()
		<-done
	}
}

// session is the terminal UI in a pty: the test keeps the tty side open
// too, so its mode can be read after the program ends.
type session struct {
	cmd    *exec.Cmd
	master *os.File
	// fd is the master's descriptor, non-blocking: reading it never
	// blocks the reader (os.File.Fd would make it blocking again).
	fd      int
	tty     *os.File
	initial *unix.Termios
	done    chan struct{}
	// stop ends the reader; stopped is closed when it has.
	stop, stopped chan struct{}

	mu  sync.Mutex
	out bytes.Buffer
}

func startTUI(t *testing.T, bin string, env []string) *session {
	t.Helper()
	master, tty, err := pty.Open()
	if err != nil {
		t.Skipf("no pty: %v", err)
	}
	t.Cleanup(func() { tty.Close() })
	if err := pty.Setsize(master, &pty.Winsize{Rows: 24, Cols: 80}); err != nil {
		t.Fatal(err)
	}
	initial, err := unix.IoctlGetTermios(int(master.Fd()), getTermios)
	if err != nil {
		t.Fatal(err)
	}
	c := exec.Command(bin, "tui")
	c.Env = env
	c.Stdin, c.Stdout, c.Stderr = tty, tty, tty
	c.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true}
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	s := &session{cmd: c, master: master, tty: tty, initial: initial, done: make(chan struct{})}
	go func() { _ = c.Wait(); close(s.done) }()
	t.Cleanup(func() {
		if !s.exited() {
			_ = c.Process.Kill()
			<-s.done
		}
		select {
		case <-s.stop:
		default:
			close(s.stop)
		}
		<-s.stopped
		master.Close()
	})
	// The reader never blocks in a read: one blocked on the master would
	// hold it open after Close, and the UI would never see its terminal
	// go away. Polling alone is not enough: macOS's poll can report a
	// pty ready (POLLNVAL) with nothing to read.
	fd := int(master.Fd())
	if err := unix.SetNonblock(fd, true); err != nil {
		t.Fatal(err)
	}
	s.fd = fd
	s.stop, s.stopped = make(chan struct{}), make(chan struct{})
	go func() {
		defer close(s.stopped)
		buf := make([]byte, 64<<10)
		for {
			select {
			case <-s.stop:
				return
			default:
			}
			fds := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
			if n, err := unix.Poll(fds, 50); err != nil && err != unix.EINTR {
				return
			} else if n == 0 {
				continue
			}
			n, err := unix.Read(fd, buf)
			if n > 0 {
				s.mu.Lock()
				s.out.Write(buf[:n])
				s.mu.Unlock()
			}
			if err != nil && err != unix.EINTR && err != unix.EAGAIN {
				return
			}
			if n == 0 && err == nil {
				return
			}
		}
	}()
	// Up when the first frame names the servers pane.
	for deadline := time.Now().Add(10 * time.Second); !strings.Contains(s.text(), "Servers"); {
		if time.Now().After(deadline) || s.exited() {
			t.Fatalf("no first frame:\n%q", s.tail(400))
		}
		time.Sleep(50 * time.Millisecond)
	}
	time.Sleep(300 * time.Millisecond)
	return s
}

func (s *session) text() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.out.String()
}

func (s *session) tail(n int) string {
	out := s.text()
	return out[max(0, len(out)-n):]
}

func (s *session) exited() bool {
	select {
	case <-s.done:
		return true
	default:
		return false
	}
}

func (s *session) write(t *testing.T, keys string) {
	t.Helper()
	if _, err := s.master.Write([]byte(keys)); err != nil {
		t.Fatal(err)
	}
}

// waitExit waits for the program to end, then for its last output.
func (s *session) waitExit(t *testing.T) {
	t.Helper()
	select {
	case <-s.done:
	case <-time.After(10 * time.Second):
		t.Fatalf("the UI did not exit:\n%q", s.tail(400))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	for last := len(s.text()); ctx.Err() == nil; {
		time.Sleep(200 * time.Millisecond)
		n := len(s.text())
		if n == last {
			break
		}
		last = n
	}
}

// restores pairs each mode the UI turns on with what turns it off; the
// off sequence must come after the last on.
var restores = []struct {
	what    string
	on, off *regexp.Regexp
}{
	{"alternate screen", regexp.MustCompile(`\x1b\[\?1049h`), regexp.MustCompile(`\x1b\[\?1049l`)},
	{"hidden cursor", regexp.MustCompile(`\x1b\[\?25l`), regexp.MustCompile(`\x1b\[\?25h`)},
	{"mouse button events", regexp.MustCompile(`\x1b\[\?1002h`), regexp.MustCompile(`\x1b\[\?1002l`)},
	{"SGR mouse", regexp.MustCompile(`\x1b\[\?1006h`), regexp.MustCompile(`\x1b\[\?1006l`)},
	{"bracketed paste", regexp.MustCompile(`\x1b\[\?2004h`), regexp.MustCompile(`\x1b\[\?2004l`)},
	{"modifyOtherKeys", regexp.MustCompile(`\x1b\[>4;[12]m`), regexp.MustCompile(`\x1b\[>4m`)},
	{"kitty keyboard", regexp.MustCompile(`\x1b\[>[0-9]+u`), regexp.MustCompile(`\x1b\[<[0-9]*u`)},
}

func (s *session) checkRestored(t *testing.T) {
	t.Helper()
	out := s.text()
	for _, r := range restores {
		on := lastIndex(r.on, out)
		off := lastIndex(r.off, out)
		switch {
		case on < 0 && (r.what == "alternate screen" || r.what == "mouse button events" || r.what == "bracketed paste"):
			t.Errorf("the UI never turned on %s: the test does not see its output", r.what)
		case on >= 0 && off < on:
			t.Errorf("%s is left on: last on at byte %d, last off at %d; tail %q", r.what, on, off, out[max(0, len(out)-300):])
		}
	}
	s.checkTermios(t)
}

// checkTermios checks that the tty reads lines with echo again, as it did
// before the program put it in raw mode.
func (s *session) checkTermios(t *testing.T) {
	t.Helper()
	now, err := unix.IoctlGetTermios(s.fd, getTermios)
	if err != nil {
		t.Fatalf("reading the tty's mode: %v", err)
	}
	const flags = unix.ICANON | unix.ECHO | unix.ISIG
	if now.Lflag&flags != s.initial.Lflag&flags {
		t.Errorf("the tty's local flags are %#x, were %#x", now.Lflag&flags, s.initial.Lflag&flags)
	}
}

func lastIndex(re *regexp.Regexp, s string) int {
	all := re.FindAllStringIndex(s, -1)
	if len(all) == 0 {
		return -1
	}
	return all[len(all)-1][0]
}
