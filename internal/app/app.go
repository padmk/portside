// Package app holds the product identity in one place. Everything user-visible
// (binary name, env var prefix, service labels, directories) derives from it.
package app

import "strings"

const (
	// Name is the user-visible product name.
	Name = "Portside"
	// ID is the lowercase identifier used for the binary, directories and hostnames.
	ID = "portside"
	// ServiceLabel identifies the background service in launchd.
	ServiceLabel = "app.foreverbeta.portside"
	// MenuBarAppID is the macOS menu bar app's bundle identifier.
	MenuBarAppID = ServiceLabel + ".app"
	// DefaultPort is where the daemon serves the dashboard, API and *.localhost proxy.
	DefaultPort = 7878
	// PeerHeader marks requests between daemons on the owner's devices.
	// Browsers cannot add it without a CORS preflight, which is never granted.
	PeerHeader = "X-" + Name + "-Peer"
)

// Version is the build's version; the command sets it from its -ldflags value.
var Version = "dev"

// FallbackPorts are the ports, in order, that a daemon whose config sets no
// port tries when DefaultPort is taken: 7879-7899, then 17879-17899. Both
// ranges lie below the ephemeral ports of macOS (49152 and up) and Linux
// (32768 and up), so a port chosen once stays free of outgoing connections.
func FallbackPorts() []int {
	var out []int
	for _, base := range []int{DefaultPort, DefaultPort + 10000} {
		for p := base + 1; p <= base+21; p++ {
			out = append(out, p)
		}
	}
	return out
}

// Env returns the name of an environment variable in the product namespace,
// for example Env("CONFIG") == "PORTSIDE_CONFIG".
func Env(key string) string {
	return strings.ToUpper(ID) + "_" + key
}
