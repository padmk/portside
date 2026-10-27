package config

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Schedule is a parsed crontab expression: the five standard fields
// (minute, hour, day of month, month, day of week) or a macro, matched in
// the local time of the instant Next is given.
type Schedule struct {
	expr   string
	minute uint64 // bit n set: minute n matches
	hour   uint64
	dom    uint64 // days 1-31
	month  uint64 // months 1-12
	dow    uint64 // days 0-6, Sunday is 0
	// As in Vixie cron, when a day field is written starting with "*" a
	// day must match both fields (so "*/2" still counts); when neither is,
	// either field matching is enough.
	domAny, dowAny bool
}

var cronMacros = map[string]string{
	"@yearly":   "0 0 1 1 *",
	"@annually": "0 0 1 1 *",
	"@monthly":  "0 0 1 * *",
	"@weekly":   "0 0 * * 0",
	"@daily":    "0 0 * * *",
	"@midnight": "0 0 * * *",
	"@hourly":   "0 * * * *",
}

var (
	monthNames = []string{"jan", "feb", "mar", "apr", "may", "jun", "jul", "aug", "sep", "oct", "nov", "dec"}
	dayNames   = []string{"sun", "mon", "tue", "wed", "thu", "fri", "sat"}
)

type cronField struct {
	name     string
	min, max int
	names    []string // names for min, min+1, ...; nil for none
}

var cronFields = [5]cronField{
	{"minute", 0, 59, nil},
	{"hour", 0, 23, nil},
	{"day of month", 1, 31, nil},
	{"month", 1, 12, monthNames},
	{"day of week", 0, 7, dayNames}, // 7 is Sunday too
}

// ParseSchedule parses a crontab expression. Its errors do not repeat the
// expression; callers name the field and the value.
func ParseSchedule(expr string) (*Schedule, error) {
	text := strings.TrimSpace(expr)
	if strings.HasPrefix(text, "@") {
		m, ok := cronMacros[strings.ToLower(text)]
		if !ok {
			return nil, fmt.Errorf("unknown macro %s: use @hourly, @daily, @weekly, @monthly or @yearly", text)
		}
		text = m
	}
	parts := strings.Fields(text)
	if len(parts) != 5 {
		return nil, fmt.Errorf("want 5 fields (minute, hour, day of month, month, day of week), got %d", len(parts))
	}
	s := &Schedule{expr: strings.TrimSpace(expr)}
	sets := [5]*uint64{&s.minute, &s.hour, &s.dom, &s.month, &s.dow}
	for i, part := range parts {
		bits, err := parseCronField(part, cronFields[i])
		if err != nil {
			return nil, err
		}
		*sets[i] = bits
	}
	if s.dow&(1<<7) != 0 {
		s.dow = s.dow&^(1<<7) | 1
	}
	s.domAny = strings.HasPrefix(parts[2], "*")
	s.dowAny = strings.HasPrefix(parts[4], "*")
	if !s.possible() {
		return nil, errors.New("never matches: no month has that day")
	}
	return s, nil
}

// parseCronField parses a comma-separated list of values, ranges and steps.
func parseCronField(text string, f cronField) (uint64, error) {
	var bits uint64
	for item := range strings.SplitSeq(text, ",") {
		rng, stepText, hasStep := strings.Cut(item, "/")
		lo, hi := f.min, f.max
		switch {
		case rng == "*":
			if f.name == "day of week" {
				hi = 6
			}
		default:
			a, b, isRange := strings.Cut(rng, "-")
			var err error
			if lo, err = cronValue(a, f); err != nil {
				return 0, err
			}
			hi = lo
			if isRange {
				if hi, err = cronValue(b, f); err != nil {
					return 0, err
				}
				if hi < lo {
					return 0, fmt.Errorf("%s range %s goes backwards", f.name, rng)
				}
			} else if hasStep {
				return 0, fmt.Errorf("%s %s: a step needs * or a range, such as */%s", f.name, item, stepText)
			}
		}
		step := 1
		if hasStep {
			n, err := strconv.Atoi(stepText)
			if err != nil || n < 1 {
				return 0, fmt.Errorf("%s step %q must be a positive number", f.name, stepText)
			}
			step = n
		}
		for v := lo; ; v += step {
			bits |= 1 << v
			if hi-v < step { // also for a step too large to add
				break
			}
		}
	}
	return bits, nil
}

