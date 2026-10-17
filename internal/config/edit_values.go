package config

import (
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
)

func specValues(s Spec) map[string]any {
	var v struct{ Servers map[string]map[string]any }
	_, _ = toml.Decode(Render(s), &v)
	return v.Servers[s.Name]
}

func tomlLiteral(v any) string {
	switch v := v.(type) {
	case string:
		return Quote(v)
	case []any:
		parts := make([]string, len(v))
		for i, item := range v {
			parts[i] = tomlLiteral(item)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case map[string]any:
		keys := make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		parts := make([]string, 0, len(keys))
		for _, k := range keys {
			parts = append(parts, key(k)+" = "+tomlLiteral(v[k]))
		}
		return "{ " + strings.Join(parts, ", ") + " }"
	default:
		return fmt.Sprint(v)
	}
}

type assignment struct {
	start, end int
	path       []string
}

// assignments uses the TOML parser to find complete values, including arrays,
// multiline strings, quoted keys and dotted keys.
func assignments(lines []string) []assignment {
	var out []assignment
	var table []string
	for i := 0; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		for end := i + 1; end <= len(lines); end++ {
			var v map[string]any
			meta, err := toml.Decode(strings.Join(lines[i:end], "\n"), &v)
			if err != nil {
				continue
			}
			keys := meta.Keys()
			if len(keys) == 0 {
				break
			}
			if strings.HasPrefix(line, "[") {
				table = append([]string{}, keys[0]...)
			} else {
				path := append(append([]string{}, table...), keys[0]...)
				out = append(out, assignment{i, end, path})
			}
			i = end - 1
			break
		}
	}
	return out
}

func hasEnvEntries(lines []string, name string) bool {
	for _, a := range assignments(lines) {
		if len(a.path) == 4 && reflect.DeepEqual(a.path[:3], []string{"servers", name, "env"}) {
			return true
		}
	}
	d := newDoc(lines)
	for i := range lines {
		if !d.isTable(i) {
			continue
		}
		var v map[string]any
		meta, err := toml.Decode(lines[i], &v)
		if err == nil && len(meta.Keys()) > 0 && reflect.DeepEqual([]string(meta.Keys()[0]), []string{"servers", name, "env"}) {
			return true
		}
	}
	return false
}

func patchValue(lines []string, path []string, literal string) []string {
	for _, a := range assignments(lines) {
		if !reflect.DeepEqual(a.path, path) {
			continue
		}
		var repl []string
		if literal != "" {
			// The key spelling and spacing before the value are unchanged.
			line := lines[a.start]
			eq := 0
			for eq < len(line) {
				if line[eq] == '"' {
					eq = skipBasic(line, eq+1)
					continue
				}
				if line[eq] == '\'' {
					for eq++; eq < len(line) && line[eq] != '\''; eq++ {
					}
				}
				if eq < len(line) && line[eq] == '=' {
					break
				}
				eq++
			}
			at := eq + 1
			for at < len(line) && (line[at] == ' ' || line[at] == '\t') {
				at++
			}
			repl = append(repl, line[:at]+literal)
		}
		d := newDoc(lines)
		for i := a.start; i < a.end; i++ {
			if at := d.comments[i]; at >= 0 {
				if i == a.start && len(repl) > 0 {
					repl[0] += " " + lines[i][at:]
				} else {
					repl = append(repl, lines[i][at:])
				}
			}
		}
		return append(append(append([]string{}, lines[:a.start]...), repl...), lines[a.end:]...)
	}
	if literal == "" {
		return lines
	}
	// Insert into the most specific existing table, before its next subtable.
	d := newDoc(lines)
	best, at := 0, len(lines)
	for i := range lines {
		if !d.isTable(i) {
			continue
		}
		var v map[string]any
		meta, err := toml.Decode(lines[i], &v)
		if err != nil || len(meta.Keys()) == 0 {
			continue
		}
		table := []string(meta.Keys()[0])
		if len(table) < len(path) && len(table) > best && reflect.DeepEqual(table, path[:len(table)]) {
			best, at = len(table), i+1
			for at < len(lines) && !d.isTable(at) {
				at++
			}
			for at > i+1 && (d.trimmed(at-1) == "" || d.isComment(at-1)) {
				at--
			}
		}
	}
	parts := make([]string, len(path)-best)
	for i, k := range path[best:] {
		parts[i] = key(k)
	}
	return append(append(append([]string{}, lines[:at]...), strings.Join(parts, ".")+" = "+literal), lines[at:]...)
}
