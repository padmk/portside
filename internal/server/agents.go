package server

import (
	"context"
	"net/http"
	"time"

	"github.com/padmk/portside/internal/agentsetup"
)

// agentsView is the coding-agent setup the dashboard shows: the items of the
// agents installed for this account and the prompt that has an agent do the
// whole setup.
type agentsView struct {
	Items  []agentsetup.Item `json:"items"`
	Prompt string            `json:"prompt"`
}

func (s *Server) agents(w http.ResponseWriter, r *http.Request) {
	items := []agentsetup.Item{}
	for _, it := range agentsetup.Check(s.opt.Port, s.opt.ConfigPath) {
		if it.Applicable {
			items = append(items, it)
		}
	}
	writeJSON(w, http.StatusOK, agentsView{Items: items, Prompt: agentsetup.Prompt()})
}

// applyAgents applies the chosen items, the same as `setup --apply`. A
// failed item is reported in its result, not as an error status.
func (s *Server) applyAgents(w http.ResponseWriter, r *http.Request) {
	var body struct {
		IDs []string `json:"ids"`
	}
	if err := readJSON(r, &body); err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	chosen, err := agentsetup.Select(agentsetup.Check(s.opt.Port, s.opt.ConfigPath), body.IDs)
	if err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	// The agents' CLIs answer in seconds; this bounds one that hangs.
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	writeJSON(w, http.StatusOK, agentsetup.Apply(ctx, chosen))
}
