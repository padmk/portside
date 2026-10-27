package config

import (
	"errors"
	"fmt"
	"path"
	"slices"
	"strings"
)

// Watch is a server's watch setting: file patterns relative to its
// directory whose changes restart it. A pattern is a slash-separated path
// whose segments follow path.Match, and a segment "**" stands for any
// number of directories, none included. "**" never enters the directories
// in WatchSkipped; a pattern that names one in a segment of its own
// reaches inside it.
type Watch struct {
	raw      []string
	patterns [][]string // segments of each pattern
}

// WatchSkipped are the directories "**" does not descend into: version
// control, dependencies, virtual environments, caches and build output.
var WatchSkipped = []string{".git", "node_modules", ".venv", "venv", "__pycache__", "target", "dist", "build", ".next"}

// ParseWatch reads the patterns of a watch setting; nil for none. A
// pattern that is empty, malformed, absolute, starts at the home directory
// or contains ".." is an error, so watching never leaves the directory.
func ParseWatch(patterns []string) (*Watch, error) {
	if len(patterns) == 0 {
		return nil, nil
	}
	w := &Watch{}
	for _, raw := range patterns {
		segs, err := watchSegments(raw)
		if err != nil {
			return nil, fmt.Errorf("watch %q: %w", raw, err)
		}
		w.raw = append(w.raw, raw)
		w.patterns = append(w.patterns, segs)
	}
	return w, nil
}

func watchSegments(raw string) ([]string, error) {
	p := strings.TrimSpace(raw)
	switch {
	case p == "":
		return nil, errors.New("a pattern must not be empty")
	case strings.HasPrefix(p, "/") || strings.HasPrefix(p, "~"):
		return nil, errors.New("patterns are relative to the server's dir; an absolute path is not allowed")
	case strings.HasSuffix(p, "/"):
		return nil, errors.New(`a pattern matches files; use "dir/**" for everything in a directory`)
	}
	var segs []string
	for seg := range strings.SplitSeq(p, "/") {
		switch {
		case seg == "" || seg == ".":
			continue
		case seg == "..":
			return nil, errors.New(`".." is not allowed: patterns stay inside the server's dir`)
		case seg != "**" && strings.Contains(seg, "**"):
			return nil, errors.New(`"**" must be a whole path segment, as in "src/**/*.py"`)
		}
		if _, err := path.Match(seg, ""); err != nil {
			return nil, errors.New("malformed pattern")
		}
		segs = append(segs, seg)
	}
	if len(segs) == 0 {
		return nil, errors.New("a pattern must name files")
	}
	return segs, nil
}

// Patterns returns the patterns as written.
func (w *Watch) Patterns() []string {
	if w == nil {
		return nil
	}
	return slices.Clone(w.raw)
}

// String identifies the patterns, for telling whether they changed.
func (w *Watch) String() string {
	if w == nil {
		return ""
	}
	return strings.Join(w.raw, "\x00")
}

// Match reports whether the file at rel, a slash-separated path relative
// to the server's directory, is watched.
func (w *Watch) Match(rel string) bool {
	return w != nil && w.match(strings.Split(rel, "/"), false)
}

// Enter reports whether a file inside the directory at rel, relative to
// the server's directory, can be watched, so a walk descends into it.
func (w *Watch) Enter(rel string) bool {
	return w != nil && w.match(strings.Split(rel, "/"), true)
}

func (w *Watch) match(segs []string, dir bool) bool {
	for _, p := range w.patterns {
		if matchSegments(p, segs, dir) {
			return true
		}
	}
	return false
}

// matchSegments matches path segments against pattern segments. With dir,
// segs is a directory, which matches when some path below it could match.
func matchSegments(pat, segs []string, dir bool) bool {
	for len(pat) > 0 {
		if pat[0] == "**" {
			if matchSegments(pat[1:], segs, dir) {
				return true
			}
			if len(segs) == 0 {
				return dir
			}
			if (dir || len(segs) > 1) && slices.Contains(WatchSkipped, segs[0]) {
				return false
			}
			segs = segs[1:]
			continue
		}
		if len(segs) == 0 {
			return dir
		}
		if ok, _ := path.Match(pat[0], segs[0]); !ok {
			return false
		}
		pat, segs = pat[1:], segs[1:]
	}
	return len(segs) == 0 && !dir
}
