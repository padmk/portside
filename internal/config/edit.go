package config

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"unicode/utf8"

	"github.com/padmk/portside/internal/app"
	"github.com/padmk/portside/internal/paths"
)

// Spec describes a server as written to the file. Empty fields are omitted;
// durations and sizes are the text written, such as "30s" or "2GB".
type Spec struct {
	Name       string            `json:"name"`
	Dir        string            `json:"dir"`
	Cmd        string            `json:"cmd"`
	Oneshot    bool              `json:"oneshot,omitempty"`
	Port       *int              `json:"port,omitempty"`
	Group      string            `json:"group,omitempty"`
	DependsOn  []string          `json:"dependsOn,omitempty"`
	Projects   []string          `json:"projects,omitempty"`
	Env        map[string]string `json:"env,omitempty"`
	Restart    string            `json:"restart,omitempty"`
	Health     string            `json:"health,omitempty"`
	ReadyLog   string            `json:"readyLog,omitempty"`
	ReadyAfter string            `json:"readyAfter,omitempty"`
	Icon       string            `json:"icon,omitempty"`
	Open       string            `json:"open,omitempty"`
	Stop       string            `json:"stopTimeout,omitempty"`
	Enabled    *bool             `json:"enabled,omitempty"`
	LAN        bool              `json:"lan,omitempty"`
	Schedule   string            `json:"schedule,omitempty"`
	Watch      []string          `json:"watch,omitempty"`
	Wrap       string            `json:"wrap,omitempty"`
	// The hook commands: nil inherits the top-level one, "" runs none.
	OnCrash      *string `json:"onCrash,omitempty"`
	OnUnready    *string `json:"onUnready,omitempty"`
	OnRecover    *string `json:"onRecover,omitempty"`
	UnreadyAfter string  `json:"unreadyAfter,omitempty"`
	RetryAfter   string  `json:"retryAfter,omitempty"`
	Umask        string  `json:"umask,omitempty"`
	MemoryLimit  string  `json:"memoryLimit,omitempty"`
	// Comment is written as # lines above the table header.
	Comment string `json:"comment,omitempty"`
}

// SpecDir is how the absolute directory abs is written as a server's dir:
// relative to root when inside it ("" for root itself), otherwise with the
// home directory shortened to ~.
func SpecDir(root, abs string) string {
	rel, err := filepath.Rel(root, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return paths.ShortenHome(abs)
	}
	if rel == "." {
		return ""
	}
	return rel
}

// Quote renders s as a TOML basic string.
func Quote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\t':
			b.WriteString(`\t`)
		case '\r':
			b.WriteString(`\r`)
		case utf8.RuneError:
			b.WriteString(`�`)
		default:
			if r < 0x20 || r == 0x7f {
				fmt.Fprintf(&b, `\u%04X`, r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}

var bareKeyRe = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

func key(k string) string {
	if bareKeyRe.MatchString(k) {
		return k
	}
	return Quote(k)
}

// specOrder is the order Render writes a server's keys in, so a key added
// by an edit lands where a new table would have it.
var specOrder = []string{
	"dir", "cmd", "oneshot", "port", "group", "depends_on", "projects", "restart", "health",
	"ready_log", "ready_after", "icon", "open", "stop_timeout", "enabled", "lan", "schedule",
	"watch", "wrap", "on_crash", "on_unready", "on_recover", "unready_after", "retry_after",
	"umask", "memory_limit", "env",
}

// specLiterals returns the TOML literal of every key s sets, without the
// defaults: restart when it is the default for what the entry is, enabled
// unless false, and wrap = "auto".
func specLiterals(s Spec) map[string]string {
	out := map[string]string{}
	str := func(k, v string) {
		if v != "" {
			out[k] = Quote(v)
		}
	}
	hook := func(k string, v *string) {
		if v != nil {
			out[k] = Quote(*v)
		}
	}
	str("dir", s.Dir)
	out["cmd"] = Quote(s.Cmd)
	if s.Oneshot {
		out["oneshot"] = "true"
	}
	if s.Port != nil {
		out["port"] = strconv.Itoa(*s.Port)
	}
	str("group", s.Group)
	if len(s.DependsOn) > 0 {
		out["depends_on"] = quoteList(s.DependsOn)
	}
	if len(s.Projects) > 0 {
		out["projects"] = quoteList(s.Projects)
	}
	if s.Restart != DefaultRestart(s.Oneshot) {
		str("restart", s.Restart)
	}
	str("health", s.Health)
	str("ready_log", s.ReadyLog)
	str("ready_after", s.ReadyAfter)
	str("icon", s.Icon)
	str("open", s.Open)
	str("stop_timeout", s.Stop)
	if s.Enabled != nil && !*s.Enabled {
		out["enabled"] = "false"
	}
	if s.LAN {
		out["lan"] = "true"
	}
	str("schedule", s.Schedule)
	if len(s.Watch) > 0 {
		out["watch"] = quoteList(s.Watch)
	}
	if s.Wrap != WrapAuto {
		str("wrap", s.Wrap)
	}
	hook("on_crash", s.OnCrash)
	hook("on_unready", s.OnUnready)
	hook("on_recover", s.OnRecover)
	str("unready_after", s.UnreadyAfter)
	str("retry_after", s.RetryAfter)
	str("umask", s.Umask)
	str("memory_limit", s.MemoryLimit)
	if len(s.Env) > 0 {
		out["env"] = inlineTable(s.Env)
	}
	return out
}

// quoteList renders a list of strings as a TOML array.
func quoteList(items []string) string {
	q := make([]string, len(items))
	for i, item := range items {
		q[i] = Quote(item)
	}
	return "[" + strings.Join(q, ", ") + "]"
}

// inlineTable renders a string map as a TOML inline table, keys sorted.
func inlineTable(m map[string]string) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = key(k) + " = " + Quote(m[k])
	}
	return "{ " + strings.Join(parts, ", ") + " }"
}

