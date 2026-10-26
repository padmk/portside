package supervisor

import (
	"os/exec"
	"runtime"

	"github.com/padmk/portside/internal/app"
)

// Notify shows a desktop notification. Text travels as arguments, never as
// script source, so server names cannot inject AppleScript.
func Notify(title, msg string) {
	switch runtime.GOOS {
	case "darwin":
		_ = exec.Command("osascript",
			"-e", "on run argv",
			"-e", "display notification (item 2 of argv) with title (item 1 of argv) subtitle (item 3 of argv)",
			"-e", "end run",
			app.Name, msg, title).Run()
	default:
		if p, err := exec.LookPath("notify-send"); err == nil {
			_ = exec.Command(p, "--app-name="+app.Name, title, msg).Run()
		}
	}
}

// NotifyUser shows a desktop notification other than a crash (a container
// problem, a device pairing or connecting) under the same notify setting and
// desktop-client guard as crashes: while the app is connected it notifies
// from the state itself.
func (s *Supervisor) NotifyUser(title, message string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cfg.Notify && s.notifier != nil && s.desktopClients == 0 {
		go s.notifier(title, message)
	}
}