// cronValue reads one number or name of field f.
func cronValue(text string, f cronField) (int, error) {
	if text == "" {
		return 0, fmt.Errorf("%s is missing a value", f.name)
	}
	for i, n := range f.names {
		if strings.EqualFold(text, n) {
			return f.min + i, nil
		}
	}
	v, err := strconv.Atoi(text)
	if err != nil {
		return 0, fmt.Errorf("%s %q is not a number", f.name, text)
	}
	if v < f.min || v > f.max {
		return 0, fmt.Errorf("%s %d out of range %d-%d", f.name, v, f.min, f.max)
	}
	return v, nil
}

// daysIn is the most days each month can have (February in a leap year).
var daysIn = [13]int{0, 31, 29, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31}

// possible reports whether some date matches. Only when the day fields
// must both match can the day of month rule out every month; every date
// falls on each day of the week in some year.
func (s *Schedule) possible() bool {
	if !s.domAny && !s.dowAny {
		return true
	}
	for m := 1; m <= 12; m++ {
		if s.month&(1<<m) == 0 {
			continue
		}
		for d := 1; d <= daysIn[m]; d++ {
			if s.dom&(1<<d) != 0 {
				return true
			}
		}
	}
	return false
}

// String returns the expression as written.
func (s *Schedule) String() string { return s.expr }

// MarshalText writes the expression, so a Server survives JSON (as in a
// handoff to an upgraded daemon).
func (s *Schedule) MarshalText() ([]byte, error) { return []byte(s.expr), nil }

func (s *Schedule) UnmarshalText(b []byte) error {
	parsed, err := ParseSchedule(string(b))
	if err != nil {
		return err
	}
	*s = *parsed
	return nil
}

func (s *Schedule) dayMatches(c time.Time) bool {
	dom := s.dom&(1<<c.Day()) != 0
	dow := s.dow&(1<<int(c.Weekday())) != 0
	if s.domAny || s.dowAny {
		return dom && dow
	}
	return dom || dow
}

// Next returns the first time after after that the schedule matches, in
// after's location. A local time that occurs twice (a clock turned back)
// matches once, at its first occurrence; one that does not occur (a clock
// turned forward) matches at the first instant after the jump. It returns
// the zero time when nothing matches within ten years, which ParseSchedule
// rules out.
func (s *Schedule) Next(after time.Time) time.Time {
	loc := after.Location()
	// Wall-clock arithmetic in UTC, which has no clock changes.
	c := wallUTC(after).Add(time.Minute)
	limit := c.AddDate(10, 0, 0)
	for c.Before(limit) {
		switch {
		case s.month&(1<<int(c.Month())) == 0:
			c = time.Date(c.Year(), c.Month()+1, 1, 0, 0, 0, 0, time.UTC)
		case !s.dayMatches(c):
			c = time.Date(c.Year(), c.Month(), c.Day()+1, 0, 0, 0, 0, time.UTC)
		case s.hour&(1<<c.Hour()) == 0:
			c = time.Date(c.Year(), c.Month(), c.Day(), c.Hour()+1, 0, 0, 0, time.UTC)
		case s.minute&(1<<c.Minute()) == 0:
			c = c.Add(time.Minute)
		default:
			if t := instant(c, loc); t.After(after) {
				return t
			}
			c = c.Add(time.Minute)
		}
	}
	return time.Time{}
}

// instant returns when the wall-clock time c (written in UTC) occurs in
// loc: its first occurrence, or for a time skipped by a clock change, the
// instant the clock jumped.
func instant(c time.Time, loc *time.Location) time.Time {
	t := time.Date(c.Year(), c.Month(), c.Day(), c.Hour(), c.Minute(), 0, 0, loc)
	if !sameWall(t, c) {
		// Skipped: t lies on one side of the jump, in the zone that ends
		// or begins there.
		start, end := t.ZoneBounds()
		if wallUTC(t).Before(c) {
			return end
		}
		return start
	}
	// Repeated: the first occurrence is in the zone before t's.
	if start, _ := t.ZoneBounds(); !start.IsZero() {
		_, before := start.Add(-time.Second).Zone()
		_, now := t.Zone()
		if earlier := t.Add(time.Duration(now-before) * time.Second); earlier.Before(start) && sameWall(earlier, c) {
			return earlier
		}
	}
	return t
}

func sameWall(t, c time.Time) bool { return wallUTC(t).Equal(c) }

// wallUTC is t's wall-clock time to the minute, written in UTC.
func wallUTC(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), 0, 0, time.UTC)
}