// Render returns the TOML table for a server, ending with a newline.
func Render(s Spec) string {
	var b strings.Builder
	if s.Comment != "" {
		for line := range strings.SplitSeq(strings.TrimRight(s.Comment, "\n"), "\n") {
			b.WriteString("# " + strings.TrimSpace(line) + "\n")
		}
	}
	fmt.Fprintf(&b, "[servers.%s]\n", s.Name)
	literals := specLiterals(s)
	for _, k := range specOrder {
		if v, ok := literals[k]; ok {
			fmt.Fprintf(&b, "%s = %s\n", k, v)
		}
	}
	return b.String()
}

// SpecOf returns a loaded server's entry as written in the file, with the
// restart policy in effect.
func SpecOf(s Server) Spec {
	sp := s.spec
	sp.Name = s.Name
	return sp
}

// writtenSpec is a server's entry as the file writes it; restart is the
// policy in effect.
func writtenSpec(name string, r rawServer, restart string) Spec {
	deref := func(v *string) string {
		if v == nil {
			return ""
		}
		return *v
	}
	return Spec{
		Name: name, Dir: r.Dir, Cmd: r.Cmd, Oneshot: r.Oneshot, Port: r.Port, Group: r.Group,
		DependsOn: r.DependsOn, Projects: r.Projects, Env: r.Env, Restart: restart, Health: r.Health,
		ReadyLog: r.ReadyLog, ReadyAfter: durationText(r.ReadyAfter), Icon: r.Icon, Open: r.Open,
		Stop: durationText(r.StopTimeout), Enabled: r.Enabled, LAN: r.LAN, Schedule: r.Schedule,
		Watch: r.Watch, Wrap: r.Wrap, OnCrash: r.OnCrash, OnUnready: r.OnUnready, OnRecover: r.OnRecover,
		UnreadyAfter: durationText(r.UnreadyAft), RetryAfter: durationText(r.RetryAfter),
		Umask: deref(r.Umask), MemoryLimit: deref(r.MemoryLimit),
	}
}

var headerRe = regexp.MustCompile(`^\s*\[\s*servers\s*\.\s*"?([A-Za-z0-9_-]+)"?\s*(\.[^\]]*)?\]\s*(#.*)?$`)

// doc is a config file split into lines, with the lines that continue a
// multi-line string marked so that text inside a command (a heredoc, say)
// is never mistaken for a table header, key or comment.
type doc struct {
	lines    []string
	cont     []bool
	comments []int
}

const (
	tripleBasic   = `"""`
	tripleLiteral = "'''"
)

func newDoc(lines []string) *doc {
	d := &doc{lines: lines, cont: make([]bool, len(lines)), comments: make([]int, len(lines))}
	state := 0 // 0 outside strings, 1 inside a basic triple string, 2 inside a literal one
	for i, l := range lines {
		d.comments[i] = -1
		d.cont[i] = state != 0
		j := 0
		for j < len(l) {
			switch state {
			case 1, 2:
				q, esc := tripleBasic, true
				if state == 2 {
					q, esc = tripleLiteral, false
				}
				k := closingTriple(l[j:], q, esc)
				if k < 0 {
					j = len(l)
				} else {
					j += k + 3
					state = 0
				}
			default:
				switch {
				case l[j] == '#':
					d.comments[i] = j
					j = len(l)
				case strings.HasPrefix(l[j:], tripleBasic):
					state, j = 1, j+3
				case strings.HasPrefix(l[j:], tripleLiteral):
					state, j = 2, j+3
				case l[j] == '"':
					j = skipBasic(l, j+1)
				case l[j] == '\'':
					j++
					for j < len(l) && l[j] != '\'' {
						j++
					}
					j++
				default:
					j++
				}
			}
		}
	}
	return d
}

