package containers

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/padmk/portside/internal/client"
	"github.com/padmk/portside/internal/docker"
	"github.com/padmk/portside/internal/server"
	"github.com/padmk/portside/internal/tui/detail"
	"github.com/padmk/portside/internal/tui/keymap"
	"github.com/padmk/portside/internal/tui/logs"
	"github.com/padmk/portside/internal/tui/ui"
	"github.com/padmk/portside/internal/viewfmt"
)

// The right pane's tabs for a container; Job shows only while the
// container has a compose job.
const (
	tabLogs = iota
	tabDetails
	tabShell
	tabJob
)

// tabNames are the tabs' titles, tabShort their abbreviations for frames
// too narrow for the titles (detail.Strip).
var (
	tabNames = []string{"Logs", "Details", "Shell", "Job"}
	tabShort = []string{"Log", "Det", "Sh", "Job"}
)

// detailsMsg carries a container's inspection for the Details tab, from
// the fetch numbered gen.
type detailsMsg struct {
	gen int
	id  string
	d   server.ContainerDetails
	err error
}

// Detail is the right pane while a container is selected: its Logs,
// Details and Shell tabs and, while it has one, its compose job's output,
// with its state in the top border.
type Detail struct {
	logs, job *logs.Pane
	tab       int
	// wantJob opens the Job tab when the job shows (ShowJob).
	wantJob bool
	// shown is the container shown (its id) and jobShown the job whose
	// output the Job tab streams (its start time).
	shown, jobShown string
	// jobSettled is set once the ended job's stream was told to close.
	jobSettled bool

	inspected  server.ContainerDetails
	detailsFor string // the id the details are of
	detailsErr string
	gen        int

	// facts is the Details tab, laid out and navigated as a server's;
	// strip is the top border with the tabs.
	facts detail.Facts
	strip detail.Strip
}

// NewDetail returns the container pane, without a container.
func NewDetail() *Detail { return &Detail{logs: logs.New(), job: logs.New()} }

// Close ends the streams.
func (d *Detail) Close() {
	d.logs.Close()
	d.job.Close()
}

// tabs are the tabs the container has.
func (d *Detail) tabs(c docker.Container, ok bool) int {
	if ok && c.Job != nil {
		return 4
	}
	return 3
}

func (d *Detail) Scope() keymap.Scope {
	switch d.tab {
	case tabLogs, tabJob:
		return keymap.ScopeLogs
	case tabShell:
		// The container's own keys: a opens the shell, r restarts.
		return keymap.ScopeContainers
	}
	return keymap.ScopeDetail
}

// container is the selected container in the latest snapshot.
func (d *Detail) container(env *ui.Env) (docker.Container, bool) {
	return Find(env.State, env.Selected)
}

// sync follows the selection, in Update and Do: a new container gets its
// log stream and its details, a new job its output.
func (d *Detail) sync(env *ui.Env) tea.Cmd {
	c, ok := d.container(env)
	var cmds []tea.Cmd
	name := ""
	if ok {
		name = c.Name
	}
	if !ok || c.ID != d.shown {
		d.shown = ""
		if ok {
			d.shown = c.ID
		}
		// The open tab stays: a recreated container (a new id) keeps its
		// Job tab while the job shows, and one without a job falls back to
		// Logs below.
		d.facts.Reset()
		cmds = append(cmds, d.logs.ShowContainer(env, name))
	}
	if d.wantJob && ok && c.Job != nil && c.Job.Running {
		d.tab, d.wantJob = tabJob, false
	}
	if d.tab >= d.tabs(c, ok) {
		d.tab = tabLogs
	}
	job := ""
	if ok && c.Job != nil {
		job = c.Job.StartedAt.String()
	}
	if job != d.jobShown {
		d.jobShown, d.jobSettled = job, false
		if job == "" {
			cmds = append(cmds, d.job.ShowJob(env, "", ""))
		} else {
			cmds = append(cmds, d.job.ShowJob(env, name, job))
		}
	}
	// A job that ended keeps its output but not its stream: it writes
	// nothing more, and a new job opens a new one.
	if ok && c.Job != nil && !c.Job.Running && !d.jobSettled {
		d.jobSettled = true
		cmds = append(cmds, d.job.Settle())
	}
	if ok && d.detailsFor != c.ID && d.tab == tabDetails {
		d.detailsFor, d.detailsErr = c.ID, ""
		d.inspected = server.ContainerDetails{}
		d.gen++
		cmds = append(cmds, fetchDetails(env.Client, d.gen, c.ID))
	}
	return tea.Batch(cmds...)
}

