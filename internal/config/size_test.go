package config

import (
	"strings"
	"testing"
)

func TestParseSize(t *testing.T) {
	for _, c := range []struct {
		in   string
		want Size
		text string
	}{
		{"2GB", Size{Bytes: 2e9}, "2 GB"},
		{"1.5GB", Size{Bytes: 1.5e9}, "1.5 GB"},
		{"512 MB", Size{Bytes: 512e6}, "512 MB"},
		{"100mb", Size{Bytes: 100e6}, "100 MB"},
		{"2GiB", Size{Bytes: 2 << 30, Binary: true}, "2 GiB"},
		{"1.5 GiB", Size{Bytes: 3 << 29, Binary: true}, "1.5 GiB"},
		{"256MiB", Size{Bytes: 256 << 20, Binary: true}, "256 MiB"},
		{"64KiB", Size{Bytes: 64 << 10, Binary: true}, "64 KiB"},
		{"800KB", Size{Bytes: 800e3}, "800 KB"},
		{"1TB", Size{Bytes: 1e12}, "1 TB"},
		{".5GB", Size{Bytes: 5e8}, "500 MB"},
		{"0", Size{}, "0 KB"},
		{"0GB", Size{}, "0 KB"},
	} {
		got, err := ParseSize(c.in)
		if err != nil || got != c.want || got.String() != c.text {
			t.Errorf("ParseSize(%q) = %+v (%s), %v; want %+v (%s)", c.in, got, got, err, c.want, c.text)
		}
	}
	for _, bad := range []string{"", "2", "1.5", "2 G", "2GBs", "2 gigabytes", "-1GB", "1.2.3GB", "GB", "two GB", "1e3MB", "9999999TB"} {
		if got, err := ParseSize(bad); err == nil {
			t.Errorf("ParseSize(%q) = %+v, want an error", bad, got)
		} else if bad != "" && !strings.Contains(err.Error(), bad) {
			t.Errorf("ParseSize(%q) error %q does not name the value", bad, err)
		}
	}
}

func TestSizeOver(t *testing.T) {
	limit := Size{Bytes: 2e9}
	if got := limit.Over(2.3e9); got != "2.3 GB" {
		t.Errorf("Over(2.3e9) = %q", got)
	}
	if got := limit.Over(2.04e9); got != "2.04 GB" {
		t.Errorf("Over(2.04e9) = %q, want more decimals than the limit's 2 GB", got)
	}
	if got := (Size{Bytes: 100 << 20, Binary: true}).Over(300 << 20); got != "300 MiB" {
		t.Errorf("binary Over = %q", got)
	}
}

func TestParseMemoryLimit(t *testing.T) {
	text := `memory_limit = "2GB"
[servers.web]
cmd = "x"
[servers.api]
cmd = "x"
memory_limit = "1.5GiB"
[servers.tool]
cmd = "x"
memory_limit = "0"
[servers.db]
cmd = "docker compose up"
memory_limit = "1GB"
`
	cfg, err := Parse("/tmp/portside.toml", []byte(text))
	if err != nil {
		t.Fatal(err)
	}
	limits := map[string]Size{}
	for _, s := range cfg.Servers {
		limits[s.Name] = s.MemoryLimit
	}
	if limits["web"] != (Size{Bytes: 2e9}) || limits["api"] != (Size{Bytes: 3 << 29, Binary: true}) || limits["tool"] != (Size{}) {
		t.Errorf("limits = %+v", limits)
	}
	if len(cfg.Warnings) != 1 || !strings.Contains(cfg.Warnings[0], "db: memory_limit covers only the compose command") {
		t.Errorf("warnings = %q", cfg.Warnings)
	}
	if cfg, err := Parse("/tmp/portside.toml", []byte("[servers.web]\ncmd = \"x\"\n")); err != nil || cfg.Servers[0].MemoryLimit != (Size{}) {
		t.Errorf("no limit unless set: %+v, %v", cfg, err)
	}
	for _, bad := range []string{`memory_limit = "2"`, `memory_limit = ""`, `memory_limit = "2 bananas"`, `memory_limit = 2`} {
		if _, err := Parse("/tmp/portside.toml", []byte(bad+"\n[servers.web]\ncmd = \"x\"\n")); err == nil || !strings.Contains(err.Error(), "memory_limit") {
			t.Errorf("%s: %v", bad, err)
		}
		if _, err := Parse("/tmp/portside.toml", []byte("[servers.web]\ncmd = \"x\"\n"+bad+"\n")); err == nil || !strings.Contains(err.Error(), "memory_limit") {
			t.Errorf("server %s: %v", bad, err)
		}
	}
}
