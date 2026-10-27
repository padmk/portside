package config

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/padmk/portside/internal/paths"
)

// Settings are the top-level keys the dashboard edits, as written in the
// file: durations and sizes keep their text, and "" means unset. port,
// share, editor and terminal have their own controls.
type Settings struct {
	Root         string            `json:"root"`
	Path         []string          `json:"path"`
	Env          map[string]string `json:"env"`
	Restore      bool              `json:"restore"`
	Notify       bool              `json:"notify"`
	StopTimeout  string            `json:"stopTimeout"`
	Shell        string            `json:"shell"`
	DockerStart  string            `json:"dockerStart"`
	OnCrash      string            `json:"onCrash"`
	OnUnready    string            `json:"onUnready"`
	OnRecover    string            `json:"onRecover"`
	UnreadyAfter string            `json:"unreadyAfter"`
	RetryAfter   string            `json:"retryAfter"`
	MemoryLimit  string            `json:"memoryLimit"`
	LoginEnv     bool              `json:"loginEnv"`
	Umask        string            `json:"umask"`
}

// settingsOrder lists the keys of Settings in the file's spelling.
var settingsOrder = []string{
	"root", "path", "env", "restore", "notify", "stop_timeout", "shell", "docker_start",
	"on_crash", "on_unready", "on_recover", "unready_after", "retry_after", "memory_limit",
	"login_env", "umask",
}

// DefaultSettings are the values in effect for keys the config at path does
// not set; "" where the default is none, or the daemon's own.
func DefaultSettings(path string) Settings {
	return Settings{
		Root:         paths.ShortenHome(filepath.Dir(path)),
		Restore:      true,
		Notify:       true,
		StopTimeout:  shortDuration(DefaultStopTimeout),
		Shell:        DefaultShell,
		UnreadyAfter: shortDuration(DefaultUnreadyAfter),
		LoginEnv:     true,
	}
}

func settingsOf(raw rawConfig) Settings {
	orTrue := func(b *bool) bool { return b == nil || *b }
	s := Settings{
		Root: raw.Root, Path: raw.Path, Env: raw.Env, Restore: orTrue(raw.Restore), Notify: orTrue(raw.Notify),
		StopTimeout: durationText(raw.StopTimeout), Shell: raw.Shell, DockerStart: raw.DockerStart,
		OnCrash: raw.OnCrash, OnUnready: raw.OnUnready, OnRecover: raw.OnRecover,
		UnreadyAfter: durationText(raw.UnreadyAft), RetryAfter: durationText(raw.RetryAfter),
		LoginEnv: orTrue(raw.LoginEnv), Umask: raw.Umask,
	}
	if raw.MemoryLimit != nil {
		s.MemoryLimit = *raw.MemoryLimit
	}
	return s
}

// ReadSettings returns the settings written in the file at path and the
// file's revision.
func ReadSettings(path string) (Settings, string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Settings{}, "", err
	}
	var raw rawConfig
	if _, err := toml.Decode(string(data), &raw); err != nil {
		return Settings{}, "", fmt.Errorf("%s: %w", path, err)
	}
	return settingsOf(raw), Revision(data), nil
}

// settingLiterals returns the TOML literal of every key s sets to something
// other than its default.
func settingLiterals(s, def Settings) map[string]string {
	out := map[string]string{}
	str := func(k, v, d string) {
		if v != "" && v != d {
			out[k] = Quote(v)
		}
	}
	flag := func(k string, v, d bool) {
		if v != d {
			out[k] = strconv.FormatBool(v)
		}
	}
	str("root", s.Root, def.Root)
	if len(s.Path) > 0 {
		out["path"] = quoteList(s.Path)
	}
	if len(s.Env) > 0 {
		out["env"] = inlineTable(s.Env)
	}
	flag("restore", s.Restore, def.Restore)
	flag("notify", s.Notify, def.Notify)
	str("stop_timeout", s.StopTimeout, def.StopTimeout)
	str("shell", s.Shell, def.Shell)
	str("docker_start", s.DockerStart, def.DockerStart)
	str("on_crash", s.OnCrash, def.OnCrash)
	str("on_unready", s.OnUnready, def.OnUnready)
	str("on_recover", s.OnRecover, def.OnRecover)
	str("unready_after", s.UnreadyAfter, def.UnreadyAfter)
	str("retry_after", s.RetryAfter, def.RetryAfter)
	str("memory_limit", s.MemoryLimit, def.MemoryLimit)
	flag("login_env", s.LoginEnv, def.LoginEnv)
	str("umask", s.Umask, def.Umask)
	return out
}

