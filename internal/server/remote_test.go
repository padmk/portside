package server

import "testing"

func TestPeerDenied(t *testing.T) {
	cases := map[string]bool{
		"/api/state":                 false,
		"/api/servers/web/start":     false,
		"/api/config":                false,
		"/api/containers/db/shell":   false,
		"/api/open":                  true, // acts on this computer's desktop
		"/api/shutdown":              true,
		"/api/auth/code":             true, // mints sign-in links
		"/api/remote":                true, // sharing and onward connections
		"/api/remote/share":          true,
		"/api/remote/connect":        true,
		"/api/peer/x.t.ts.net/state": true,
	}
	for path, want := range cases {
		if got := peerDenied(path); got != want {
			t.Errorf("peerDenied(%q) = %v, want %v", path, got, want)
		}
	}
}
