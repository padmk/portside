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

// Version is set at build time with -ldflags "-X .../internal/app.Version=v1.2.3".
var Version = "dev"

// Env returns the name of an environment variable in the product namespace,
// for example Env("CONFIG") == "PORTSIDE_CONFIG".
func Env(key string) string {
	return strings.ToUpper(ID) + "_" + key
}
