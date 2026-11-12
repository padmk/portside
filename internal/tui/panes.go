package tui

import (
	"github.com/padmk/portside/internal/tui/containers"
	"github.com/padmk/portside/internal/tui/detail"
	"github.com/padmk/portside/internal/tui/keymap"
	"github.com/padmk/portside/internal/tui/logs"
	"github.com/padmk/portside/internal/tui/ports"
	"github.com/padmk/portside/internal/tui/servers"
	"github.com/padmk/portside/internal/tui/ui"
)

// newPanes creates the left column's panes, top to bottom, and the right
// pane: the servers first, which always show; the root model shows the
// containers and the ports when they belong on screen (syncLeft). The
// right pane shows the selected server, or the selected container.
func newPanes() (left []ui.Pane, right ui.Pane) {
	return []ui.Pane{servers.New(), containers.New(), ports.New()}, containers.NewMain(detail.New(logs.New()))
}

// paneActions are the actions of each pane scope that the pane's Do
// carries out (servers.Pane.Do; detail.Pane.Do, which passes the
// Logs tab's to logs.Pane.Do). With targetActions and globals they cover
// every action of the registry, which model_test.go checks; a pane that
// handles a new action lists it here.
var paneActions = map[keymap.Scope][]string{
	keymap.ScopeServers: {"down", "up", "next-page", "prev-page", "top", "bottom", "collapse-all", "expand-all", "clear-filter", "filter", "open-detail"},
	keymap.ScopeLogs: {"down", "up", "next-page", "prev-page", "top", "bottom", "search", "next-match", "prev-match", "keep-matching",
		"follow", "wrap", "timestamps", "visual", "select-down", "select-up", "yank", "scroll-left", "scroll-right", "attach", "clear-logs", "back"},
	keymap.ScopeDetail: {"down", "up", "next-page", "prev-page", "top", "bottom", "copy", "copy-dir", "back"},
	// containers.Pane.Do, which sends the container actions to the root
	// as ui.ActMsg; containers.Detail.Do on its Shell tab.
	keymap.ScopeContainers: {"down", "up", "next-page", "prev-page", "top", "bottom", "open-detail",
		"toggle", "restart", "start", "stop", "shell", "open", "copy-url", "recreate", "rebuild", "pull",
		"start-project", "stop-project", "down-project", "dismiss-job"},
	// ports.Pane.Do, likewise.
	keymap.ScopePorts: {"down", "up", "next-page", "prev-page", "top", "bottom", "kill", "takeover", "open", "copy-url",
		"open-detail", "filter", "back"},
}
