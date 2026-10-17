package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDirectoryWarnings(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{"", ".", root} {
		if got := directoryWarnings(root, dir); len(got) != 0 {
			t.Errorf("%q: %v", dir, got)
		}
	}
	for _, dir := range []string{"missing", filepath.Join(root, "missing")} {
		if got := directoryWarnings(root, dir); len(got) != 1 || !strings.Contains(got[0], "does not exist") {
			t.Errorf("%q: %v", dir, got)
		}
	}
	file := filepath.Join(root, "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if got := directoryWarnings(root, file); len(got) != 1 || !strings.Contains(got[0], "not a directory") {
		t.Errorf("file: %v", got)
	}
}
