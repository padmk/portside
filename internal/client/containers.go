package client

import (
	"context"
	"errors"
	"net/http"
	"net/url"

	"github.com/padmk/portside/internal/docker"
)

// ContainerAction uses the daemon's engine and compose context. Recreate waits
// until its job ends and returns a bounded output tail, including on failure.
func (c *Client) ContainerAction(ctx context.Context, name, action string, build, pull bool) (docker.JobResult, error) {
	var result docker.JobResult
	if build && pull {
		return result, errors.New("choose either build or pull")
	}
	switch action {
	case "start", "stop", "restart":
		if build || pull {
			return result, errors.New("build and pull apply only to recreate")
		}
	case "recreate":
	default:
		return result, errors.New("action must be start, stop, restart or recreate")
	}
	var out any
	if action == "recreate" {
		out = &result
	}
	err := c.Do(ctx, http.MethodPost, "/api/containers/"+url.PathEscape(name)+"/"+action, map[string]bool{"build": build, "pull": pull, "wait": true}, out)
	if err == nil && result.Error != "" {
		err = errors.New(result.Error)
	}
	return result, err
}
