package app

import (
	"strconv"
	"strings"
)

// Older reports whether v, such as a running daemon's version, is a release
// older than this build. Only plain releases (MAJOR.MINOR.PATCH with an
// optional leading v) compare; a development or snapshot build on either
// side never counts as older, so nothing replaces a daemon on a guess.
func Older(v string) bool {
	have, ok := release(v)
	if !ok {
		return false
	}
	own, ok := release(Version)
	if !ok {
		return false
	}
	for i := range own {
		if have[i] != own[i] {
			return have[i] < own[i]
		}
	}
	return false
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