// skipBasic returns the index after the closing quote of a one-line basic string.
func skipBasic(l string, j int) int {
	for j < len(l) {
		switch l[j] {
		case '\\':
			j += 2
		case '"':
			return j + 1
		default:
			j++
		}
	}
	return j
}

// closingTriple finds the end of a multi-line string; in basic strings an
// escaped quote does not count.
func closingTriple(s, q string, escapes bool) int {
	for i := 0; i+3 <= len(s); i++ {
		if escapes && s[i] == '\\' {
			i++
			continue
		}
		if s[i:i+3] == q {
			return i
		}
	}
	return -1
}

func (d *doc) trimmed(i int) string { return strings.TrimSpace(d.lines[i]) }
func (d *doc) isTable(i int) bool   { return !d.cont[i] && strings.HasPrefix(d.trimmed(i), "[") }
func (d *doc) isComment(i int) bool {
	return !d.cont[i] && strings.HasPrefix(d.trimmed(i), "#")
}
func (d *doc) header(i int) []string {
	if d.cont[i] {
		return nil
	}
	return headerRe.FindStringSubmatch(d.lines[i])
}

// block locates a server table: [start, end) line indexes including the
// comment lines directly above the header; head is the header line.
func (d *doc) block(name string) (start, head, end int, ok bool) {
	head = -1
	for i := range d.lines {
		if m := d.header(i); m != nil && m[1] == name && m[2] == "" {
			head = i
			break
		}
	}
	if head < 0 {
		return 0, 0, 0, false
	}
	end = len(d.lines)
	for i := head + 1; i < len(d.lines); i++ {
		if d.isTable(i) {
			if m := d.header(i); m != nil && m[1] == name && m[2] != "" {
				continue // sub-table of this server, e.g. [servers.web.env]
			}
			end = i
			break
		}
	}
	// Trailing comments and blank lines belong to whatever follows.
	for end > head+1 && !d.cont[end-1] && (d.trimmed(end-1) == "" || d.isComment(end-1)) {
		end--
	}
	start = head
	for start > 0 && d.isComment(start-1) {
		start--
	}
	return start, head, end, true
}

func readLines(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return splitLines(data), nil
}

// splitLines splits the file's text into lines for an edit.
func splitLines(data []byte) []string {
	// writeValidated ends the text with one newline; blank lines before it
	// stay.
	text := strings.TrimSuffix(string(data), "\n")
	if strings.TrimSpace(text) == "" {
		return nil
	}
	return strings.Split(text, "\n")
}

// writeValidated parses the new text first so that a bad edit never reaches disk.
func writeValidated(path string, lines []string) error {
	text := strings.Join(lines, "\n") + "\n"
	if _, err := Parse(path, []byte(text)); err != nil {
		return err
	}
	return writeFile(path, []byte(text))
}

// realPath follows a symlinked config (e.g. into a dotfiles repository) so
// that edits update the target instead of replacing the link.
func realPath(path string) string {
	if fi, err := os.Lstat(path); err == nil && fi.Mode()&os.ModeSymlink != 0 {
		if target, err := filepath.EvalSymlinks(path); err == nil {
			return target
		}
	}
	return path
}

// lock serialises read-modify-write cycles across processes (daemon, CLI).
func lock(path string) (func(), error) {
	dir := filepath.Dir(realPath(path))
	if err := os.MkdirAll(dir, paths.DirMode); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(dir, paths.ConfigLockName(path)), os.O_CREATE|os.O_RDWR, paths.FileMode)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return nil, err
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	}, nil
}

// edit runs fn on the current lines under the lock and writes the result.
func edit(path string, fn func(d *doc) ([]string, error)) error {
	return editIfUnchanged(path, "", fn)
}

// editIfUnchanged is edit that first returns ErrConflict, under the same
// lock, when the file no longer has revision rev ("" skips the check).
func editIfUnchanged(path, rev string, fn func(d *doc) ([]string, error)) error {
	unlock, err := lock(path)
	if err != nil {
		return err
	}
	defer unlock()
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if rev != "" && rev != Revision(data) {
		return ErrConflict
	}
	out, err := fn(newDoc(splitLines(data)))
	if err != nil {
		return err
	}
	return writeValidated(path, out)
}

