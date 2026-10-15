// Package service registers the daemon with the OS so it starts at login:
// a launchd agent on macOS, a systemd user unit on Linux.
package service

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/padmk/portside/internal/app"
	"github.com/padmk/portside/internal/paths"
)

// Status describes the installed service.
type Status struct {
	Installed bool
	Running   bool
	File      string
	Detail    string
}

func executable() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if r, err := filepath.EvalSymlinks(exe); err == nil {
		exe = r
	}
	if strings.Contains(exe, "/go-build") {
		return "", errors.New("refusing to install a temporary `go run` binary; build or install it first")
	}
	return exe, nil
}

// File returns the path of the service definition.
func File() string {
	home, _ := os.UserHomeDir()
	if runtime.GOOS == "darwin" {
		return filepath.Join(home, "Library", "LaunchAgents", app.ServiceLabel+".plist")
	}
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "systemd", "user", app.ID+".service")
}

// Install writes the service definition and starts it.
func Install(configPath string) error {
	exe, err := executable()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(paths.StateDir(), 0o755); err != nil {
		return err
	}
	file := File()
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return err
	}
	switch runtime.GOOS {
	case "darwin":
		return installLaunchd(exe, file, configPath)
	case "linux":
		return installSystemd(exe, file, configPath)
	}
	return fmt.Errorf("services are not supported on %s", runtime.GOOS)
}

func xmlEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}

func installLaunchd(exe, file, configPath string) error {
	plist := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>%s</string>
  <key>ProgramArguments</key>
  <array><string>%s</string><string>daemon</string></array>
  <key>EnvironmentVariables</key>
  <dict><key>%s</key><string>%s</string></dict>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><dict><key>SuccessfulExit</key><false/></dict>
  <key>ProcessType</key><string>Interactive</string>
  <key>StandardOutPath</key><string>%s</string>
  <key>StandardErrorPath</key><string>%s</string>
</dict>
</plist>
`, app.ServiceLabel, xmlEscape(exe), app.Env("CONFIG"), xmlEscape(configPath),
		xmlEscape(paths.DaemonLog()), xmlEscape(paths.DaemonLog()))
	if err := os.WriteFile(file, []byte(plist), 0o644); err != nil {
		return err
	}
	domain := "gui/" + strconv.Itoa(os.Getuid())
	_ = exec.Command("launchctl", "bootout", domain+"/"+app.ServiceLabel).Run()
	if out, err := exec.Command("launchctl", "bootstrap", domain, file).CombinedOutput(); err != nil {
		return fmt.Errorf("launchctl bootstrap: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func installSystemd(exe, file, configPath string) error {
	unit := fmt.Sprintf(`[Unit]
Description=%s dev server manager

[Service]
ExecStart=%s daemon
Environment=%s=%s
Restart=on-failure
KillMode=mixed
TimeoutStopSec=60

[Install]
WantedBy=default.target
`, app.Name, strconv.Quote(exe), app.Env("CONFIG"), strconv.Quote(configPath))
	if err := os.WriteFile(file, []byte(unit), 0o644); err != nil {
		return err
	}
	for _, args := range [][]string{{"--user", "daemon-reload"}, {"--user", "enable", "--now", app.ID + ".service"}} {
		if out, err := exec.Command("systemctl", args...).CombinedOutput(); err != nil {
			return fmt.Errorf("systemctl %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
		}
	}
	return nil
}

// Uninstall stops the service and removes its definition.
func Uninstall() error {
	file := File()
	switch runtime.GOOS {
	case "darwin":
		_ = exec.Command("launchctl", "bootout", "gui/"+strconv.Itoa(os.Getuid())+"/"+app.ServiceLabel).Run()
	case "linux":
		_ = exec.Command("systemctl", "--user", "disable", "--now", app.ID+".service").Run()
	}
	if err := os.Remove(file); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if runtime.GOOS == "linux" {
		_ = exec.Command("systemctl", "--user", "daemon-reload").Run()
	}
	return nil
}

// Query reports whether the service is installed and running.
func Query() Status {
	st := Status{File: File()}
	if _, err := os.Stat(st.File); err == nil {
		st.Installed = true
	}
	switch runtime.GOOS {
	case "darwin":
		out, err := exec.Command("launchctl", "print", "gui/"+strconv.Itoa(os.Getuid())+"/"+app.ServiceLabel).Output()
		if err == nil {
			st.Running = strings.Contains(string(out), "state = running")
		}
	case "linux":
		out, _ := exec.Command("systemctl", "--user", "is-active", app.ID+".service").Output()
		st.Detail = strings.TrimSpace(string(out))
		st.Running = st.Detail == "active"
	}
	return st
}
