package edit

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/padmk/portside/internal/app"
	"github.com/padmk/portside/internal/client"
	"github.com/padmk/portside/internal/config"
	"github.com/padmk/portside/internal/discover"
	"github.com/padmk/portside/internal/server"
	"github.com/padmk/portside/internal/signing"
	"github.com/padmk/portside/internal/supervisor"
	"github.com/padmk/portside/internal/tui/form"
	"github.com/padmk/portside/internal/tui/theme"
	"github.com/padmk/portside/internal/tui/ui"
)

func ptr[T any](v T) *T { return &v }

func TestSpecFieldsRoundTrip(t *testing.T) {
	full := config.Spec{
		Name: "web", Dir: "apps/web", Cmd: "pnpm dev --port {PORT}", Oneshot: false, Port: ptr(3000), Group: "shop",
		DependsOn: []string{"db", "migrate"}, Projects: []string{"shop", "shop_test"},
		Env:     map[string]string{"API_TOKEN": "s3cret", "NODE_ENV": "development", "URL": "http://x/?a=b=c"},
		Restart: "always", Health: "/healthz", ReadyLog: "ready in  ", ReadyAfter: "5s", Icon: "icon.png", Open: "/admin",
		Stop: "30s", Enabled: ptr(false), LAN: true, Schedule: "0 9 * * mon-fri", Watch: []string{"src/**/*.ts", "package.json"},
		Wrap: "nix develop -c", OnCrash: ptr(""), OnUnready: ptr("notify-send down"), UnreadyAfter: "2m", RetryAfter: "5m",
		Umask: "0007", MemoryLimit: "2GB", Comment: "the shop's front end",
	}
	for _, sp := range []config.Spec{full, {}, {Name: "task", Cmd: "make migrate", Oneshot: true, Port: ptr(0), Enabled: ptr(true)}} {
		got, err := specFrom(form.New("t", specFields(sp, false)), sp)
		if err != nil {
			t.Fatalf("%s: %v", sp.Name, err)
		}
		if !reflect.DeepEqual(got, sp) {
			t.Errorf("round trip of %q:\n got %#v\nwant %#v", sp.Name, got, sp)
		}
	}
}

func TestSpecFromReadsTheEditedValues(t *testing.T) {
	f := form.New("t", specFields(config.Spec{Name: "web", Cmd: "x"}, true))
	f.Field("port").Value = ""
	f.Field("env").Value = "A=1\n\n B = two \n"
	f.Field("enabled").On = false
	f.Field("depends_on").Value = "db, ,cache"
	sp, err := specFrom(f, config.Spec{})
	if err != nil {
		t.Fatal(err)
	}
	if sp.Port != nil || !reflect.DeepEqual(sp.Env, map[string]string{"A": "1", "B": " two "}) ||
		sp.Enabled == nil || *sp.Enabled || !reflect.DeepEqual(sp.DependsOn, []string{"db", "cache"}) {
		t.Errorf("spec %#v", sp)
	}
	f.Field("env").Value = "A=1\nnot a variable"
	if _, err := specFrom(f, config.Spec{}); err == nil || err.(fieldError).key != "env" {
		t.Errorf("a line without = gives %v, want an error under env", err)
	}
}

func TestSettingsFieldsRoundTrip(t *testing.T) {
	v := config.Settings{Root: "~/dev", Port: 7900, Path: []string{"~/bin", "/opt/bin"}, Env: map[string]string{"A": "1"},
		Restore: false, Notify: true, StopTimeout: "20s", Shell: "zsh", DockerStart: "colima start", OnCrash: "say crash",
		OnUnready: "x", OnRecover: "y", UnreadyAfter: "3m", RetryAfter: "5m", MemoryLimit: "1GB", LoginEnv: false, Umask: "0022"}
	for _, s := range []config.Settings{v, {Restore: true, Notify: true, LoginEnv: true}} {
		v := settingsView{Values: s, Defaults: config.DefaultSettings("/x/c.toml")}
		got, err := settingsFrom(form.New("t", settingsFields(v)), v)
		if err != nil || !reflect.DeepEqual(got, s) {
			t.Errorf("round trip:\n got %#v, %v\nwant %#v", got, err, s)
		}
	}
}