// WriteAtomic replaces the file at path (or its symlink target) under the lock.
func WriteAtomic(path string, data []byte) error {
	unlock, err := lock(path)
	if err != nil {
		return err
	}
	defer unlock()
	return writeFile(path, data)
}

func writeFile(path string, data []byte) error {
	path = realPath(path)
	// The file can hold secrets in server environments: a new one is private
	// and an existing one keeps only its owner's permissions.
	mode := paths.FileMode
	if st, err := os.Stat(path); err == nil {
		mode = st.Mode().Perm() &^ 0o077
	}
	if err := os.MkdirAll(filepath.Dir(path), paths.DirMode); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// Revision identifies a version of the file's text for optimistic saves.
func Revision(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:8])
}

// ErrConflict means the file changed since the caller read it.
var ErrConflict = errors.New("the config file changed since it was loaded; reload and try again")

// ReplaceIfUnchanged writes text only if the file still has revision rev.
func ReplaceIfUnchanged(path string, text []byte, rev string) error {
	if _, err := Parse(path, text); err != nil {
		return err
	}
	unlock, err := lock(path)
	if err != nil {
		return err
	}
	defer unlock()
	cur, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if rev != Revision(cur) {
		return ErrConflict
	}
	return writeFile(path, text)
}

// AddServer appends a new server table.
func AddServer(path string, s Spec) error {
	if err := ValidName(s.Name); err != nil {
		return err
	}
	return edit(path, func(d *doc) ([]string, error) {
		if _, _, _, ok := d.block(s.Name); ok {
			return nil, fmt.Errorf("server %q already exists", s.Name)
		}
		lines := d.lines
		if len(lines) > 0 {
			lines = append(lines, "")
		}
		return append(lines, strings.Split(strings.TrimRight(Render(s), "\n"), "\n")...), nil
	})
}

// UpdateServer patches changed fields of name with s (which may carry a new name).
// Comment lines directly above the table are kept unless s.Comment is set.
func UpdateServer(path, name string, s Spec) error {
	return UpdateServerIfUnchanged(path, name, s, "")
}

// UpdateServerIfUnchanged is UpdateServer for an edit made from a form that
// showed revision rev of the file: it returns ErrConflict when the file has
// changed since ("" skips the check).
func UpdateServerIfUnchanged(path, name string, s Spec, rev string) error {
	if s.Name != name || name != app.ID {
		if err := ValidName(s.Name); err != nil {
			return err
		}
	}
	return editIfUnchanged(path, rev, func(d *doc) ([]string, error) {
		start, _, _, ok := d.block(name)
		if !ok {
			return nil, fmt.Errorf("server %q not found in %s", name, path)
		}
		if s.Name != name {
			if _, _, _, dup := d.block(s.Name); dup {
				return nil, fmt.Errorf("server %q already exists", s.Name)
			}
		}
		cfg, err := Parse(path, []byte(strings.Join(d.lines, "\n")))
		if err != nil {
			return nil, err
		}
		old, _ := cfg.Server(name)
		written := SpecOf(old)
		before, after := specLiterals(written), specLiterals(s)
		out := append([]string{}, d.lines...)
		for _, k := range specOrder {
			changed := before[k] != after[k]
			if k == "restart" {
				// The unwritten default depends on oneshot, so compare what the
				// entry means: a task turned into a server (or back) gets its
				// restart line reconciled even when both sides omit it.
				changed = written.Restart != s.Restart || written.Oneshot != s.Oneshot
			}
			if !changed {
				continue
			}
			table := []string{"servers", name, k}
			if k == "env" && hasEntries(out, table) {
				out = patchEntries(out, table, written.Env, s.Env)
				continue
			}
			out = patchValue(out, table, after[k])
		}
		if s.Comment != "" {
			_, head, _, _ := newDoc(out).block(name)
			comments := strings.Split(strings.TrimSuffix(Render(Spec{Name: name, Cmd: "x", Comment: s.Comment}), "\n"), "\n")
			out = append(append(append([]string{}, out[:start]...), comments[:len(comments)-2]...), out[head:]...)
		}
		if s.Name != name {
			renamed := newDoc(out)
			prefix := regexp.MustCompile(`^(\s*\[\s*servers\s*\.\s*)"?` + regexp.QuoteMeta(name) + `"?`)
			for i, line := range out {
				if m := renamed.header(i); m != nil && m[1] == name {
					out[i] = prefix.ReplaceAllString(line, "${1}"+s.Name)
				}
			}
			out = renameDependency(newDoc(out), name, s.Name)
		}
		return out, nil
	})
}

