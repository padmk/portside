package app

import (
	"strconv"
	"strings"
)

// Older reports whether v, such as a running daemon's version, is a release
// older than this build. Only plain releases (MAJOR.MINOR.PATCH with an
// optional leading v) compare; a development or snapshot build on either
// side never counts as older, so nothing replaces a daemon on a guess.
func Older(v string) bool { return compare(v) < 0 }

// Newer reports whether v is a release newer than this build, under the
// same rules as Older: a development or snapshot build is never newer.
func Newer(v string) bool { return compare(v) > 0 }

// Replacement words what replacing a daemon of version from with this build
// does: upgraded or downgraded between releases, otherwise replaced (an
// equal version, or a development build on either side).
func Replacement(from string) string {
	switch compare(from) {
	case -1:
		return "upgraded"
	case 1:
		return "downgraded"
	}
	return "replaced"
}

// compare orders v against this build: -1 older, 1 newer, 0 equal or not
// comparable.
func compare(v string) int {
	have, ok := release(v)
	if !ok {
		return 0
	}
	own, ok := release(Version)
	if !ok {
		return 0
	}
	for i := range own {
		if have[i] != own[i] {
			if have[i] < own[i] {
				return -1
			}
			return 1
		}
	}
	return 0
}

func release(v string) ([3]int, bool) {
	var out [3]int
	parts := strings.Split(strings.TrimPrefix(v, "v"), ".")
	if len(parts) != len(out) {
		return out, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 || p != strconv.Itoa(n) {
			return out, false
		}
		out[i] = n
	}
	return out, true
}
