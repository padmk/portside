package hostport

import "testing"

func TestHost(t *testing.T) {
	for in, want := range map[string]string{
		"localhost:7878": "localhost",
		"127.0.0.1:7878": "127.0.0.1",
		"web.localhost":  "web.localhost",
		"D-0123456789ABCDEF01234567.LOCALHOST:7878": "d-0123456789abcdef01234567.localhost",
		"Web.LocalHost": "web.localhost",
		"[::1]:7878":    "::1",
		"[::1]":         "::1",
		"::1":           "::1",
		"[FE80::1]:80":  "fe80::1",
	} {
		if got := Host(in); got != want {
			t.Errorf("Host(%q) = %q, want %q", in, got, want)
		}
	}
}