// patchEntries patches the entries of the string table at path (env) one
// by one from old to new, so each keeps its line and comment.
func patchEntries(lines, table []string, old, new map[string]string) []string {
	keys := map[string]bool{}
	for k := range old {
		keys[k] = true
	}
	for k := range new {
		keys[k] = true
	}
	ordered := make([]string, 0, len(keys))
	for k := range keys {
		ordered = append(ordered, k)
	}
	sort.Strings(ordered)
	for _, k := range ordered {
		v, exists := new[k]
		if ov, ok := old[k]; ok == exists && ov == v {
			continue
		}
		literal := ""
		if exists {
			literal = Quote(v)
		}
		lines = patchValue(lines, append(append([]string{}, table...), k), literal)
	}
	return lines
}

// renameDependency rewrites depends_on entries of other servers so that a
// rename keeps the file valid. Arrays may span several lines.
func renameDependency(d *doc, from, to string) []string {
	quoted := regexp.MustCompile(`(["'])` + regexp.QuoteMeta(from) + `(["'])`)
	for _, a := range assignments(d.lines) {
		if len(a.path) != 3 || a.path[0] != "servers" || a.path[2] != "depends_on" {
			continue
		}
		for i := a.start; i < a.end; i++ {
			line := d.lines[i]
			at := len(line)
			if d.comments[i] >= 0 {
				at = d.comments[i]
			}
			d.lines[i] = quoted.ReplaceAllString(line[:at], "${1}"+to+"${2}") + line[at:]
		}
	}
	return d.lines
}

// RemoveServer deletes a server table and the comments directly above it.
func RemoveServer(path, name string) error {
	return edit(path, func(d *doc) ([]string, error) {
		start, _, end, ok := d.block(name)
		if !ok {
			return nil, fmt.Errorf("server %q not found in %s", name, path)
		}
		out := append(append([]string{}, d.lines[:start]...), d.lines[end:]...)
		// Collapse the double blank line left behind, the blank line that
		// would now open the file, or the blank lines that would now end it.
		switch {
		case start == len(out):
			for len(out) > 0 && strings.TrimSpace(out[len(out)-1]) == "" {
				out = out[:len(out)-1]
			}
		case strings.TrimSpace(out[start]) == "" && (start == 0 || strings.TrimSpace(out[start-1]) == ""):
			out = append(out[:start], out[start+1:]...)
		}
		return out, nil
	})
}

// SetKey sets one key inside a server table to a TOML literal, keeping every
// other line (and comment) of the table untouched.
func SetKey(path, name, k, literal string) error {
	return edit(path, func(d *doc) ([]string, error) {
		if _, _, _, ok := d.block(name); !ok {
			return nil, fmt.Errorf("server %q not found in %s", name, path)
		}
		// patchValue keeps a comment on the line and the key's spelling, and
		// puts a new key before any sub-table.
		return patchValue(d.lines, []string{"servers", name, k}, literal), nil
	})
}

// SetEnabled toggles a server without touching anything else in its table.
func SetEnabled(path, name string, enabled bool) error {
	return SetKey(path, name, "enabled", strconv.FormatBool(enabled))
}

// LoadAssignedPorts loads the daemon's config and persists automatic ports
// under the edit lock, so later removals or reordering cannot change them.
// Automatic ports skip the ports avoid reports (nil avoids none), such as
// those of temporary servers, which the config does not list.
func LoadAssignedPorts(path string, avoid func(port int) bool) (*Config, error) {
	cfg, err := Load(path)
	if err != nil {
		return nil, err
	}
	needsWrite := false
	for _, sv := range cfg.Servers {
		needsWrite = needsWrite || sv.PortAuto
	}
	if !needsWrite {
		return cfg, nil
	}
	unlock, err := lock(path)
	if err != nil {
		return nil, err
	}
	defer unlock()
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if cfg, err = parse(path, data, avoid); err != nil {
		return nil, err
	}
	var lines []string
	for _, sv := range cfg.Servers {
		if !sv.PortAuto {
			continue
		}
		if lines == nil {
			lines, err = readLines(path)
			if err != nil {
				return nil, err
			}
		}
		lines = patchValue(lines, []string{"servers", sv.Name, "port"}, strconv.Itoa(sv.Port))
	}
	if lines == nil {
		return cfg, nil
	}
	if err := writeValidated(path, lines); err != nil {
		return nil, err
	}
	return Parse(path, []byte(strings.Join(lines, "\n")))
}
