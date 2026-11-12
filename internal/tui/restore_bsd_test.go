//go:build darwin || freebsd || netbsd || openbsd

package tui_test

import "golang.org/x/sys/unix"

const getTermios = unix.TIOCGETA
