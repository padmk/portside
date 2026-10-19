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

// ErrNoSystemd means this Linux system does not run systemd, so there is
// no user service manager to register the daemon with.
var ErrNoSystemd = errors.New("this system does not run systemd")

// Supported reports whether this system has a service manager that Install
// can use: launchd on macOS, a running systemd on Linux.
func Supported() bool {
	switch runtime.GOOS {
	case "darwin":
		return true
	case "linux":
		return systemdRunning()
	}
	return false
}

// systemdRunning reports whether systemd is the init system, the check
// sd_booted(3) makes, and systemctl is installed.
func systemdRunning() bool {
	if _, err := exec.LookPath("systemctl"); err != nil {
		return false
	}
	st, err := os.Stat("/run/systemd/system")
	return err == nil && st.IsDir()
}

// NoSystemdHint explains how to start the daemon at login without systemd.
func NoSystemdHint() string {
	exe, err := executable()
	if err != nil {
		exe = app.ID
	}
	return fmt.Sprintf(`This system does not run systemd, so there is no login service to install.
Start the daemon from your shell profile instead, for example in ~/.profile:

    %[1]s daemon start >/dev/null 2>&1

or have your init system (OpenRC, runit, s6) run "%[1]s daemon" as your user.
Without either, any %[2]s command starts the daemon when it is needed.
`, exe, app.ID)
}