// exact are values a line per variable or a trimmed text would change.
var exact = map[string]string{
	"TRAILING": "keep trailing spaces  ", "EMPTY": "", "LEADING": "  lead", "EQUALS": "a=b==c ",
	"MULTI": "line one\nline two\r\n", "TAB": "\tx\t",
}

func TestUntouchedFormsKeepExactValues(t *testing.T) {
	sp := config.Spec{Name: "web", Cmd: "printf 'a  b'  ", Env: exact, ReadyLog: "ready  ", Health: " /h "}
	f := form.New("t", specFields(sp, false))
	if env := f.Field("env"); env.ReadOnly == "" || strings.Contains(env.Value, "line one\n") {
		t.Errorf("a multi-line value: field %q read-only %q, want it escaped and read-only", env.Value, env.ReadOnly)
	}
	// Another field changes; every untouched key goes back as loaded.
	f.Field("group").Value = "shop"
	got, err := specFrom(f, sp)
	want := sp
	want.Group = "shop"
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("spec:\n got %#v, %v\nwant %#v", got, err, want)
	}

	v := settingsView{Values: config.Settings{Shell: "zsh ", Env: exact, OnCrash: "say  crash "}}
	sf := form.New("t", settingsFields(v))
	sf.Field("notify").On = true
	s, err := settingsFrom(sf, v)
	wantS := v.Values
	wantS.Notify = true
	if err != nil || !reflect.DeepEqual(s, wantS) {
		t.Errorf("settings:\n got %#v, %v\nwant %#v", s, err, wantS)
	}
}

func TestEditedEnvKeepsValuesRaw(t *testing.T) {
	sp := config.Spec{Name: "web", Cmd: "x", Env: map[string]string{"A": "1  ", "B": "x=y"}}
	f := form.New("t", specFields(sp, false))
	f.Field("env").Value += "\n C = two  \nD=\n\n"
	got, err := specFrom(f, sp)
	want := map[string]string{"A": "1  ", "B": "x=y", "C": " two  ", "D": ""}
	if err != nil || !reflect.DeepEqual(got.Env, want) {
		t.Errorf("env %#v, %v, want %#v", got.Env, err, want)
	}
}

func TestProposalSpec(t *testing.T) {
	for _, c := range []struct {
		in   discover.Suggestion
		port *int
	}{
		{discover.Suggestion{Name: "web", Cmd: "pnpm dev", Port: 3001, StopAfter: "15s"}, ptr(3001)},
		{discover.Suggestion{Name: "worker", Cmd: "pnpm worker", NoPort: true}, ptr(0)},
		{discover.Suggestion{Name: "site", Cmd: "hugo server"}, nil},
	} {
		sp := proposalSpec(server.DiscoverResult{Suggestion: c.in, DirConfig: "apps/" + c.in.Name, DirDisplay: "~/dev/apps/" + c.in.Name})
		want := config.Spec{Name: c.in.Name, Dir: "apps/" + c.in.Name, Cmd: c.in.Cmd, Port: c.port, Stop: c.in.StopAfter}
		if !reflect.DeepEqual(sp, want) {
			t.Errorf("%s: %#v, want %#v", c.in.Name, sp, want)
		}
	}
}

// fakeDaemon serves GET and PUT /api/config with proven replies; put
// decides each PUT's status and error.
type fakeDaemon struct {
	mu   sync.Mutex
	text string
	rev  string
	puts []string // "revision|text" of each PUT
	put  func(text, rev string) (int, string)
	// freePort answers GET /api/free-port; adds records each POST
	// /api/servers and refuse its error by name.
	freePort int
	adds     []config.Spec
	refuse   map[string]string
}