// UpdateSettingsIfUnchanged writes the settings that differ from the file's,
// as UpdateServerIfUnchanged does for a server: a value equal to its default
// removes the key, and ErrConflict means the file is no longer at revision
// rev ("" skips the check). Comments and the other keys stay as they are.
func UpdateSettingsIfUnchanged(path string, s Settings, rev string) error {
	def := DefaultSettings(path)
	return editIfUnchanged(path, rev, func(d *doc) ([]string, error) {
		var raw rawConfig
		if _, err := toml.Decode(strings.Join(d.lines, "\n"), &raw); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		written := settingsOf(raw)
		// Changes are found on the values themselves, so clearing a default
		// written in the file removes it; what is written leaves defaults out.
		before, after := settingLiterals(written, Settings{}), settingLiterals(s, Settings{})
		write := settingLiterals(s, def)
		out := d.lines
		for _, k := range settingsOrder {
			if before[k] == after[k] {
				continue
			}
			if k == "env" && hasEntries(out, []string{"env"}) {
				out = patchEntries(out, []string{"env"}, written.Env, s.Env)
				continue
			}
			out = setTopLevel(out, k, write[k])
		}
		return out, nil
	})
}

// SetTopLevel sets a top-level key (outside any table) to a TOML literal.
func SetTopLevel(path, k, literal string) error {
	return edit(path, func(d *doc) ([]string, error) {
		return setTopLevel(d.lines, k, literal), nil
	})
}

var commentedTableRe = regexp.MustCompile(`^\s*#\s*\[`)

// setTopLevel sets the top-level key k to literal, or removes it for "".
// An existing key keeps its spelling and trailing comment. A new key goes
// below its commented-out example (as the starter file has them), else
// after the last top-level key, else before the first table and the
// comments that introduce it.
func setTopLevel(lines []string, k, literal string) []string {
	for _, a := range assignments(lines) {
		if samePath(a.path, []string{k}) {
			return patchValue(lines, []string{k}, literal)
		}
	}
	if literal == "" {
		return lines
	}
	d := newDoc(lines)
	firstTable := len(lines)
	for i := range lines {
		if d.isTable(i) {
			firstTable = i
			break
		}
	}
	line := k + " = " + literal
	insert := func(at int, ins ...string) []string {
		return append(append(append([]string{}, lines[:at]...), ins...), lines[at:]...)
	}
	example := regexp.MustCompile(`^\s*#\s*` + regexp.QuoteMeta(k) + `\s*=`)
	for i := 0; i < firstTable; i++ {
		if !d.isComment(i) {
			continue
		}
		if commentedTableRe.MatchString(lines[i]) {
			break // the examples below it belong to a commented-out table
		}
		if example.MatchString(lines[i]) {
			at := i + 1
			for at < firstTable && d.isComment(at) && !commentedTableRe.MatchString(lines[at]) {
				at++
			}
			return insert(at, line)
		}
	}
	last := -1
	for _, a := range assignments(lines) {
		if a.start < firstTable {
			last = a.end
		}
	}
	if last >= 0 {
		return insert(last, line)
	}
	at := firstTable
	// Keep comments that introduce the first table attached to it.
	for at > 0 && d.isComment(at-1) {
		at--
	}
	ins := []string{line}
	if at > 0 && strings.TrimSpace(lines[at-1]) != "" {
		ins = append([]string{""}, ins...)
	}
	if at < len(lines) {
		ins = append(ins, "")
	}
	return insert(at, ins...)
}
