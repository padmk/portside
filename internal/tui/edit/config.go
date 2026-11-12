package edit

import (
	"context"
	"errors"
	"net/http"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/padmk/portside/internal/client"
	"github.com/padmk/portside/internal/server"
	"github.com/padmk/portside/internal/tui/ui"
)

// configRun is a `:config` in progress: the text and revision the daemon
// served, and the text the editor left after the last try.
type configRun struct {
	seq int
	// gen is the session the run belongs to: the editor's result comes
	// back outside the root's command tagging, so it carries it itself.
	gen      int
	client   *client.Client
	ctx      context.Context // the program's, which ends the editor too
	revision string
	original string
	text     string
}

// configDoc is GET /api/config.
type configDoc struct {
	Path     string `json:"path"`
	Text     string `json:"text"`
	Revision string `json:"revision"`
}

type (
	configFetchedMsg struct {
		seq int
		doc configDoc
		err error
		// overwrite puts the edited text with the new revision instead
		// of opening the editor.
		overwrite bool
	}
	// configEditedMsg is how the editor ended: text is the copy as it
	// left it, read back (and removed) before this message exists.
	configEditedMsg struct {
		seq  int
		text string
		err  error
	}
	configSavedMsg struct {
		seq   int
		state *server.State
		err   error
	}
	configRetryMsg     struct{ seq int }
	configOverwriteMsg struct{ seq int }
)

// launchEditor runs c with the terminal UI suspended; done turns how it
// ended into the message that continues the flow. Tests replace it.
var launchEditor = func(c tea.ExecCommand, done func(error) tea.Msg) tea.Cmd {
	return ui.Exec(c, done)
}

// Config starts `:config`: the config text and revision come from the
// daemon (so it works with --on too), the user's editor runs on a private
// copy, and the result goes back with the original revision. The file on
// disk is never edited here. A new run replaces one whose dialog was
// cancelled. gen is the root's session generation.
func (e *Editor) Config(env *ui.Env, gen int) tea.Cmd {
	removeLeftovers()
	e.seq++
	e.cfg = &configRun{seq: e.seq, gen: gen, client: env.Client, ctx: env.ProgramContext()}
	return fetchConfig(e.cfg, false)
}

func fetchConfig(r *configRun, overwrite bool) tea.Cmd {
	c, seq := r.client, r.seq
	return func() tea.Msg {
		var doc configDoc
		err := request(c, http.MethodGet, "/api/config", nil, &doc)
		return configFetchedMsg{seq: seq, doc: doc, err: err, overwrite: overwrite}
	}
}

// configUpdate continues `:config` with one of its messages.
func (e *Editor) configUpdate(msg tea.Msg) (tea.Cmd, bool) {
	r := e.cfg
	switch msg := msg.(type) {
	case configFetchedMsg:
		if r == nil || msg.seq != r.seq {
			return nil, true
		}
		if msg.err != nil {
			e.cfg = nil
			return notice(ui.LevelError, "config: "+msg.err.Error()), true
		}
		r.revision = msg.doc.Revision
		if msg.overwrite {
			return putConfig(r), true
		}
		r.original, r.text = msg.doc.Text, msg.doc.Text
		return e.openEditor(r), true
	case configEditedMsg:
		if r == nil || msg.seq != r.seq {
			return nil, true
		}
		if msg.err != nil {
			e.cfg = nil
			return notice(ui.LevelError, "config not saved: "+msg.err.Error()), true
		}
		r.text = msg.text
		if r.text == r.original {
			e.cfg = nil
			return notice(ui.LevelInfo, "config unchanged"), true
		}
		return putConfig(r), true
	case configSavedMsg:
		if r == nil || msg.seq != r.seq {
			return nil, true
		}
		return e.configSaved(r, msg), true
	case configRetryMsg:
		if r == nil || msg.seq != r.seq {
			return nil, true
		}
		return e.openEditor(r), true
	case configOverwriteMsg:
		if r == nil || msg.seq != r.seq {
			return nil, true
		}
		return fetchConfig(r, true), true
	}
	return nil, false
}

// openEditor runs the editor on a private copy of the run's text. The
// copy exists only while the editor runs (editCopy), so no path through
// the program, a stale result or a session switch can leave it behind.
func (e *Editor) openEditor(r *configRun) tea.Cmd {
	c := &editCopy{text: r.text, ctx: r.ctx}
	seq, gen := r.seq, r.gen
	return launchEditor(c, func(err error) tea.Msg {
		text, cerr := c.result(err)
		return ui.GenMsg{Gen: gen, Msg: configEditedMsg{seq: seq, text: text, err: cerr}}
	})
}

func putConfig(r *configRun) tea.Cmd {
	c, seq := r.client, r.seq
	body := struct {
		Text     string `json:"text"`
		Revision string `json:"revision"`
	}{r.text, r.revision}
	return func() tea.Msg {
		var st server.State
		err := request(c, http.MethodPut, "/api/config", body, &st)
		if err != nil {
			return configSavedMsg{seq: seq, err: err}
		}
		return configSavedMsg{seq: seq, state: &st}
	}
}

// configSaved ends `:config` after a save, or offers what can follow a
// refusal: a file changed since it was loaded can be overwritten (not the
// default); a config the daemon refuses can be edited again with the text
// kept.
func (e *Editor) configSaved(r *configRun, msg configSavedMsg) tea.Cmd {
	seq := r.seq
	var apiErr *client.APIError
	switch {
	case msg.err == nil:
		e.cfg = nil
		return tea.Batch(stateCmd(msg.state), savedNotice("config saved", msg.state, nil), restartOffer(staleNames(msg.state)))
	case errors.As(msg.err, &apiErr) && apiErr.Status == http.StatusConflict:
		return confirm(ui.ConfirmMsg{
			Title:   "The config changed elsewhere",
			Body:    "The file changed after it was opened here. Overwrite replaces that change with your text; Cancel drops your edits.",
			Confirm: "Overwrite",
			Yes:     func() tea.Msg { return configOverwriteMsg{seq: seq} },
		})
	case errors.As(msg.err, &apiErr) && apiErr.Status == http.StatusBadRequest:
		return confirm(ui.ConfirmMsg{
			Title:   "The config was not saved",
			Body:    withoutPath(apiErr.Msg) + "\n\nEdit again keeps your text; Cancel drops it.",
			Confirm: "Edit again",
			Yes:     func() tea.Msg { return configRetryMsg{seq: seq} },
		})
	}
	e.cfg = nil
	return notice(ui.LevelError, "config not saved: "+msg.err.Error())
}

// withoutPath drops the config file's path the daemon puts before a
// message; the dialog is about that file.
func withoutPath(msg string) string {
	if i := strings.Index(msg, ".toml: "); i >= 0 {
		return msg[i+len(".toml: "):]
	}
	return msg
}