func fetchDetails(c *client.Client, gen int, id string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		var d server.ContainerDetails
		err := c.Do(ctx, http.MethodGet, "/api/containers/"+url.PathEscape(id), nil, &d)
		return detailsMsg{gen: gen, id: id, d: d, err: err}
	}
}

// ShowJob opens the Job tab once the snapshot shows the compose job that
// just started for the shown container.
func (d *Detail) ShowJob() { d.wantJob = true }

func (d *Detail) Update(env *ui.Env, msg tea.Msg) tea.Cmd {
	cmds := []tea.Cmd{d.sync(env)}
	switch msg := msg.(type) {
	case detailsMsg:
		if msg.gen == d.gen && msg.id == d.detailsFor {
			d.inspected = msg.d
			if msg.err != nil {
				d.detailsErr = msg.err.Error()
			}
		}
		return tea.Batch(cmds...)
	case tea.MouseClickMsg:
		m := msg.Mouse()
		if m.Y == 0 {
			if tab, ok := d.strip.Click(m.X); ok && m.Button == tea.MouseLeft {
				d.tab = tab
				cmds = append(cmds, d.sync(env))
			}
			return tea.Batch(cmds...)
		}
		m.X, m.Y = m.X-2, m.Y-1
		msg = tea.MouseClickMsg(m)
		if d.tab == tabDetails {
			d.facts.Click(m.Y)
			return tea.Batch(cmds...)
		}
		return tea.Batch(append(cmds, d.logsPane().Update(env, msg))...)
	case tea.MouseWheelMsg:
		m := msg.Mouse()
		if d.tab == tabDetails {
			dir := 1
			if m.Button == tea.MouseWheelUp {
				dir = -1
			}
			d.facts.Wheel(dir)
			return tea.Batch(cmds...)
		}
		m.X, m.Y = m.X-2, m.Y-1
		return tea.Batch(append(cmds, d.logsPane().Update(env, tea.MouseWheelMsg(m)))...)
	case tea.MouseMsg:
		return tea.Batch(cmds...)
	}
	// Each stream's message goes to the pane whose feed sent it, and only
	// that pane schedules the feed's next read.
	switch {
	case d.job.Owns(msg):
		cmds = append(cmds, d.job.Update(env, msg))
	case d.logs.Owns(msg):
		cmds = append(cmds, d.logs.Update(env, msg))
	default:
		cmds = append(cmds, d.logs.Update(env, msg), d.job.Update(env, msg))
	}
	return tea.Batch(cmds...)
}

// ActionLabel is how a menu names action id on the open tab, "" for the
// registry's own label: on the log tabs attach opens the container's
// shell, so it reads Shell (Attach is for a server's terminal).
func (d *Detail) ActionLabel(id string) string {
	if (d.tab == tabLogs || d.tab == tabJob) && id == "attach" {
		return "Shell"
	}
	return ""
}

// ActionOff is why action id does nothing on the open tab of the shown
// container, "" when it acts: on the log tabs attach opens the shell
// (containers.Off's rule for shell) and the engine keeps the logs.
func (d *Detail) ActionOff(env *ui.Env, id string) string {
	c, ok := d.container(env)
	if !ok {
		return "the container is gone"
	}
	if d.tab != tabLogs && d.tab != tabJob {
		return ""
	}
	switch id {
	case "attach":
		return Off(env, "shell", Item(c))
	case "clear-logs":
		return "the container engine keeps its logs"
	}
	return ""
}