// Install writes the service definition and starts it. On Linux without
// systemd it returns ErrNoSystemd.
func Install(configPath string) error {
	if runtime.GOOS == "linux" && !systemdRunning() {
		return ErrNoSystemd
	}
	exe, err := executable()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(paths.StateDir(), paths.DirMode); err != nil {
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
  <dict><key>%s</key><string>%s</string><key>%s</key><string>%s</string></dict>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><dict><key>SuccessfulExit</key><false/></dict>
  <key>ProcessType</key><string>Interactive</string>
  <key>ExitTimeOut</key><integer>60</integer>
  <key>StandardOutPath</key><string>%s</string>
  <key>StandardErrorPath</key><string>%s</string>
</dict>
</plist>
`, app.ServiceLabel, xmlEscape(exe), app.Env("CONFIG"), xmlEscape(configPath),
		app.Env("STATE_DIR"), xmlEscape(paths.StateDir()),
		xmlEscape(paths.DaemonLog()), xmlEscape(paths.DaemonLog()))
	if err := os.WriteFile(file, []byte(plist), 0o644); err != nil {
		return err
	}
	_ = exec.Command("launchctl", "bootout", launchdTarget()).Run()
	return run("launchctl", "bootstrap", "gui/"+strconv.Itoa(os.Getuid()), file)
}

// installSystemd writes the user unit. It is wanted by default.target, so
// it also runs without a desktop (over SSH, with lingering); on a desktop it
// is ordered after graphical-session.target, but not part of it, so ending
// the desktop session leaves its servers running. That ordering does not
// give it DISPLAY: the desktop has to import its session variables into the
// user manager (most do with `systemctl --user import-environment` or
// `dbus-update-activation-environment --systemd` at session start), and
// importSessionEnv does so from the CLI.
func installSystemd(exe, file, configPath string) error {
	unit := fmt.Sprintf(`[Unit]
Description=%s dev server manager
After=graphical-session.target

[Service]
ExecStart=%s daemon
Environment=%s=%s
Environment=%s=%s
Restart=on-failure
KillMode=mixed
TimeoutStopSec=60

[Install]
WantedBy=default.target
`, app.Name, strconv.Quote(exe), app.Env("CONFIG"), strconv.Quote(configPath),
		app.Env("STATE_DIR"), strconv.Quote(paths.StateDir()))
	if err := os.WriteFile(file, []byte(unit), 0o644); err != nil {
		return err
	}
	importSessionEnv()
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
		_ = exec.Command("launchctl", "bootout", launchdTarget()).Run()
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
		out, err := exec.Command("launchctl", "print", launchdTarget()).Output()
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

// launchdTarget is the agent's service target in the user's GUI domain.
func launchdTarget() string {
	return "gui/" + strconv.Itoa(os.Getuid()) + "/" + app.ServiceLabel
}

// Owns reports whether the installed service runs the daemon for configPath
// with this process's state directory and the configured port, so starting
// the service starts the daemon that a client with these settings talks to.
// A service installed before it pinned the state directory runs with the
// default one.
func Owns(configPath string) bool {
	if os.Getenv(app.Env("PORT")) != "" {
		return false
	}
	data, err := os.ReadFile(File())
	if err != nil {
		return false
	}
	cfg, ok := unitEnv(string(data), app.Env("CONFIG"))
	if !ok || cfg != configPath {
		return false
	}
	state, ok := unitEnv(string(data), app.Env("STATE_DIR"))
	if !ok {
		return paths.Default(paths.DefaultConfigFile(), paths.StateDir())
	}
	return filepath.Clean(state) == filepath.Clean(paths.StateDir())
}

// unitEnv returns the value the service definition sets for the
// environment variable key.
func unitEnv(def, key string) (string, bool) {
	switch runtime.GOOS {
	case "darwin":
		prefix := "<key>" + key + "</key><string>"
		i := strings.Index(def, prefix)
		if i < 0 {
			return "", false
		}
		rest := def[i+len(prefix):]
		j := strings.Index(rest, "</string>")
		if j < 0 {
			return "", false
		}
		return strings.NewReplacer("&lt;", "<", "&gt;", ">", "&amp;", "&").Replace(rest[:j]), true
	case "linux":
		for _, line := range strings.Split(def, "\n") {
			if v, ok := strings.CutPrefix(strings.TrimSpace(line), "Environment="+key+"="); ok {
				if u, err := strconv.Unquote(v); err == nil {
					return u, true
				}
				return v, true
			}
		}
	}
	return "", false
}

// sessionEnv are the variables a daemon needs from the desktop session to
// show notifications and open editors and terminals.
var sessionEnv = []string{"DISPLAY", "WAYLAND_DISPLAY", "XDG_RUNTIME_DIR", "DBUS_SESSION_BUS_ADDRESS", "XDG_SESSION_TYPE"}

// importSessionEnv copies the desktop session's variables that this process
// has into the systemd user manager, so the service gets them when it
// starts. Desktops that import them themselves make this a no-op. A display
// forwarded over SSH is left out: it ends with the SSH session.
func importSessionEnv() {
	var names []string
	for _, k := range sessionEnv {
		if os.Getenv(k) == "" || (k == "DISPLAY" && !localDisplay()) {
			continue
		}
		names = append(names, k)
	}
	if len(names) == 0 {
		return
	}
	_ = exec.Command("systemctl", append([]string{"--user", "import-environment"}, names...)...).Run()
}

// localDisplay reports whether DISPLAY names this computer's own display
// rather than one forwarded over SSH (`ssh -X` sets localhost:10.0 and the
// like, with SSH_CONNECTION).
func localDisplay() bool {
	d := os.Getenv("DISPLAY")
	if d == "" || os.Getenv("SSH_CONNECTION") != "" {
		return false
	}
	host, _, _ := strings.Cut(d, ":")
	return host == "" || strings.HasPrefix(host, "/") || host == "unix"
}

// MissingDisplay reports, on Linux, whether this process runs in a desktop
// session (a local DISPLAY or a WAYLAND_DISPLAY) while the running
// service's daemon has neither, so it cannot show notifications or open
// windows.
func MissingDisplay() bool {
	if runtime.GOOS != "linux" || (!localDisplay() && (os.Getenv("WAYLAND_DISPLAY") == "" || os.Getenv("SSH_CONNECTION") != "")) {
		return false
	}
	out, err := exec.Command("systemctl", "--user", "show", "-p", "MainPID", "--value", app.ID+".service").Output()
	if err != nil {
		return false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil || pid <= 0 {
		return false
	}
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/environ")
	if err != nil {
		return false
	}
	for _, kv := range strings.Split(string(data), "\x00") {
		if strings.HasPrefix(kv, "DISPLAY=") || strings.HasPrefix(kv, "WAYLAND_DISPLAY=") {
			return false
		}
	}
	return true
}

// Start starts the installed service's daemon if it is not running.
func Start() error {
	switch runtime.GOOS {
	case "darwin":
		return run("launchctl", "kickstart", launchdTarget())
	case "linux":
		importSessionEnv()
		return run("systemctl", "--user", "start", app.ID+".service")
	}
	return fmt.Errorf("services are not supported on %s", runtime.GOOS)
}

// Restart stops the service's daemon, if it runs, and starts it again.
func Restart() error {
	switch runtime.GOOS {
	case "darwin":
		return run("launchctl", "kickstart", "-k", launchdTarget())
	case "linux":
		importSessionEnv()
		return run("systemctl", "--user", "restart", app.ID+".service")
	}
	return fmt.Errorf("services are not supported on %s", runtime.GOOS)
}

func run(name string, args ...string) error {
	if out, err := exec.Command(name, args...).CombinedOutput(); err != nil {
		return fmt.Errorf("%s %s: %v: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

// LingerOff reports whether systemd ends the account's user services at
// logout (Linux only), so the daemon neither outlives a session nor starts at
// boot. It is false when this cannot be determined.
func LingerOff() bool {
	if runtime.GOOS != "linux" {
		return false
	}
	user := os.Getenv("USER")
	if user == "" {
		return false
	}
	out, err := exec.Command("loginctl", "show-user", user, "-p", "Linger").Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) == "Linger=no"
}

// MenuBarAppStartsDaemon reports whether the macOS menu bar app opens at
// login, which starts the daemon too. The app records that setting in its
// preferences.
func MenuBarAppStartsDaemon() bool {
	if runtime.GOOS != "darwin" {
		return false
	}
	out, err := exec.Command("defaults", "read", app.MenuBarAppID, "launchAtLogin").Output()
	return err == nil && strings.TrimSpace(string(out)) == "1"
}
