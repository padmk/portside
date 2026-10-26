package server

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/padmk/portside/internal/config"
	"github.com/padmk/portside/internal/paths"
)

// ── config editing ───────────────────────────────────────────────────────

func (s *Server) getSpec(w http.ResponseWriter, r *http.Request) {
	sv, ok := s.sup.Lookup(r.PathValue("name"))
	if !ok {
		fail(w, http.StatusNotFound, errors.New("unknown server"))
		return
	}
	writeJSON(w, http.StatusOK, config.SpecOf(sv))
}

// edit applies fn to the config file and reloads it. warnings go back with the
// new state; rename (old, new) carries a running server over to its new name.
func (s *Server) edit(w http.ResponseWriter, r *http.Request, fn func() error, warnings []string, rename ...string) {
	s.editMu.Lock()
	err := fn()
	if err == nil {
		err = s.reload(rename...)
	}
	s.editMu.Unlock()
	if errors.Is(err, config.ErrConflict) {
		fail(w, http.StatusConflict, err)
		return
	}
	if err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	state := s.snapshot(r)
	state.Warnings = warnings
	writeJSON(w, http.StatusOK, state)
}

func (s *Server) createServer(w http.ResponseWriter, r *http.Request) {
	var spec config.Spec
	if err := readJSON(r, &spec); err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	s.normalizeSpec(&spec)
	s.edit(w, r, func() error { return config.AddServer(s.opt.ConfigPath, spec) }, directoryWarnings(s.sup.Config().Root, spec.Dir))
}

// updateServer replaces a server's table. A body that names the config
// revision its form was loaded from is refused with 409 when the file changed
// since, so the edit never overwrites a change the form did not show.
func (s *Server) updateServer(w http.ResponseWriter, r *http.Request) {
	var body struct {
		config.Spec
		Revision string `json:"revision"`
	}
	if err := readJSON(r, &body); err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	spec := body.Spec
	s.normalizeSpec(&spec)
	s.edit(w, r, func() error {
		if body.Revision != "" {
			if err := config.CheckRevision(s.opt.ConfigPath, body.Revision); err != nil {
				return err
			}
		}
		return config.UpdateServer(s.opt.ConfigPath, r.PathValue("name"), spec)
	}, directoryWarnings(s.sup.Config().Root, spec.Dir), r.PathValue("name"), spec.Name)
}

func directoryWarnings(root, dir string) []string {
	dir = paths.ExpandHome(dir)
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(root, dir)
	}
	st, err := os.Stat(dir)
	if errors.Is(err, os.ErrNotExist) {
		return []string{fmt.Sprintf("directory %s does not exist", paths.ShortenHome(dir))}
	}
	if err == nil && !st.IsDir() {
		return []string{fmt.Sprintf("%s is not a directory", paths.ShortenHome(dir))}
	}
	return nil
}

// normalizeSpec makes directories under root relative and trims input.
func (s *Server) normalizeSpec(sp *config.Spec) {
	sp.Name = strings.TrimSpace(sp.Name)
	sp.Cmd = strings.TrimSpace(sp.Cmd)
	sp.Dir = strings.TrimSpace(sp.Dir)
	sp.Group = strings.TrimSpace(sp.Group)
	root := s.sup.Config().Root
	abs := paths.ExpandHome(sp.Dir)
	if filepath.IsAbs(abs) {
		sp.Dir = config.SpecDir(root, abs)
	}
}

func (s *Server) deleteServer(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	s.edit(w, r, func() error { return config.RemoveServer(s.opt.ConfigPath, name) }, nil)
}

func (s *Server) setEnabled(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Enabled bool `json:"enabled"`
	}
	if err := readJSON(r, &body); err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	s.edit(w, r, func() error { return config.SetEnabled(s.opt.ConfigPath, r.PathValue("name"), body.Enabled) }, nil)
}

func (s *Server) getConfig(w http.ResponseWriter, r *http.Request) {
	data, err := os.ReadFile(s.opt.ConfigPath)
	if err != nil {
		fail(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"path": s.opt.ConfigPath, "text": string(data), "revision": config.Revision(data)})
}

func (s *Server) putConfig(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Text     string `json:"text"`
		Revision string `json:"revision"`
	}
	if err := readJSON(r, &body); err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	if body.Revision == "" {
		fail(w, http.StatusBadRequest, errors.New("revision is required; reload the config and try again"))
		return
	}
	s.editMu.Lock()
	// Sharing is controlled on this computer only: compared with the file
	// being replaced, under the edit lock, so no concurrent edit slips in.
	if fromPeer(r) && s.changesShare(body.Text) {
		s.editMu.Unlock()
		fail(w, http.StatusForbidden, errors.New("sharing can only be changed on this computer"))
		return
	}
	err := config.ReplaceIfUnchanged(s.opt.ConfigPath, []byte(body.Text), body.Revision)
	if err == nil {
		err = s.reload()
	}
	s.editMu.Unlock()
	if errors.Is(err, config.ErrConflict) {
		fail(w, http.StatusConflict, err)
		return
	}
	if err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, s.snapshot(r))
}

// changesShare reports whether text sets share differently from the file on
// disk (or, when that does not parse, from the applied config).
func (s *Server) changesShare(text string) bool {
	cur := s.sup.Config().Share
	if old, err := config.Load(s.opt.ConfigPath); err == nil {
		cur = old.Share
	}
	next, err := config.Parse(s.opt.ConfigPath, []byte(text))
	return err == nil && next.Share != cur
}