// logsPane is the log pane of the open tab: the job's on the Job tab.
// Logs is the log pane of the tab shown: the container's logs or its job.
func (d *Detail) Logs() *logs.Pane { return d.logsPane() }

func (d *Detail) logsPane() *logs.Pane {
	if d.tab == tabJob {
		return d.job
	}
	return d.logs
}

// Do runs the tab keys, the main panel's scrolling and the open tab's
// actions. On the log tabs attach opens the container's shell and
// clear-logs says why it does not apply; on the Shell tab the container
// keys act on the shown container.
func (d *Detail) Do(env *ui.Env, a keymap.Action) tea.Cmd {
	c, ok := d.container(env)
	switch a.ID {
	case "tab-next", "tab-prev":
		n := d.tabs(c, ok)
		step := 1
		if a.ID == "tab-prev" {
			step = -1
		}
		d.tab = (d.tab + step + n) % n
		return d.sync(env)
	case "back":
		if (d.tab == tabLogs || d.tab == tabJob) && d.logsPane().Back() {
			return nil
		}
		return func() tea.Msg { return ui.FocusListMsg{} }
	}
	if a.Scope == keymap.ScopeContainers {
		if !ok {
			return nil
		}
		switch a.ID {
		case "down", "up", "next-page", "prev-page", "top", "bottom", "open-detail":
			return nil
		}
		act := ui.ActMsg{ID: a.ID, Item: Item(c)}
		return func() tea.Msg { return act }
	}
	if d.tab == tabLogs || d.tab == tabJob {
		switch a.ID {
		case "attach":
			if !ok {
				return nil
			}
			act := ui.ActMsg{ID: "shell", Item: Item(c)}
			return func() tea.Msg { return act }
		case "clear-logs":
			return func() tea.Msg {
				return ui.NoticeMsg{Text: "the container engine keeps a container's logs; there is no scrollback to clear", Level: ui.LevelInfo}
			}
		}
		return d.logsPane().Do(env, a)
	}
	switch a.ID {
	case "copy":
		return d.copyField()
	case "copy-dir":
		if !ok || c.WorkingDir == "" {
			return notice("no compose directory to copy", ui.LevelInfo)
		}
		return tea.Batch(tea.SetClipboard(c.WorkingDir), notice("copied "+viewfmt.PlainText(c.WorkingDir), ui.LevelSuccess))
	}
	if d.tab == tabDetails {
		d.facts.Do(a.ID)
	}
	return nil
}

func notice(text string, l ui.Level) tea.Cmd {
	return func() tea.Msg { return ui.NoticeMsg{Text: text, Level: l} }
}

func (d *Detail) copyField() tea.Cmd {
	label, value, ok := d.facts.Current()
	if !ok || value == "" {
		return notice("nothing to copy here", ui.LevelInfo)
	}
	what := label
	if what == "" {
		what = "the line"
	}
	return tea.Batch(tea.SetClipboard(value), notice("copied "+viewfmt.PlainText(what), ui.LevelSuccess))
}

