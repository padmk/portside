package server

import (
	"slices"
	"testing"
	"time"

	"github.com/padmk/portside/internal/docker"
	"github.com/padmk/portside/internal/supervisor"
)

func TestIssueFor(t *testing.T) {
	started := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	before, after := started.Add(-time.Minute), started.Add(time.Minute)
	views := []supervisor.View{
		{Name: "web", Status: supervisor.Running, StartedAt: &started},
		{Name: "docs", Status: supervisor.Stopped},
	}
	code := 1
	cases := []struct {
		name string
		c    docker.Container
		want []string
	}{
		{"healthy", docker.Container{Server: "web", State: "running", Health: "healthy"}, nil},
		{"unhealthy, for every server", docker.Container{Server: "web", SharedWith: []string{"docs"}, State: "running", Health: "unhealthy"}, []string{"web", "docs"}},
		{"restarting", docker.Container{Server: "docs", State: "restarting"}, []string{"docs"}},
		{"failed while the server ran", docker.Container{Server: "web", State: "exited", Failed: true, ExitCode: &code, FinishedAt: &after}, []string{"web"}},
		{"failed before the server started", docker.Container{Server: "web", State: "exited", Failed: true, ExitCode: &code, FinishedAt: &before}, nil},
		{"failed, server stopped", docker.Container{Server: "docs", State: "exited", Failed: true, ExitCode: &code, FinishedAt: &after}, nil},
		{"stopped on purpose", docker.Container{Server: "web", State: "exited", FinishedAt: &after}, nil},
		{"no server", docker.Container{State: "running", Health: "unhealthy"}, nil},
		{"unknown server", docker.Container{Server: "gone", State: "restarting"}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := issueFor(&c.c, views); !slices.Equal(got, c.want) {
				t.Errorf("issueFor = %v, want %v", got, c.want)
			}
		})
	}
}
