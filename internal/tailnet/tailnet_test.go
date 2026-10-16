package tailnet

import (
	"testing"
)

const statusJSON = `{
  "BackendState": "Running",
  "Self": {"HostName": "devbox", "DNSName": "devbox.tail1.ts.net.", "OS": "macOS",
           "TailscaleIPs": ["100.64.0.10", "fd7a::1"], "Online": true, "UserID": 7},
  "Peer": {
    "a": {"HostName": "MBP", "DNSName": "mbp.tail1.ts.net.", "OS": "macOS",
          "TailscaleIPs": ["100.64.0.11"], "Online": true, "UserID": 7},
    "b": {"HostName": "build", "DNSName": "build.tail1.ts.net.", "OS": "linux",
          "TailscaleIPs": ["100.1.1.1"], "Online": true, "UserID": 7, "Tags": ["tag:ci"]},
    "c": {"HostName": "friend", "DNSName": "friend.tail1.ts.net.", "OS": "macOS",
          "TailscaleIPs": ["100.2.2.2"], "Online": true, "UserID": 9}
  },
  "User": {"7": {"LoginName": "me@example.com"}, "9": {"LoginName": "friend@example.com"}}
}`

func TestParseStatus(t *testing.T) {
	st, err := parseStatus([]byte(statusJSON))
	if err != nil {
		t.Fatal(err)
	}
	if st.Owner != "me@example.com" {
		t.Errorf("Owner = %q", st.Owner)
	}
	if st.Self.DNSName != "devbox.tail1.ts.net" || st.Self.Label != "devbox" {
		t.Errorf("Self = %+v, want the DNS name without its trailing dot", st.Self)
	}
	byName := map[string]Node{}
	for _, p := range st.Peers {
		byName[p.Label] = p
	}
	if byName["mbp"].Name != "MBP" {
		t.Errorf("label of MBP = %+v", byName["mbp"])
	}
	cases := []struct {
		label string
		owned bool
	}{
		{"mbp", true},
		{"build", false},  // tagged
		{"friend", false}, // another user
	}
	for _, c := range cases {
		if got := st.Owned(byName[c.label]); got != c.owned {
			t.Errorf("Owned(%s) = %v, want %v", c.label, got, c.owned)
		}
	}
	if n, ok := st.ByIP("100.64.0.11"); !ok || n.Label != "mbp" {
		t.Errorf("ByIP(mbp) = %+v, %v", n, ok)
	}
	if n, ok := st.ByIP("fd7a::1"); !ok || n.Label != "devbox" {
		t.Errorf("ByIP(self) = %+v, %v", n, ok)
	}
	if _, ok := st.ByIP("100.9.9.9"); ok {
		t.Error("ByIP found an unknown address")
	}
}

func TestParseStatusNotRunning(t *testing.T) {
	if _, err := parseStatus([]byte(`{"BackendState": "Stopped"}`)); err == nil {
		t.Error("a stopped backend parsed without error")
	}
}

func TestParseStatusTaggedSelfHasNoOwner(t *testing.T) {
	st, err := parseStatus([]byte(`{"BackendState": "Running",
		"Self": {"HostName": "srv", "DNSName": "srv.t.ts.net.", "UserID": 7, "Tags": ["tag:server"]},
		"User": {"7": {"LoginName": "me@example.com"}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if st.Owner != "" {
		t.Errorf("Owner of a tagged device = %q, want none", st.Owner)
	}
}

func TestParseServe(t *testing.T) {
	const out = `{
	  "TCP": {"443": {"HTTPS": true}, "7878": {"HTTPS": true}, "8080": {"HTTP": true}, "9000": {"TCPForward": "127.0.0.1:9"}},
	  "Web": {
	    "box.t.ts.net:443":  {"Handlers": {"/": {"Proxy": "http://127.0.0.1:3001"}, "/x": {"Proxy": "http://127.0.0.1:3002"}}},
	    "box.t.ts.net:7878": {"Handlers": {"/": {"Proxy": "http://127.0.0.1:5555/secret"}}},
	    "box.t.ts.net:8080": {"Handlers": {"/": {"Proxy": "http://127.0.0.1:8081"}}}
	  }
	}`
	cases := []struct {
		port int
		want ServeEntry
	}{
		{7878, ServeEntry{Served: true, Target: "http://127.0.0.1:5555/secret", HTTPS: true}},
		{8080, ServeEntry{Served: true, Target: "http://127.0.0.1:8081"}},
		{443, ServeEntry{Served: true, HTTPS: true}}, // several handlers: not one backend
		{9000, ServeEntry{Served: true}},             // TCP forwarding
		{1234, ServeEntry{}},
	}
	for _, c := range cases {
		got, err := parseServe([]byte(out), "box.t.ts.net", c.port)
		if err != nil {
			t.Fatal(err)
		}
		if got != c.want {
			t.Errorf("port %d: %+v, want %+v", c.port, got, c.want)
		}
	}
	if got, err := parseServe(nil, "box.t.ts.net", 7878); err != nil || got.Served {
		t.Errorf("empty output: %+v, %v", got, err)
	}
}