// details are the Details tab's values: what the snapshot says, then what
// the inspection adds once it arrives.
func (d *Detail) details(env *ui.Env, c docker.Container) []detail.Fact {
	s := env.Styles
	st := StatusOf(c)
	var out []detail.Fact
	// add keeps a fact; a value of several lines (volumes, files, the
	// environment) becomes one fact a line, labeled on its first, so the
	// cursor and copy take one mount or one variable.
	add := func(label, value string, style lipgloss.Style) {
		if value == "" {
			return
		}
		for i, l := range strings.Split(value, "\n") {
			if i > 0 {
				label = ""
			}
			out = append(out, detail.Fact{Label: label, Value: l, Copy: l, Style: style})
		}
	}
	state := st.Label
	if c.Failed && c.ExitCode != nil {
		state += fmt.Sprintf(" (exit %d)", *c.ExitCode)
	}
	if c.OOMKilled {
		state += ", killed: out of memory"
	}
	add("State", state, s.State(string(st.Display)))
	add("Status", c.Status, s.Text2)
	add("Name", c.Name, s.Text)
	add("Image", c.Image, s.Text)
	if c.Project != "" {
		add("Project", c.Project+" / "+c.Service, s.Text)
	}
	add("Directory", c.WorkingDir, s.Text2)
	if c.Server != "" {
		owner := c.Server
		if c.OwnedBy != "" {
			owner += " (by " + string(c.OwnedBy) + ")"
		}
		add("Server", owner, s.Accent)
	}
	if len(c.SharedWith) > 0 {
		add("Shared with", strings.Join(c.SharedWith, ", "), s.Text2)
	}
	if len(c.IssueFor) > 0 {
		add("Needs attention", "for "+strings.Join(c.IssueFor, ", "), s.Warn)
	}
	var ports []string
	for _, p := range c.Ports {
		t := fmt.Sprintf("%d -> %d/%s", p.Public, p.Private, p.Proto)
		if p.NetworkWarning {
			t += " (reachable from the network)"
		}
		ports = append(ports, t)
	}
	add("Ports", strings.Join(ports, ", "), s.Port)
	if c.Restarts > 0 {
		add("Restarts", strconv.Itoa(c.Restarts), s.Warn)
	}
	if c.State == "running" {
		mem := viewfmt.HumanBytes(c.Mem)
		if c.MemLimit > 0 {
			mem += " of " + viewfmt.HumanBytes(c.MemLimit)
		}
		add("CPU, memory", fmt.Sprintf("%.1f%%, %s", c.CPU, mem), s.Text2)
	}
	if c.Job != nil {
		add("Compose job", jobLine(c.Job), jobStyle(env, c.Job))
	}
	if d.detailsFor != c.ID {
		return out
	}
	if d.detailsErr != "" {
		add("Inspect", d.detailsErr, s.Error)
		return out
	}
	in := d.inspected
	add("Command", in.Command, s.Text)
	if !in.Created.IsZero() {
		add("Created", in.Created.Local().Format("2006-01-02 15:04:05"), s.Text2)
	}
	add("Restart policy", in.RestartPolicy, s.Text2)
	if h := in.Health; h != nil {
		health := h.Status
		if h.FailingStreak > 0 {
			health += fmt.Sprintf(", %d failures in a row", h.FailingStreak)
		}
		if h.Output != "" {
			health += ": " + h.Output
		}
		add("Health check", health, s.Text2)
	}
	add("Error", in.Error, s.Error)
	var mounts []string
	for _, m := range in.Mounts {
		src := m.Source
		if m.Name != "" && m.Type == "volume" {
			src = m.Name
		}
		t := src + " -> " + m.Destination
		if m.ReadOnly {
			t += " (read-only)"
		}
		mounts = append(mounts, t)
	}
	add("Volumes", strings.Join(mounts, "\n"), s.Text2)
	var nets []string
	for _, n := range in.Networks {
		nets = append(nets, strings.TrimSpace(n.Name+" "+n.IP))
	}
	add("Networks", strings.Join(nets, ", "), s.Text2)
	add("Compose files", strings.Join(in.ConfigFiles, "\n"), s.Text2)
	add("Environment", envLines(in.Env), s.Text2)
	return out
}

// envLines are the container's variables as NAME=value lines in the
// engine's order, secret-looking values masked (viewfmt.SecretEnv, the
// rule of the servers' Env tab), with a last line saying so.
func envLines(vars []server.EnvVar) string {
	lines := make([]string, 0, len(vars)+1)
	hidden := 0
	for _, v := range vars {
		value := v.Value
		if viewfmt.SecretEnv(v.Key, v.Value) {
			value = viewfmt.SecretMask
			hidden++
		}
		lines = append(lines, v.Key+"="+value)
	}
	if hidden > 0 {
		word := "secrets"
		if hidden == 1 {
			word = "secret"
		}
		lines = append(lines, fmt.Sprintf("(%d %s hidden)", hidden, word))
	}
	return strings.Join(lines, "\n")
}