func (f *fakeDaemon) handler(token, epoch string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reply := func(status int, v any) {
			body, _ := json.Marshal(v)
			signing.ProveReply(w.Header(), r.Header, token, epoch, status, body)
			w.WriteHeader(status)
			_, _ = w.Write(body)
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		switch r.Method + " " + r.URL.Path {
		case "GET /api/health":
			reply(http.StatusOK, client.Health{App: app.ID, Version: "test", PID: 1})
		case "GET /api/config":
			reply(http.StatusOK, configDoc{Path: "/x/c.toml", Text: f.text, Revision: f.rev})
		case "PUT /api/config":
			var body struct{ Text, Revision string }
			data, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(data, &body)
			f.puts = append(f.puts, body.Revision+"|"+body.Text)
			if status, msg := f.put(body.Text, body.Revision); status != http.StatusOK {
				reply(status, map[string]string{"error": msg})
				return
			}
			f.text, f.rev = body.Text, "r"+strconv.Itoa(len(f.puts)+1)
			reply(http.StatusOK, server.State{Servers: []supervisor.View{{Name: "web", Status: supervisor.Running, Stale: true}}})
		case "GET /api/free-port":
			reply(http.StatusOK, map[string]int{"port": f.freePort})
		case "POST /api/servers":
			var sp config.Spec
			data, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(data, &sp)
			if msg := f.refuse[sp.Name]; msg != "" {
				reply(http.StatusBadRequest, map[string]string{"error": msg})
				return
			}
			f.adds = append(f.adds, sp)
			reply(http.StatusOK, server.State{})
		default:
			reply(http.StatusNotFound, map[string]string{"error": "no such endpoint"})
		}
	})
}

