package config

import (
	"strings"
	"testing"
	"time"
)

func TestParseSchedule(t *testing.T) {
	// Times are next runs after Thursday 2026-01-01 00:00 UTC.
	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		expr string
		want []string // the next runs, in order
		err  string
	}{
		{expr: "* * * * *", want: []string{"2026-01-01 00:01", "2026-01-01 00:02"}},
		{expr: "30 9 * * *", want: []string{"2026-01-01 09:30", "2026-01-02 09:30"}},
		{expr: "*/20 * * * *", want: []string{"2026-01-01 00:20", "2026-01-01 00:40", "2026-01-01 01:00"}},
		{expr: "5-50/15 3 * * *", want: []string{"2026-01-01 03:05", "2026-01-01 03:20", "2026-01-01 03:35", "2026-01-01 03:50", "2026-01-02 03:05"}},
		{expr: "0 8,12,18 * * *", want: []string{"2026-01-01 08:00", "2026-01-01 12:00", "2026-01-01 18:00"}},
		{expr: "0 9 * * MON-FRI", want: []string{"2026-01-01 09:00", "2026-01-02 09:00", "2026-01-05 09:00"}},
		{expr: "0 9 * * sat,7", want: []string{"2026-01-03 09:00", "2026-01-04 09:00", "2026-01-10 09:00"}},
		{expr: "0 0 1 jun-AUG *", want: []string{"2026-06-01 00:00", "2026-07-01 00:00", "2026-08-01 00:00", "2027-06-01 00:00"}},
		{expr: "0 0 31 * *", want: []string{"2026-01-31 00:00", "2026-03-31 00:00"}},
		{expr: "0 0 29 2 *", want: []string{"2028-02-29 00:00"}},
		// Vixie cron: both day fields restricted, either one matches.
		{expr: "0 12 13 * FRI", want: []string{"2026-01-02 12:00", "2026-01-09 12:00", "2026-01-13 12:00", "2026-01-16 12:00"}},
		// A day field starting with * makes a day match both fields, as in
		// Vixie cron: the 1st, 11th, 21st or 31st that is a Friday.
		{expr: "0 12 */10 * FRI", want: []string{"2026-05-01 12:00", "2026-07-31 12:00"}},
		{expr: "0 0 */2 * *", want: []string{"2026-01-03 00:00", "2026-01-05 00:00"}},
		{expr: "0 0 * * */2", want: []string{"2026-01-03 00:00", "2026-01-04 00:00"}},
		// A step larger than the field keeps only its start.
		{expr: "0 0 */9223372036854775807 * *", want: []string{"2026-02-01 00:00", "2026-03-01 00:00"}},
		{expr: "@hourly", want: []string{"2026-01-01 01:00", "2026-01-01 02:00"}},
		{expr: "@daily", want: []string{"2026-01-02 00:00"}},
		{expr: "@weekly", want: []string{"2026-01-04 00:00", "2026-01-11 00:00"}},
		{expr: "@monthly", want: []string{"2026-02-01 00:00"}},
		{expr: "@YEARLY", want: []string{"2027-01-01 00:00"}},

		{expr: "61 * * * *", err: "minute 61 out of range 0-59"},
		{expr: "0 24 * * *", err: "hour 24 out of range 0-23"},
		{expr: "0 0 0 * *", err: "day of month 0 out of range 1-31"},
		{expr: "0 0 * 13 *", err: "month 13 out of range 1-12"},
		{expr: "0 0 * * 8", err: "day of week 8 out of range 0-7"},
		{expr: "0 0 30 2 *", err: "never matches"},
		{expr: "0 0 31 4,6,9,11 *", err: "never matches"},
		{expr: "* * * *", err: "want 5 fields"},
		{expr: "0 * * * * *", err: "want 5 fields"},
		{expr: "", err: "want 5 fields"},
		{expr: "@reboot", err: "unknown macro"},
		{expr: "*/0 * * * *", err: "step"},
		{expr: "5/10 * * * *", err: "a step needs * or a range"},
		{expr: "30-10 * * * *", err: "goes backwards"},
		{expr: "x * * * *", err: `minute "x" is not a number`},
		{expr: "0 0 * FOO *", err: `month "FOO" is not a number`},
		{expr: "1,,2 * * * *", err: "minute is missing a value"},
	}
	for _, c := range cases {
		s, err := ParseSchedule(c.expr)
		if c.err != "" {
			if err == nil || !strings.Contains(err.Error(), c.err) {
				t.Errorf("%q: error %v, want %q", c.expr, err, c.err)
			}
			continue
		}
		if err != nil {
			t.Errorf("%q: %v", c.expr, err)
			continue
		}
		at := from
		for _, want := range c.want {
			at = s.Next(at)
			if got := at.Format("2006-01-02 15:04"); got != want {
				t.Errorf("%q: next %s, want %s", c.expr, got, want)
				break
			}
		}
	}
}