// jobLine says where a compose job is: running, failed and why, or done.
func jobLine(j *docker.JobView) string {
	switch {
	case j.Running:
		return Progressive(jobVerb(j.Kind)) + " (" + j.Kind + ")"
	case j.Error != "":
		return j.Kind + " failed: " + j.Error
	}
	return j.Kind + " done; :dismiss-job hides it"
}

func jobStyle(env *ui.Env, j *docker.JobView) lipgloss.Style {
	switch {
	case j.Running:
		return env.Styles.Starting
	case j.Error != "":
		return env.Styles.Error
	}
	return env.Styles.Running
}

// jobVerb is the action a job kind came from.
func jobVerb(kind string) string {
	switch kind {
	case "start", "stop", "down":
		return kind + "-project"
	}
	return "recreate"
}

func (d *Detail) View(env *ui.Env, w, h int, focused bool) string {
	c, ok := d.container(env)
	cw, ch := max(0, w-4), max(0, h-2)
	var lines []string
	status := ""
	switch {
	case !ok:
		lines = []string{env.Styles.Muted.Render("The container is gone")}
	case d.tab == tabLogs:
		lines = strings.Split(d.logs.View(env, cw, ch, false), "\n")
		status = d.logs.Status()
	case d.tab == tabJob && c.Job != nil:
		head := ansi.Truncate(jobLine(c.Job), cw, env.Glyphs.Ellipsis)
		lines = append([]string{jobStyle(env, c.Job).Render(head)}, strings.Split(d.job.View(env, cw, max(0, ch-1), false), "\n")...)
		status = d.job.Status()
	case d.tab == tabShell:
		lines = d.shellLines(env, c, cw)
	default:
		facts := d.details(env, c)
		if d.detailsFor == c.ID && d.inspected.ID == "" && d.detailsErr == "" {
			facts = append(facts, detail.Fact{Value: "Loading the inspection" + env.Glyphs.Ellipsis, Style: env.Styles.Muted, Note: true})
		}
		lines, status = d.facts.View(env, facts, cw, ch, focused)
	}
	title, right := d.header(env, c, ok, max(0, w-2), focused)
	return ui.WithBottom(env, ui.Box(env, w, h, focused, title, right, lines), w, focused, status)
}

// header is the top border: the container's state glyph and name, the
// tab strip and its state word at the right, for inner cells, laid out as
// a server's (detail.Strip).
func (d *Detail) header(env *ui.Env, c docker.Container, ok bool, inner int, focused bool) (string, string) {
	if !ok {
		return "Container", ""
	}
	s := env.Styles
	st := StatusOf(c)
	style := s.State(string(st.Display))
	n := d.tabs(c, ok)
	d.strip.Names, d.strip.Short, d.strip.Active = tabNames[:n], tabShort[:n], d.tab
	glyph := style.Render(env.Glyphs.State(st.Display, env.Frame))
	return d.strip.Header(env, glyph, viewfmt.PlainText(c.Name), []string{style.Render(st.Label)}, inner, focused)
}

func (d *Detail) shellLines(env *ui.Env, c docker.Container, w int) []string {
	s := env.Styles
	if c.State != "running" {
		return []string{s.Muted.Render("Start the container to open a shell.")}
	}
	text := "a opens a shell in " + c.Name + " (bash when the image has it, else sh); ctrl-] comes back here."
	var out []string
	for part := range strings.SplitSeq(ansi.Wordwrap(text, max(1, w), ""), "\n") {
		out = append(out, s.Text.Render(part))
	}
	return out
}
