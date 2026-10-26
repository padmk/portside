package supervisor

import (
	"testing"

	"github.com/padmk/portside/internal/config"
)

func TestViewHealth(t *testing.T) {
	tests := []struct {
		name string
		view View
		want Health
	}{
		{"running", View{Status: Running, Wanted: true, Restart: config.RestartNo}, Health{Expected: true, Ready: true}},
		{"crashed", View{Status: Crashed, Note: "exited with code 1", Restart: config.RestartNo}, Health{Expected: true, Reason: "exited with code 1"}},
		{"crashed without a note", View{Status: Crashed, Restart: config.RestartOnFailure}, Health{Expected: true, Reason: "crashed"}},
		{"crashed, restart pending", View{Status: Crashed, Wanted: true, RestartPending: true, Note: "exited with code 1, restarting in 2s", Restart: config.RestartOnFailure}, Health{Expected: true, Reason: "exited with code 1, restarting in 2s"}},
		{"exited, always gave up", View{Status: Exited, Note: "exited on its own", Restart: config.RestartAlways}, Health{Expected: true, Reason: "exited on its own"}},
		{"exited cleanly, restart no", View{Status: Exited, Note: "exited on its own", Restart: config.RestartNo}, Health{}},
		{"exited cleanly, on-failure", View{Status: Exited, Note: "exited on its own", Restart: config.RestartOnFailure}, Health{}},
		{"starting", View{Status: Starting, Wanted: true, Restart: config.RestartNo}, Health{Expected: true, Reason: "starting"}},
		{"blocked", View{Status: Blocked, Wanted: true, Note: "port 3000 is in use", Restart: config.RestartNo}, Health{Expected: true, Reason: "port 3000 is in use"}},
		{"stopped by the user", View{Status: Stopped, Restart: config.RestartAlways}, Health{}},
		{"disabled, never started", View{Status: Stopped, Restart: config.RestartNo}, Health{}},
		{"temporary copy crashed", View{Status: Crashed, Temporary: true, Note: "exited with code 2", Restart: config.RestartNo}, Health{Expected: true, Reason: "exited with code 2"}},
		{"temporary copy running", View{Status: Running, Temporary: true, Wanted: true, Restart: config.RestartNo}, Health{Expected: true, Ready: true}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.view.Health()
			if got != tt.want {
				t.Errorf("Health() = %+v, want %+v", got, tt.want)
			}
			if got.Degraded() != (tt.want.Expected && !tt.want.Ready) {
				t.Errorf("Degraded() = %v", got.Degraded())
			}
		})
	}
}
