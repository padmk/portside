package docker

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// inspect builds an Inspect from the engine's JSON shape.
func inspect(t *testing.T, status string, code int, oom bool, policy string) *Inspect {
	t.Helper()
	raw := map[string]any{
		"Id":         "abc",
		"Name":       "/app-db-1",
		"State":      map[string]any{"Status": status, "ExitCode": code, "OOMKilled": oom},
		"Config":     map[string]any{"Image": "postgres:17-alpine"},
		"HostConfig": map[string]any{"RestartPolicy": map[string]any{"Name": policy}},
	}
	data, _ := json.Marshal(raw)
	var in Inspect
	if err := json.Unmarshal(data, &in); err != nil {
		t.Fatal(err)
	}
	return &in
}

func TestBuildFailed(t *testing.T) {
	cases := []struct {
		name   string
		status string
		code   int
		oom    bool
		policy string
		want   bool
	}{
		{"running", "running", 0, false, "", false},
		{"clean exit", "exited", 0, false, "", false},
		{"ctrl-c", "exited", 130, false, "", false},
		{"sigterm", "exited", 143, false, "", false},
		{"error exit", "exited", 1, false, "", true},
		{"sigkill without policy", "exited", 137, false, "", true},
		{"error exit with on-failure", "exited", 1, false, "on-failure", true},
		{"stopped under unless-stopped", "exited", 137, false, "unless-stopped", false},
		{"stopped under always", "exited", 1, false, "always", false},
		{"oom under always", "exited", 137, true, "always", true},
		{"oom", "exited", 137, true, "", true},
		{"dead", "dead", 0, false, "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := build(apiContainer{ID: "abc", State: c.status}, inspect(t, c.status, c.code, c.oom, c.policy))
			if got.Failed != c.want {
				t.Errorf("Failed = %v, want %v", got.Failed, c.want)
			}
			if c.status == "exited" && (got.ExitCode == nil || *got.ExitCode != c.code) {
				t.Errorf("ExitCode = %v, want %d", got.ExitCode, c.code)
			}
		})
	}
}

func TestServers(t *testing.T) {
	c := Container{Server: "web", SharedWith: []string{"worker"}}
	if got := c.Servers(); len(got) != 2 || got[0] != "web" || got[1] != "worker" {
		t.Errorf("Servers() = %v", got)
	}
	if got := (&Container{SharedWith: []string{"x"}}).Servers(); got != nil {
		t.Errorf("a container without an owner has no servers, got %v", got)
	}
}

func TestStopSignal(t *testing.T) {
	for sig, want := range map[string]bool{"15": true, "9": true, "2": true, "3": true, "SIGTERM": true, "kill": true, "1": false, "HUP": false, "10": false, "": false} {
		if got := stopSignal(sig); got != want {
			t.Errorf("stopSignal(%q) = %v, want %v", sig, got, want)
		}
	}
}

type fakeHost struct{}

func (fakeHost) Environ() []string      { return nil }
func (fakeHost) LookPath(string) string { return "" }

// fakeEngine serves the list and inspect calls of one exited container.
func fakeEngine(t *testing.T, code int, policy string) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/containers/json":
			_ = json.NewEncoder(w).Encode([]map[string]any{{"Id": "abc", "Names": []string{"/app-db-1"}, "State": "exited"}})
		case r.URL.Path == "/containers/abc/json":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"Id": "abc", "Name": "/app-db-1",
				"State":      map[string]any{"Status": "exited", "ExitCode": code},
				"Config":     map[string]any{"Image": "postgres:17-alpine"},
				"HostConfig": map[string]any{"RestartPolicy": map[string]any{"Name": policy}},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return NewClient(Endpoint{Network: "tcp", Address: srv.Listener.Addr().String(), Host: "tcp://" + srv.Listener.Addr().String()})
}

func TestRefreshStopIntent(t *testing.T) {
	cases := []struct {
		name   string
		killed bool
		want   bool
	}{
		{"exit 137 with no stop seen", false, true},
		{"exit 137 after a stop signal", true, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cl := fakeEngine(t, 137, "")
			m := NewMonitor(fakeHost{}, time.Second, func() string { return "" })
			m.client, m.status = cl, Ready
			if c.killed {
				m.killed["abc"] = true
			}
			m.refresh(context.Background(), cl)
			got, ok := m.Find("app-db-1")
			if !ok {
				t.Fatal("container not found after refresh")
			}
			if got.Failed != c.want {
				t.Errorf("Failed = %v, want %v", got.Failed, c.want)
			}
		})
	}
}

func TestBuildRestartFailureStaysFailed(t *testing.T) {
	in := inspect(t, "exited", 1, false, "unless-stopped")
	in.State.Error = "error mounting /data: no such file or directory"
	if got := build(apiContainer{ID: "abc", State: "exited"}, in); !got.Failed {
		t.Error("an exit the engine could not restart must stay failed")
	}
}