func TestScheduleConfigError(t *testing.T) {
	_, err := Parse("/x/portside.toml", []byte("[servers.job]\ncmd = \"true\"\nschedule = \"61 * * * *\"\n"))
	if err == nil || !strings.Contains(err.Error(), `[servers.job]: schedule "61 * * * *": minute 61 out of range 0-59`) {
		t.Fatalf("err = %v", err)
	}
	cfg, err := Parse("/x/portside.toml", []byte("[servers.job]\ncmd = \"true\"\noneshot = true\nschedule = \"0 9 * * mon-fri\"\n"))
	if err != nil {
		t.Fatal(err)
	}
	if s := cfg.Servers[0].Schedule; s == nil || s.String() != "0 9 * * mon-fri" {
		t.Errorf("schedule = %v", s)
	}
}

// Clock changes in a zone with daylight saving time: in Warsaw 02:00-03:00
// is skipped on 2026-03-29 and repeated on 2026-10-25.
func TestScheduleAcrossClockChanges(t *testing.T) {
	loc, err := time.LoadLocation("Europe/Warsaw")
	if err != nil {
		t.Skip("no time zone data:", err)
	}
	at := func(s string) time.Time {
		t.Helper()
		v, err := time.Parse(time.RFC3339, s)
		if err != nil {
			t.Fatal(err)
		}
		return v.In(loc)
	}
	cases := []struct {
		expr string
		from string
		want []string // instants in RFC 3339 with the local offset
	}{
		// Skipped: the run happens in the first minute after the jump, once.
		{"30 2 * * *", "2026-03-29T01:00:00+01:00", []string{"2026-03-29T03:00:00+02:00", "2026-03-30T02:30:00+02:00"}},
		{"*/15 2 * * *", "2026-03-29T01:00:00+01:00", []string{"2026-03-29T03:00:00+02:00", "2026-03-30T02:00:00+02:00"}},
		{"0 * * * *", "2026-03-29T01:30:00+01:00", []string{"2026-03-29T03:00:00+02:00", "2026-03-29T04:00:00+02:00"}},
		// Repeated: the run happens at the first occurrence only.
		{"30 2 * * *", "2026-10-25T01:00:00+02:00", []string{"2026-10-25T02:30:00+02:00", "2026-10-26T02:30:00+01:00"}},
		{"0 * * * *", "2026-10-25T01:30:00+02:00", []string{"2026-10-25T02:00:00+02:00", "2026-10-25T03:00:00+01:00"}},
		// Asked during the second occurrence: the hour is not run again.
		{"*/20 2 * * *", "2026-10-25T02:10:00+01:00", []string{"2026-10-26T02:00:00+01:00"}},
	}
	for _, c := range cases {
		s, err := ParseSchedule(c.expr)
		if err != nil {
			t.Fatal(err)
		}
		next := at(c.from)
		for _, want := range c.want {
			next = s.Next(next)
			if !next.Equal(at(want)) {
				t.Errorf("%q from %s: next %s, want %s", c.expr, c.from, next.Format(time.RFC3339), want)
				break
			}
		}
	}
}