func testEnv(t *testing.T, f *fakeDaemon) *ui.Env {
	t.Helper()
	state := t.TempDir()
	t.Setenv(app.Env("STATE_DIR"), state)
	token := signing.RandomHex(32)
	if err := os.WriteFile(filepath.Join(state, "auth-token"), []byte(token+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(f.handler(token, signing.NewEpoch()))
	t.Cleanup(srv.Close)
	_, port, _ := net.SplitHostPort(srv.Listener.Addr().String())
	n, _ := strconv.Atoi(port)
	return &ui.Env{Client: client.New(n), Styles: theme.New(theme.TerminalFamily.Variant(true)), Glyphs: theme.Unicode}
}

// fakeEditor stands for the user's editor: edit changes the file's text;
// it records each file it was given and what that file held.
type fakeEditor struct {
	edit  func(string) string
	files []string
	seen  []string
}

func (fe *fakeEditor) install(t *testing.T) {
	oldLaunch, oldRun := launchEditor, runEditor
	t.Cleanup(func() { launchEditor, runEditor = oldLaunch, oldRun })
	launchEditor = func(c tea.ExecCommand, done func(error) tea.Msg) tea.Cmd {
		return func() tea.Msg { return done(c.Run()) }
	}
	runEditor = func(_ context.Context, file string, _ io.Reader, _, _ io.Writer) error {
		data, err := os.ReadFile(file)
		if err != nil {
			return err
		}
		if st, _ := os.Stat(file); st.Mode().Perm() != 0o600 {
			return os.ErrPermission
		}
		fe.files, fe.seen = append(fe.files, file), append(fe.seen, string(data))
		return os.WriteFile(file, []byte(fe.edit(string(data))), 0o600)
	}
}

// run feeds the editor the messages of cmd as the program would, until a
// message is not the editor's; it returns those messages (notices,
// confirmations, states).
func run(e *Editor, env *ui.Env, cmd tea.Cmd) []tea.Msg {
	var out []tea.Msg
	queue := []tea.Cmd{cmd}
	for len(queue) > 0 {
		c := queue[0]
		queue = queue[1:]
		if c == nil {
			continue
		}
		msg := c()
		if g, ok := msg.(ui.GenMsg); ok {
			msg = g.Msg
		}
		if batch, ok := msg.(tea.BatchMsg); ok {
			queue = append(queue, batch...)
			continue
		}
		if next, ok := e.Update(env, msg); ok {
			queue = append(queue, next)
			continue
		}
		out = append(out, msg)
	}
	return out
}

func noticeOf(t *testing.T, msgs []tea.Msg) ui.NoticeMsg {
	t.Helper()
	for _, m := range msgs {
		if n, ok := m.(ui.NoticeMsg); ok {
			return n
		}
	}
	t.Fatalf("no notice in %#v", msgs)
	return ui.NoticeMsg{}
}

func confirmOf(t *testing.T, msgs []tea.Msg) ui.ConfirmMsg {
	t.Helper()
	for _, m := range msgs {
		if c, ok := m.(ui.ConfirmMsg); ok {
			return c
		}
	}
	t.Fatalf("no confirmation in %#v", msgs)
	return ui.ConfirmMsg{}
}

const added = "\n[servers.added]\ncmd = \"sleep 1000\"\n"

func TestConfigSavesWithTheOriginalRevision(t *testing.T) {
	f := &fakeDaemon{text: "root = \".\"\n", rev: "r1", put: func(string, string) (int, string) { return http.StatusOK, "" }}
	env := testEnv(t, f)
	ed := &fakeEditor{edit: func(s string) string { return s + added }}
	ed.install(t)
	var e Editor
	msgs := run(&e, env, e.Config(env, 0))
	if len(f.puts) != 1 || f.puts[0] != "r1|root = \".\"\n"+added {
		t.Fatalf("PUTs %q, want the edited text with r1", f.puts)
	}
	n := noticeOf(t, msgs)
	if n.Level != ui.LevelSuccess || !strings.Contains(n.Text, "config saved") || !strings.Contains(n.Text, "web runs with the old settings") {
		t.Errorf("notice %+v, want saved and web named as stale", n)
	}
	// The running server with old settings gets one restart offer.
	if c := confirmOf(t, msgs); c.Title != "Restart web?" || c.Yes == nil {
		t.Errorf("restart offer %+v", c)
	} else if r, ok := c.Yes().(ui.RunMsg); !ok || r.Action != "restart" || r.Item != ui.ServerItem("web") {
		t.Errorf("the offer runs %+v", r)
	}
	if _, err := os.Stat(ed.files[0]); !os.IsNotExist(err) {
		t.Errorf("the temporary copy %s is left behind (%v)", ed.files[0], err)
	}
	// Leaving the editor without a change sends nothing.
	ed.edit = func(s string) string { return s }
	if n := noticeOf(t, run(&e, env, e.Config(env, 0))); n.Text != "config unchanged" || len(f.puts) != 1 {
		t.Errorf("unchanged: notice %q, %d PUTs", n.Text, len(f.puts))
	}
}

func TestConfigRefusedKeepsTheTextForAnotherTry(t *testing.T) {
	refuse := true
	f := &fakeDaemon{text: "a = 1\n", rev: "r1", put: func(string, string) (int, string) {
		if refuse {
			return http.StatusBadRequest, "/x/c.toml: [servers.added]: cmd is required"
		}
		return http.StatusOK, ""
	}}
	env := testEnv(t, f)
	ed := &fakeEditor{edit: func(s string) string { return s + "x" }}
	ed.install(t)
	var e Editor
	c := confirmOf(t, run(&e, env, e.Config(env, 0)))
	if !strings.Contains(c.Body, "cmd is required") || c.Confirm != "Edit again" {
		t.Fatalf("refusal dialog %+v", c)
	}
	refuse = false
	msgs := run(&e, env, c.Yes)
	if len(ed.seen) != 2 || ed.seen[1] != "a = 1\nx" {
		t.Errorf("the second try opened %q, want the first try's text", ed.seen)
	}
	if n := noticeOf(t, msgs); n.Level != ui.LevelSuccess || f.puts[1] != "r1|a = 1\nxx" {
		t.Errorf("after the retry: %+v, PUTs %q", n, f.puts)
	}
}

func TestConfigConflictOverwritesOnlyWhenConfirmed(t *testing.T) {
	f := &fakeDaemon{text: "a = 1\n", rev: "r1"}
	f.put = func(_, rev string) (int, string) {
		if rev != f.rev {
			return http.StatusConflict, config.ErrConflict.Error()
		}
		return http.StatusOK, ""
	}
	env := testEnv(t, f)
	ed := &fakeEditor{edit: func(s string) string {
		// Someone else saves while the editor is open.
		f.rev, f.text = "r9", "b = 2\n"
		return "mine = 1\n"
	}}
	ed.install(t)
	var e Editor
	c := confirmOf(t, run(&e, env, e.Config(env, 0)))
	if c.Confirm != "Overwrite" || len(f.puts) != 1 || f.text != "b = 2\n" {
		t.Fatalf("conflict: dialog %+v, PUTs %q, file %q", c, f.puts, f.text)
	}
	ed.edit = func(s string) string { return s }
	msgs := run(&e, env, c.Yes)
	if f.text != "mine = 1\n" || f.puts[1] != "r9|mine = 1\n" || len(ed.files) != 1 {
		t.Errorf("overwrite: file %q, PUTs %q, editor runs %d", f.text, f.puts, len(ed.files))
	}
	noticeOf(t, msgs)
}

func TestNewServerFormSuggestsAFreePort(t *testing.T) {
	f := &fakeDaemon{freePort: 3007}
	env := testEnv(t, f)
	var e Editor
	e.picker = &picker{items: []server.DiscoverResult{proposal(discover.Suggestion{Name: "app", Cmd: "npm run dev"}), proposal(discover.Suggestion{Name: "job", Cmd: "make", NoPort: true})}}
	run(&e, env, e.Key(env, tea.KeyPressMsg{Code: tea.KeyEnter}))
	sh := e.sheet
	if sh == nil || sh.form.Value("port") != "3007" || sh.form.Dirty() {
		t.Fatalf("the form of a proposal without a port: %+v", sh)
	}
	// Cleared, the port is automatic: the key is left out.
	sh.form.Field("port").Value = ""
	if sp, _ := specFrom(sh.form, sh.base); sp.Port != nil {
		t.Errorf("an empty port sends %d", *sp.Port)
	}
	// A proposal with no port keeps 0 and asks for none.
	e.close()
	e.picker = &picker{items: []server.DiscoverResult{proposal(discover.Suggestion{Name: "job", Cmd: "make", NoPort: true})}}
	if cmd := e.Key(env, tea.KeyPressMsg{Code: tea.KeyEnter}); cmd != nil || e.sheet.form.Value("port") != "0" {
		t.Errorf("a proposal without a port: port %q, request %v", e.sheet.form.Value("port"), cmd != nil)
	}
}

func TestMarkedProposalsAreAddedAtOnce(t *testing.T) {
	f := &fakeDaemon{refuse: map[string]string{"api": "a server named api exists"}}
	env := testEnv(t, f)
	var e Editor
	e.picker = &picker{items: []server.DiscoverResult{proposal(discover.Suggestion{Name: "web", Cmd: "a"}), proposal(discover.Suggestion{Name: "api", Cmd: "b"}), proposal(discover.Suggestion{Name: "docs", Cmd: "c", Port: 4000})}}
	space := tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}
	for range 3 {
		e.Key(env, space)
	}
	msgs := run(&e, env, e.Key(env, tea.KeyPressMsg{Code: tea.KeyEnter}))
	if len(f.adds) != 2 || f.adds[0].Name != "web" || f.adds[1].Name != "docs" || *f.adds[1].Port != 4000 {
		t.Fatalf("added %+v", f.adds)
	}
	p := e.picker
	if p == nil || len(p.items) != 1 || p.items[0].Name != "api" || !p.marked["api"] || p.errs["api"] != "a server named api exists" {
		t.Fatalf("the refused proposal does not stay with its error: %+v", p)
	}
	if n := noticeOf(t, msgs); !strings.Contains(n.Text, "added web and docs; api not added") {
		t.Errorf("notice %q", n.Text)
	}
	// Once all went in, the list closes.
	delete(f.refuse, "api")
	run(&e, env, e.Key(env, tea.KeyPressMsg{Code: tea.KeyEnter}))
	if e.picker != nil || len(f.adds) != 3 {
		t.Errorf("after adding the rest: picker %+v, %d added", e.picker, len(f.adds))
	}
}

func proposal(sg discover.Suggestion) server.DiscoverResult {
	return server.DiscoverResult{Suggestion: sg}
}

func TestConfigCopyIsGoneWhateverHappensToTheResult(t *testing.T) {
	f := &fakeDaemon{text: "a = 1\n", rev: "r1", put: func(string, string) (int, string) { return http.StatusOK, "" }}
	env := testEnv(t, f)
	ed := &fakeEditor{edit: func(s string) string { return s + "b = 2\n" }}
	ed.install(t)
	var e Editor
	fetched := e.Config(env, 7)()
	open, _ := e.Update(env, fetched)
	// A new :config replaces the run while the editor is open; its result
	// comes late, tagged with the session, and is dropped.
	e.Config(env, 7)
	msg := open()
	g, ok := msg.(ui.GenMsg)
	if !ok || g.Gen != 7 {
		t.Fatalf("the editor's result %#v is not tagged with session 7", msg)
	}
	if len(ed.files) != 1 {
		t.Fatalf("editor runs %d", len(ed.files))
	}
	if _, err := os.Stat(filepath.Dir(ed.files[0])); !os.IsNotExist(err) {
		t.Errorf("the copy's directory is left behind (%v)", err)
	}
	if next, handled := e.Update(env, g.Msg); !handled || next != nil || len(f.puts) != 0 {
		t.Errorf("a stale result was acted on: %v, %v, PUTs %q", handled, next, f.puts)
	}
	// The editor failing removes the copy too.
	runEditor = func(_ context.Context, file string, _ io.Reader, _, _ io.Writer) error {
		ed.files = append(ed.files, file)
		return os.ErrInvalid
	}
	if n := noticeOf(t, run(&e, env, e.Config(env, 7))); n.Level != ui.LevelError || !strings.Contains(n.Text, "editor") {
		t.Errorf("notice %+v", n)
	}
	if _, err := os.Stat(filepath.Dir(ed.files[1])); !os.IsNotExist(err) {
		t.Errorf("a failed editor leaves the copy behind (%v)", err)
	}
}

func TestLeftoverCopiesOfEndedProcessesAreRemoved(t *testing.T) {
	dir := t.TempDir()
	ended := exec.Command("true")
	if err := ended.Run(); err != nil {
		t.Skip(err)
	}
	names := map[string]bool{
		copyPrefix + strconv.Itoa(ended.Process.Pid) + "-a": false,
		copyPrefix + strconv.Itoa(os.Getpid()) + "-b":       true,
		copyPrefix + strconv.Itoa(os.Getppid()) + "-c":      true,
		"other-" + strconv.Itoa(ended.Process.Pid) + "-d":   true,
	}
	for n := range names {
		if err := os.MkdirAll(filepath.Join(dir, n), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	removeLeftoversIn(dir)
	for n, keep := range names {
		if _, err := os.Stat(filepath.Join(dir, n)); (err == nil) != keep {
			t.Errorf("%s: kept %v, want %v", n, err == nil, keep)
		}
	}
}
