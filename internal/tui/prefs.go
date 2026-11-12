package tui

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/padmk/portside/internal/paths"
)

// paths.TUIPrefsName is the preferences file in the state directory.

// prefs are the terminal UI's choices kept between runs.
type prefs struct {
	// Theme is a theme family id (theme.Lookup).
	Theme string `json:"theme,omitempty"`
	// Variant forces "dark" or "light"; empty follows the terminal.
	Variant string `json:"variant,omitempty"`
	// Icons is a glyph set id (theme.GlyphSets).
	Icons string `json:"icons,omitempty"`
}

func prefsPath() string { return filepath.Join(paths.StateDir(), paths.TUIPrefsName) }

// loadPrefs reads the preferences; a missing or unreadable file gives the
// defaults.
func loadPrefs() prefs {
	var p prefs
	data, err := os.ReadFile(prefsPath())
	if err != nil {
		return prefs{}
	}
	if json.Unmarshal(data, &p) != nil {
		return prefs{}
	}
	if p.Variant != "dark" && p.Variant != "light" {
		p.Variant = ""
	}
	return p
}

func (p prefs) save() error {
	path := prefsPath()
	if err := os.MkdirAll(filepath.Dir(path), paths.DirMode); err != nil {
		return err
	}
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	return paths.WriteFile(path, append(data, '\n'))
}
