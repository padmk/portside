package server

import (
	"runtime"
	"slices"
	"testing"
)

func TestTerminalCommand(t *testing.T) {
	lookup := func(name string) string {
		if name == "custom-terminal" {
			return "/bin/custom-terminal"
		}
		return ""
	}
	dir := "/projects/a folder"
	for _, tc := range []struct {
		setting string
		args    []string
	}{
		{"custom-terminal --directory", []string{"/bin/custom-terminal", "--directory", dir}},
		{"custom-terminal --cwd={DIR}", []string{"/bin/custom-terminal", "--cwd=" + dir}},
	} {
		cmd, err := terminalCommand(tc.setting, dir, lookup)
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(cmd.Args, tc.args) || cmd.Dir != dir {
			t.Errorf("command = %v, dir = %s", cmd.Args, cmd.Dir)
		}
	}
	if runtime.GOOS == "darwin" {
		cmd, err := terminalCommand("Custom Terminal", dir, lookup)
		if err != nil || !slices.Equal(cmd.Args, []string{"open", "-a", "Custom Terminal", dir}) {
			t.Fatalf("app command = %v, %v", cmd, err)
		}
	}
}
