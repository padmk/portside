package cli

import (
	"os"
	"os/exec"
	"strings"
)

// browserCommand is the command that opens u in a browser of this session,
// or nil when there is none and the address should be printed instead: over
// SSH, where a browser would open on the remote machine, or without a
// desktop.
func browserCommand(goos string, getenv func(string) string, u string) []string {
	if getenv("SSH_CONNECTION") != "" {
		return nil
	}
	// $BROWSER lists commands separated by colons; %s stands for the address.
	for _, entry := range strings.Split(getenv("BROWSER"), ":") {
		f := strings.Fields(entry)
		if len(f) == 0 || !onPath(f[0]) {
			continue
		}
		placed := false
		for i, a := range f[1:] {
			if strings.Contains(a, "%s") {
				f[i+1], placed = strings.ReplaceAll(a, "%s", u), true
			}
		}
		if !placed {
			f = append(f, u)
		}
		return f
	}
	if goos == "darwin" {
		return []string{"open", u}
	}
	if goos == "linux" && isWSL() {
		// The browser runs on Windows.
		for _, c := range []string{"wslview", "explorer.exe"} {
			if onPath(c) {
				return []string{c, u}
			}
		}
		return nil
	}
	if getenv("DISPLAY") == "" && getenv("WAYLAND_DISPLAY") == "" {
		return nil
	}
	if onPath("xdg-open") {
		return []string{"xdg-open", u}
	}
	return nil
}

func onPath(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

// isWSL reports whether this is Linux under the Windows Subsystem for Linux.
func isWSL() bool {
	data, err := os.ReadFile("/proc/version")
	return err == nil && strings.Contains(strings.ToLower(string(data)), "microsoft")
}
