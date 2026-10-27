package config

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Size is an amount of memory in bytes. Binary says it was written in
// binary units (KiB, MiB, GiB, TiB), so it is shown in them too.
type Size struct {
	Bytes  int64
	Binary bool
}

// sizeUnits maps the accepted units, in lower case, to their byte counts.
var sizeUnits = map[string]struct {
	bytes  float64
	binary bool
}{
	"kb": {1e3, false}, "mb": {1e6, false}, "gb": {1e9, false}, "tb": {1e12, false},
	"kib": {1 << 10, true}, "mib": {1 << 20, true}, "gib": {1 << 30, true}, "tib": {1 << 40, true},
}

// ParseSize reads a size such as "2GB", "1.5 GiB" or "512MB" (units in any
// case). "0" is the zero size; any other number needs a unit.
func ParseSize(v string) (Size, error) {
	t := strings.TrimSpace(v)
	if t == "0" {
		return Size{}, nil
	}
	i := strings.IndexFunc(t, func(r rune) bool { return (r < '0' || r > '9') && r != '.' })
	if i < 0 && t != "" {
		i = len(t) // a bare number
	}
	if i <= 0 {
		return Size{}, fmt.Errorf("%q is not a size such as \"2GB\" or \"512MiB\"", v)
	}
	num, unit := t[:i], strings.ToLower(strings.TrimSpace(t[i:]))
	u, ok := sizeUnits[unit]
	if unit == "" {
		return Size{}, fmt.Errorf("%q needs a unit: KB, MB, GB, TB, KiB, MiB, GiB or TiB", v)
	}
	if !ok {
		return Size{}, fmt.Errorf("%q has an unknown unit; use KB, MB, GB, TB, KiB, MiB, GiB or TiB", v)
	}
	n, err := strconv.ParseFloat(num, 64)
	if err != nil {
		return Size{}, fmt.Errorf("%q is not a size such as \"2GB\" or \"512MiB\"", v)
	}
	b := n * u.bytes
	if b >= math.MaxInt64/2 {
		return Size{}, fmt.Errorf("%q is too large", v)
	}
	return Size{Bytes: int64(math.Round(b)), Binary: u.binary}, nil
}

// String shows the size in GB, MB or KB (GiB, MiB or KiB when Binary)
// with up to one decimal below 10 of the unit.
func (s Size) String() string { return s.format(1) }

// format is String with up to digits decimals below 10 of the unit.
func (s Size) format(digits int) string {
	base, units := 1000.0, [...]string{"KB", "MB", "GB", "TB"}
	if s.Binary {
		base, units = 1024, [...]string{"KiB", "MiB", "GiB", "TiB"}
	}
	v, unit := float64(s.Bytes)/base, units[0]
	for _, next := range units[1:] {
		if v < base {
			break
		}
		v, unit = v/base, next
	}
	if v >= 10 {
		return fmt.Sprintf("%.0f %s", v, unit)
	}
	text := strconv.FormatFloat(v, 'f', digits, 64)
	if strings.Contains(text, ".") {
		text = strings.TrimRight(strings.TrimRight(text, "0"), ".")
	}
	return text + " " + unit
}

// Over describes used, a size above s, in s's units: "2.3 GB". A value
// that would read the same as s gets more decimals.
func (s Size) Over(used int64) string {
	u := Size{Bytes: used, Binary: s.Binary}
	if text := u.format(1); text != s.String() {
		return text
	}
	return u.format(3)
}

var errNoSize = errors.New("memory_limit must be a size such as \"2GB\", or \"0\" for none")

// memoryLimit reads a memory_limit setting: def when it is not set, the
// zero Size (none) for "0".
func memoryLimit(v *string, def Size) (Size, error) {
	if v == nil {
		return def, nil
	}
	if strings.TrimSpace(*v) == "" {
		return Size{}, errNoSize
	}
	sz, err := ParseSize(*v)
	if err != nil {
		return Size{}, fmt.Errorf("memory_limit: %w", err)
	}
	return sz, nil
}
