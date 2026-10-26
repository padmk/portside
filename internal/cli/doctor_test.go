package cli

import (
	"fmt"
	"strings"
	"testing"

	"github.com/padmk/portside/internal/remote"
	"github.com/padmk/portside/internal/server"
	"github.com/padmk/portside/internal/share"
	"github.com/padmk/portside/internal/tailnet"
)

func TestDoctorRemote(t *testing.T) {
	run := func(ov server.RemoteOverview) (oks, warns []string) {
		doctorRemote(ov,
			func(m string, a ...any) { oks = append(oks, fmt.Sprintf(m, a...)) },
			func(m string, a ...any) { warns = append(warns, fmt.Sprintf(m, a...)) })
		return oks, warns
	}
	if oks, warns := run(server.RemoteOverview{}); len(oks)+len(warns) != 0 {
		t.Errorf("no sharing and no connection: %q %q", oks, warns)
	}

	oks, warns := run(server.RemoteOverview{
		Share: share.Status{Enabled: true, Active: true, URL: "https://mbp.t.ts.net:7878",
			Devices: []share.DeviceStatus{{Connected: true}, {}}},
		Connection: &remote.Info{Peer: tailnet.Node{Name: "devbox"}, Status: remote.Open,
			Mirrors: []remote.Mirror{{State: "mirrored"}, {State: "mirrored"}, {State: "busy"}, {State: "off"}}},
	})
	if len(warns) != 0 || len(oks) != 2 ||
		!strings.Contains(oks[0], "2 paired, 1 connected now") ||
		!strings.Contains(oks[1], "connected to devbox (2 ports forwarded, 1 busy here, 1 turned off)") {
		t.Errorf("sharing and an open connection: %q %q", oks, warns)
	}

	_, warns = run(server.RemoteOverview{
		Share:      share.Status{Enabled: true, Error: "Tailscale is not running"},
		Connection: &remote.Info{Peer: tailnet.Node{Name: "devbox"}, Status: remote.Reconnecting, Error: "timeout"},
	})
	if len(warns) != 2 || !strings.Contains(warns[0], "Tailscale is not running") || !strings.Contains(warns[1], "reconnecting") {
		t.Errorf("inactive sharing and a lost connection: %q", warns)
	}
}
