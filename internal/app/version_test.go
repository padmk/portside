package app

import "testing"

func TestOlder(t *testing.T) {
	defer func(v string) { Version = v }(Version)
	Version = "0.4.3"
	for v, want := range map[string]bool{
		"0.3.1":            true,
		"v0.4.2":           true,
		"0.4.3":            false,
		"v0.4.3":           false,
		"0.4.10":           false,
		"1.0.0":            false,
		"dev":              false,
		"":                 false,
		"0.4.2-5-g1246099": false,
		"0.4.2-dirty":      false,
		"0.4":              false,
		"0.04.1":           false,
	} {
		if got := Older(v); got != want {
			t.Errorf("Older(%q) with build 0.4.3 = %v, want %v", v, got, want)
		}
	}
	for _, own := range []string{"dev", "0.4.3-2-g1246099", "0.1.0-dev"} {
		Version = own
		if Older("0.0.1") {
			t.Errorf("Older(0.0.1) with build %q = true, want false", own)
		}
	}
}

func TestNewerAndReplacement(t *testing.T) {
	defer func(v string) { Version = v }(Version)
	Version = "0.4.3"
	for v, want := range map[string]struct {
		newer bool
		word  string
	}{
		"0.3.1":  {false, "upgraded"},
		"0.4.3":  {false, "replaced"},
		"0.4.10": {true, "downgraded"},
		"v1.0.0": {true, "downgraded"},
		"dev":    {false, "replaced"},
	} {
		if got := Newer(v); got != want.newer {
			t.Errorf("Newer(%q) with build 0.4.3 = %v, want %v", v, got, want.newer)
		}
		if got := Replacement(v); got != want.word {
			t.Errorf("Replacement(%q) with build 0.4.3 = %q, want %q", v, got, want.word)
		}
	}
	Version = "dev"
	if Newer("9.9.9") || Replacement("9.9.9") != "replaced" {
		t.Error("a development build compared with a release")
	}
}
