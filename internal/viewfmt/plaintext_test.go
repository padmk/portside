package viewfmt

import (
	"reflect"
	"testing"
)

func TestPlainText(t *testing.T) {
	for in, want := range map[string]string{
		"npm run dev":                   "npm run dev",
		"a\x1b[2Jb":                     "a�[2Jb",
		"bell\a\x7f":                    "bell��",
		"left\u202eright\u2066":         "left�right�",
		"👩‍💻 dev":                       "👩‍💻 dev", // the joiner keeps the emoji whole
		"tab\there":                     "tab�here",
		"\x9b31m c1 control introducer": "�31m c1 control introducer",
	} {
		if got := PlainText(in); got != want {
			t.Errorf("PlainText(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPlainLines(t *testing.T) {
	got := PlainLines("one\r\n\ttwo\rthree\x1b\n")
	want := []string{"one", "    two", "three�"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("PlainLines = %q, want %q", got, want)
	}
}
