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
)

// Spec describes a server as written to the file. Empty fields are omitted.
type Spec struct {
	Name      string            `json:"name"`
	Dir       string            `json:"dir"`
	Cmd       string            `json:"cmd"`
	Port      *int              `json:"port,omitempty"`
	Group     string            `json:"group,omitempty"`
	DependsOn []string          `json:"dependsOn,omitempty"`
	Env       map[string]string `json:"env,omitempty"`
	Restart   string            `json:"restart,omitempty"`
	Health    string            `json:"health,omitempty"`
	Icon      string            `json:"icon,omitempty"`
	Open      string            `json:"open,omitempty"`
	Stop      string            `json:"stopTimeout,omitempty"`
	Enabled   *bool             `json:"enabled,omitempty"`
	// Comment is written as # lines above the table header.
	Comment string `json:"comment,omitempty"`
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

// Render returns the TOML table for a server, ending with a newline.
func Render(s Spec) string {
	var b strings.Builder
	if s.Comment != "" {
		for _, line := range strings.Split(strings.TrimRight(s.Comment, "\n"), "\n") {
			b.WriteString("# " + strings.TrimSpace(line) + "\n")
		}
	}
	fmt.Fprintf(&b, "[servers.%s]\n", s.Name)
	if s.Dir != "" {
		fmt.Fprintf(&b, "dir = %s\n", Quote(s.Dir))
	}
	fmt.Fprintf(&b, "cmd = %s\n", Quote(s.Cmd))
	if s.Port != nil {
		fmt.Fprintf(&b, "port = %d\n", *s.Port)
	}
	if s.Group != "" {
		fmt.Fprintf(&b, "group = %s\n", Quote(s.Group))
	}
	if len(s.DependsOn) > 0 {
		q := make([]string, len(s.DependsOn))
		for i, d := range s.DependsOn {
			q[i] = Quote(d)
		}
		fmt.Fprintf(&b, "depends_on = [%s]\n", strings.Join(q, ", "))
	}
	if s.Restart != "" && s.Restart != RestartNo {
		fmt.Fprintf(&b, "restart = %s\n", Quote(s.Restart))
	}
	if s.Health != "" {
		fmt.Fprintf(&b, "health = %s\n", Quote(s.Health))
	}
	if s.Icon != "" {
		fmt.Fprintf(&b, "icon = %s\n", Quote(s.Icon))
	}
	if s.Open != "" {
		fmt.Fprintf(&b, "open = %s\n", Quote(s.Open))
	}
	if s.Stop != "" {
		fmt.Fprintf(&b, "stop_timeout = %s\n", Quote(s.Stop))
	}
	if s.Enabled != nil && !*s.Enabled {
		b.WriteString("enabled = false\n")
	}
	if len(s.Env) > 0 {
		keys := make([]string, 0, len(s.Env))
		for k := range s.Env {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		parts := make([]string, len(keys))
		for i, k := range keys {
			parts[i] = key(k) + " = " + Quote(s.Env[k])
		}
		fmt.Fprintf(&b, "env = { %s }\n", strings.Join(parts, ", "))
	}
	return b.String()
}

// SpecOf converts a loaded server back into a Spec, keeping the original dir text.
func SpecOf(s Server) Spec {
	sp := Spec{
		Name: s.Name, Dir: s.DirRaw, Cmd: s.Cmd, Group: s.Group, DependsOn: s.DependsOn,
		Env: s.Env, Restart: s.Restart, Health: s.Health, Open: s.Open, Stop: s.StopRaw,
		Icon: s.IconRaw,
	}
	if !s.PortAuto {
		p := s.Port
		sp.Port = &p
	}
	if !s.Enabled {
		f := false
		sp.Enabled = &f
	}
	return sp
}

var headerRe = regexp.MustCompile(`^\s*\[\s*servers\s*\.\s*"?([A-Za-z0-9_-]+)"?\s*(\.[^\]]*)?\]\s*(#.*)?$`)

// doc is a config file split into lines, with the lines that continue a
// multi-line string marked so that text inside a command (a heredoc, say)
// is never mistaken for a table header, key or comment.
type doc struct {
	lines []string
	cont  []bool
}

const (
	tripleBasic   = `"""`
	tripleLiteral = "'''"
)

func newDoc(lines []string) *doc {
	d := &doc{lines: lines, cont: make([]bool, len(lines))}
	state := 0 // 0 outside strings, 1 inside a basic triple string, 2 inside a literal one
	for i, l := range lines {
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
	text := strings.TrimRight(string(data), "\n")
	if text == "" {
		return nil, nil
	}
	return strings.Split(text, "\n"), nil
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
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(dir, "."+filepath.Base(path)+".lock"), os.O_CREATE|os.O_RDWR, 0o600)
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
	unlock, err := lock(path)
	if err != nil {
		return err
	}
	defer unlock()
	lines, err := readLines(path)
	if err != nil {
		return err
	}
	out, err := fn(newDoc(lines))
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
	mode := os.FileMode(0o644)
	if st, err := os.Stat(path); err == nil {
		mode = st.Mode().Perm()
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
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

// UpdateServer replaces the table of name with s (which may carry a new name).
// Comment lines directly above the table are kept unless s.Comment is set.
func UpdateServer(path, name string, s Spec) error {
	if err := ValidName(s.Name); err != nil {
		return err
	}
	return edit(path, func(d *doc) ([]string, error) {
		start, head, end, ok := d.block(name)
		if !ok {
			return nil, fmt.Errorf("server %q not found in %s", name, path)
		}
		if s.Name != name {
			if _, _, _, dup := d.block(s.Name); dup {
				return nil, fmt.Errorf("server %q already exists", s.Name)
			}
		}
		keep := d.lines[start:head]
		if s.Comment != "" {
			keep = nil
		}
		repl := append(append([]string{}, keep...), strings.Split(strings.TrimRight(Render(s), "\n"), "\n")...)
		return append(append(append([]string{}, d.lines[:start]...), repl...), d.lines[end:]...), nil
	})
}

// RemoveServer deletes a server table and the comments directly above it.
func RemoveServer(path, name string) error {
	return edit(path, func(d *doc) ([]string, error) {
		start, _, end, ok := d.block(name)
		if !ok {
			return nil, fmt.Errorf("server %q not found in %s", name, path)
		}
		out := append(append([]string{}, d.lines[:start]...), d.lines[end:]...)
		// Collapse the double blank line left behind.
		if start > 0 && start < len(out) && strings.TrimSpace(out[start-1]) == "" && strings.TrimSpace(out[start]) == "" {
			out = append(out[:start], out[start+1:]...)
		}
		return out, nil
	})
}

// SetKey sets one key inside a server table to a TOML literal, keeping every
// other line (and comment) of the table untouched.
func SetKey(path, name, k, literal string) error {
	keyRe := regexp.MustCompile(`^\s*` + regexp.QuoteMeta(k) + `\s*=`)
	return edit(path, func(d *doc) ([]string, error) {
		_, head, end, ok := d.block(name)
		if !ok {
			return nil, fmt.Errorf("server %q not found in %s", name, path)
		}
		lines := d.lines
		at := end
		for i := head + 1; i < end; i++ {
			if d.isTable(i) {
				at = i // keys go before any sub-table
				break
			}
			if !d.cont[i] && keyRe.MatchString(lines[i]) {
				lines[i] = k + " = " + literal
				return lines, nil
			}
		}
		return append(append(append([]string{}, lines[:at]...), k+" = "+literal), lines[at:]...), nil
	})
}

// SetEnabled toggles a server without touching anything else in its table.
func SetEnabled(path, name string, enabled bool) error {
	return SetKey(path, name, "enabled", strconv.FormatBool(enabled))
}

// SetTopLevel sets a top-level key (outside any table) to a TOML literal.
func SetTopLevel(path, k, literal string) error {
	keyRe := regexp.MustCompile(`^\s*` + regexp.QuoteMeta(k) + `\s*=`)
	return edit(path, func(d *doc) ([]string, error) {
		lines := d.lines
		firstTable := len(lines)
		for i := range lines {
			if d.isTable(i) {
				firstTable = i
				break
			}
		}
		for i := 0; i < firstTable; i++ {
			if !d.cont[i] && keyRe.MatchString(lines[i]) {
				lines[i] = k + " = " + literal
				return lines, nil
			}
		}
		at := firstTable
		// Keep comments that introduce the first table attached to it.
		for at > 0 && d.isComment(at-1) {
			at--
		}
		ins := []string{k + " = " + literal}
		if at > 0 && strings.TrimSpace(lines[at-1]) != "" {
			ins = append([]string{""}, ins...)
		}
		if at < len(lines) {
			ins = append(ins, "")
		}
		return append(append(append([]string{}, lines[:at]...), ins...), lines[at:]...), nil
	})
}
