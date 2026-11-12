package containers

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/padmk/portside/internal/client"
	"github.com/padmk/portside/internal/docker"
	"github.com/padmk/portside/internal/tui/ui"
)

const (
	// actionTimeout bounds a start, stop or restart, as the daemon does.
	actionTimeout = 2 * time.Minute
	// jobTimeout bounds a compose job's request: an image build or pull
	// can take long, and the job keeps running in the daemon if the
	// request ends first.
	jobTimeout = 30 * time.Minute
)

// Progressive is a container operation's verb while it runs, for the
// pending mark and "already ..." notices.
func Progressive(id string) string {
	switch id {
	case "start", "start-project":
		return "starting"
	case "stop", "stop-project":
		return "stopping"
	case "restart":
		return "restarting"
	case "recreate", "rebuild", "pull":
		return "recreating"
	case "down-project":
		return "removing"
	case "dismiss-job":
		return "dismissing"
	}
	return id
}

// Request runs action id on item it as operation op and reports it with
// ui.OpDoneMsg when the request returns: start, stop and restart on a
// container, recreate, rebuild and pull through its compose service
// (waiting for the job's end), the project actions through the project's
// first compose container (ref), and dismiss-job. ctx is the program's
// lifetime, which ends a waiting request.
func Request(ctx context.Context, c *client.Client, op int, id string, it ui.Selection, ref string) tea.Cmd {
	return func() tea.Msg {
		done := ui.OpDoneMsg{ID: op, Items: []ui.Selection{it}}
		name := it.Name
		path := "/api/containers/" + url.PathEscape(ref)
		switch id {
		case "start", "stop", "restart":
			done.Label = id + " " + name
			rctx, cancel := context.WithTimeout(ctx, actionTimeout)
			defer cancel()
			done.Err = c.Do(rctx, http.MethodPost, path+"/"+id, struct{}{}, nil)
		case "recreate", "rebuild", "pull":
			done.Label = id + " " + name
			rctx, cancel := context.WithTimeout(ctx, jobTimeout)
			defer cancel()
			_, done.Err = c.ContainerAction(rctx, ref, "recreate", id == "rebuild", id == "pull")
		case "start-project", "stop-project", "down-project":
			action := map[string]string{"start-project": "start", "stop-project": "stop", "down-project": "down"}[id]
			done.Label = map[string]string{"start": "start", "stop": "stop", "down": "remove the containers of"}[action] + " project " + name
			rctx, cancel := context.WithTimeout(ctx, jobTimeout)
			defer cancel()
			var res docker.JobResult
			done.Err = c.Do(rctx, http.MethodPost, path+"/project/"+action, map[string]bool{"confirm": action == "down"}, &res)
			if done.Err == nil && res.Error != "" {
				done.Err = errors.New(res.Error)
			}
		case "dismiss-job":
			done.Label = "dismiss the job of " + name
			rctx, cancel := context.WithTimeout(ctx, actionTimeout)
			defer cancel()
			done.Err = c.Do(rctx, http.MethodDelete, path+"/job", nil, nil)
		default:
			done.Err = errors.New("unknown container action " + id)
		}
		return done
	}
}

// ConfirmDown asks before removing a project's containers; yes runs it.
func ConfirmDown(project string, yes tea.Cmd) tea.Cmd {
	return func() tea.Msg {
		return ui.ConfirmMsg{
			Title:   "Remove containers",
			Body:    "Stop and remove all containers in " + project + "? Volumes are kept. Anonymous volumes will not be reattached automatically.",
			Confirm: "Remove",
			Yes:     yes,
		}
	}
}

// StartEngine asks the daemon to start the container engine; it answers
// once the start command runs, and the snapshot says when the engine is
// up.
func StartEngine(c *client.Client) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), actionTimeout)
		defer cancel()
		if err := c.Do(ctx, http.MethodPost, "/api/docker/start", struct{}{}, nil); err != nil {
			return ui.NoticeMsg{Text: "start Docker: " + err.Error(), Level: ui.LevelError}
		}
		return ui.NoticeMsg{Text: "starting the container engine" + "; the containers pane shows when it answers", Level: ui.LevelInfo}
	}
}
