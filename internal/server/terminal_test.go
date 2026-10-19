package server

import (
	"slices"
	"testing"
)

func TestTerminalCommand(t *testing.T) {
	onPath := map[string]bool{"custom-terminal": true, "ptyxis": true, "kitty": true, "zed": true}
	lookup := func(name string) string {
		if onPath[name] {
			return "/bin/" + name
		}
		return ""
	}
	macApps := map[string]string{"com.mitchellh.ghostty": "/Applications/Ghostty.app", "net.kovidgoyal.kitty": "/Applications/kitty.app",
		"com.apple.Terminal": "/System/Applications/Utilities/Terminal.app", "dev.zed.Zed": "/Applications/Zed.app"}
	dir := "/projects/a folder"
	for _, tc := range []struct {
		goos, setting, terminalEnv string
		args                       []string
		inDir                      bool
	}{
		{"linux", "custom-terminal --directory", "", []string{"/bin/custom-terminal", "--directory", dir}, true},
		{"linux", "custom-terminal --cwd={DIR}", "", []string{"/bin/custom-terminal", "--cwd=" + dir}, true},
		{"linux", "", "", []string{"/bin/kitty", "--directory", dir}, true},
		{"linux", "ptyxis", "", []string{"/bin/ptyxis", "--new-window", "--working-directory=" + dir}, true},
		{"linux", "", "ptyxis", []string{"/bin/ptyxis", "--new-window", "--working-directory=" + dir}, true},
		{"linux", "", "custom-terminal -e", []string{"/bin/custom-terminal", "-e"}, true},
		{"darwin", "", "", []string{"open", "-a", "/Applications/Ghostty.app", dir}, false},
		{"darwin", "kitty", "", []string{"open", "-n", "-a", "/Applications/kitty.app", "--args", "--directory", dir}, false},
		{"darwin", "Terminal", "", []string{"open", "-a", "/System/Applications/Utilities/Terminal.app", dir}, false},
		{"darwin", "Custom Terminal", "", []string{"open", "-a", "Custom Terminal", dir}, false},
	} {
		d := desktop{goos: tc.goos, lookup: lookup, apps: func() map[string]string { return macApps },
			getenv: func(k string) string {
				if k == "TERMINAL" {
					return tc.terminalEnv
				}
				return ""
			}}
		cmd, err := d.terminalCommand(tc.setting, dir)
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(cmd.Args, tc.args) || (cmd.Dir == dir) != tc.inDir {
			t.Errorf("%s %q: command = %v, dir = %s", tc.goos, tc.setting, cmd.Args, cmd.Dir)
		}
	}
	for _, tc := range []struct {
		goos, setting, visual string
		args                  []string
	}{
		{"linux", "", "", []string{"/bin/zed", dir}},
		{"linux", "", "vim", []string{"/bin/zed", dir}},
		{"darwin", "", "", []string{"open", "-a", "/Applications/Zed.app", dir}},
		{"darwin", "Zed", "", []string{"open", "-a", "/Applications/Zed.app", dir}},
		{"darwin", "Nova", "", []string{"open", "-a", "Nova", dir}},
	} {
		d := desktop{goos: tc.goos, lookup: lookup, apps: func() map[string]string { return macApps },
			getenv: func(k string) string {
				if k == "VISUAL" {
					return tc.visual
				}
				return ""
			}}
		cmd, err := d.editorCommand(tc.setting, dir)
		if err != nil || !slices.Equal(cmd.Args, tc.args) {
			t.Errorf("%s editor %q: command = %v, %v", tc.goos, tc.setting, cmd, err)
		}
	}
}
